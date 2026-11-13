package authz

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"google.golang.org/protobuf/proto"
)

var ErrInvalidBundle = errors.New("invalid authorization policy bundle")

type BundleStore struct {
	mu          sync.RWMutex
	keys        map[string]ed25519.PublicKey
	bundles     map[string]*authv1.AuthorizationPolicyBundle
	maxBytes    int
	maxGrants   int
	persistence BundlePersistence
}

type BundlePersistence interface {
	Load(context.Context, func([]byte) error) error
	Persist(context.Context, string, uint64, uint64, uint64, []byte, []byte) ([]byte, error)
}

func NewBundleStore(keys map[string]ed25519.PublicKey, maxBytes, maxGrants int) (*BundleStore, error) {
	if len(keys) == 0 || maxBytes <= 0 || maxGrants <= 0 {
		return nil, ErrInvalidBundle
	}
	trusted := make(map[string]ed25519.PublicKey, len(keys))
	for keyID, key := range keys {
		if keyID == "" || len(key) != ed25519.PublicKeySize {
			return nil, ErrInvalidBundle
		}
		trusted[keyID] = append(ed25519.PublicKey(nil), key...)
	}
	return &BundleStore{keys: trusted, bundles: make(map[string]*authv1.AuthorizationPolicyBundle), maxBytes: maxBytes, maxGrants: maxGrants}, nil
}

func (s *BundleStore) Install(raw []byte) error {
	return s.InstallContext(context.Background(), raw)
}

func (s *BundleStore) SetPersistence(persistence BundlePersistence) error {
	if persistence == nil {
		return ErrInvalidBundle
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bundles) != 0 || s.persistence != nil {
		return ErrInvalidBundle
	}
	s.persistence = persistence
	return nil
}

func (s *BundleStore) Restore(ctx context.Context) error {
	s.mu.RLock()
	persistence := s.persistence
	s.mu.RUnlock()
	if persistence == nil {
		return ErrInvalidBundle
	}
	return persistence.Load(ctx, func(raw []byte) error {
		bundle, err := s.verify(raw)
		if err != nil {
			return err
		}
		return s.installVerified(bundle)
	})
}

func (s *BundleStore) InstallContext(ctx context.Context, raw []byte) error {
	bundle, err := s.verify(raw)
	if err != nil {
		return err
	}
	s.mu.RLock()
	persistence := s.persistence
	s.mu.RUnlock()
	if persistence != nil {
		latestRaw, persistErr := persistence.Persist(ctx, bundle.TenantId, bundle.BundleSequence, bundle.CatalogVersion, bundle.RevokeEpoch, bundle.ContentHash, raw)
		if persistErr != nil {
			return persistErr
		}
		latest, verifyErr := s.verify(latestRaw)
		if verifyErr != nil || latest.TenantId != bundle.TenantId || latest.BundleSequence < bundle.BundleSequence {
			return ErrInvalidBundle
		}
		if latest.BundleSequence == bundle.BundleSequence && !bytes.Equal(latest.ContentHash, bundle.ContentHash) {
			return ErrInvalidBundle
		}
		bundle = latest
	}
	return s.installVerified(bundle)
}

func (s *BundleStore) installVerified(bundle *authv1.AuthorizationPolicyBundle) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.bundles[bundle.TenantId]; current != nil {
		if bundle.BundleSequence < current.BundleSequence {
			return nil
		}
		if bundle.BundleSequence == current.BundleSequence {
			if bytes.Equal(bundle.ContentHash, current.ContentHash) {
				return nil
			}
			return ErrInvalidBundle
		}
		if bundle.RevokeEpoch < current.RevokeEpoch {
			return ErrInvalidBundle
		}
	}
	s.bundles[bundle.TenantId] = bundle
	return nil
}

func (s *BundleStore) Ready(tenantIDs []string) error {
	now := uint64(time.Now().UTC().UnixMilli())
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, tenantID := range tenantIDs {
		bundle := s.bundles[tenantID]
		if bundle == nil || bundle.ValidFromEpochMs > now || bundle.ExpiresAtEpochMs <= now {
			return ErrInvalidBundle
		}
	}
	return nil
}

func (s *BundleStore) Authorize(tenantID string, actor application.Actor, ownerID, manifestVersion string, manifestDigest []byte, capability, resourceType, action, resourceID string, requirements application.AuthorizationRequirements, now time.Time) error {
	s.mu.RLock()
	bundle := s.bundles[tenantID]
	s.mu.RUnlock()
	if bundle == nil || bundle.TenantId != tenantID || now.UnixMilli() < int64(bundle.ValidFromEpochMs) || now.UnixMilli() >= int64(bundle.ExpiresAtEpochMs) || actor.RevokeEpoch < bundle.RevokeEpoch {
		return application.ErrForbidden
	}
	for _, revocation := range bundle.ActorRevokeEpochs {
		if revocation.ActorId == actor.ID && actor.RevokeEpoch < revocation.RevokeEpoch {
			return application.ErrForbidden
		}
	}
	if !contains(actor.Capabilities, capability) {
		return application.ErrForbidden
	}
	definitionFound := false
	definitionScopes := make(map[string]struct{})
	defaultReadable, defaultWritable := make(map[string]struct{}), make(map[string]struct{})
	for _, definition := range bundle.ResourceCatalog {
		if definition != nil && definition.OwnerId == ownerID && definition.ManifestVersion == manifestVersion && bytes.Equal(definition.ManifestDigest, manifestDigest) && definition.ResourceType == resourceType && definition.Action == action && definition.Capability == capability {
			definitionFound = true
			for _, scope := range definition.SupportedScopeTypes {
				definitionScopes[scope] = struct{}{}
			}
			for _, field := range definition.ReadableFields {
				defaultReadable[field] = struct{}{}
			}
			for _, field := range definition.WritableFields {
				defaultWritable[field] = struct{}{}
			}
		}
	}
	if !definitionFound {
		return application.ErrForbidden
	}
	supportsScope := func(scope string) bool { _, ok := definitionScopes[scope]; return ok }
	roleSet := makeSet(actor.Roles)
	allow, deny := false, false
	allowedReadable, allowedWritable := make(map[string]struct{}), make(map[string]struct{})
	for _, grant := range bundle.ResourceGrants {
		if grant == nil || grant.OwnerId != ownerID || grant.ResourceType != resourceType || grant.Action != action || !subjectMatches(grant.ActorIds, grant.Roles, actor.ID, roleSet) || !hasAll(actor.Capabilities, grant.RequiredCapabilities) {
			continue
		}
		// These authoring resources currently have no authoritative branch, queue,
		// or creator resolver. Such grants fail closed instead of being widened.
		if len(grant.BranchScopeIds) > 0 || len(grant.QueueScopeIds) > 0 || grant.OwnScope {
			return application.ErrForbidden
		}
		if grant.TenantScope {
			if len(grant.ResourceScopeIds) > 0 || !supportsScope("tenant") {
				return application.ErrForbidden
			}
		} else {
			if !supportsScope("resource") {
				return application.ErrForbidden
			}
			if len(grant.ResourceScopeIds) == 0 || resourceID == "" {
				continue
			}
			if !contains(grant.ResourceScopeIds, resourceID) {
				continue
			}
		}
		readable := effectiveFields(grant.ReadableFields, defaultReadable)
		writable := effectiveFields(grant.WritableFields, defaultWritable)
		if !subset(readable, defaultReadable) || !subset(writable, defaultWritable) {
			return application.ErrForbidden
		}
		switch grant.Effect {
		case authv1.AuthorizationPolicyEffect_AUTHORIZATION_POLICY_EFFECT_ALLOW:
			allow = true
			mergeFields(allowedReadable, readable)
			mergeFields(allowedWritable, writable)
		case authv1.AuthorizationPolicyEffect_AUTHORIZATION_POLICY_EFFECT_DENY:
			if len(requirements.ReadFields) == 0 && len(requirements.WriteFields) == 0 {
				deny = true
			}
			for _, field := range requirements.ReadFields {
				if _, found := readable[field]; found {
					deny = true
				}
			}
			for _, field := range requirements.WriteFields {
				if _, found := writable[field]; found {
					deny = true
				}
			}
		default:
			return application.ErrForbidden
		}
	}
	if !allow || deny {
		return application.ErrForbidden
	}
	for _, field := range requirements.ReadFields {
		if _, found := allowedReadable[field]; !found {
			return application.ErrForbidden
		}
	}
	for _, field := range requirements.WriteFields {
		if _, found := allowedWritable[field]; !found {
			return application.ErrForbidden
		}
	}
	return nil
}

func (s *BundleStore) verify(raw []byte) (*authv1.AuthorizationPolicyBundle, error) {
	if len(raw) == 0 || len(raw) > s.maxBytes {
		return nil, ErrInvalidBundle
	}
	bundle := &authv1.AuthorizationPolicyBundle{}
	if (&proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, bundle) != nil || bundle.GetSchemaVersion() != 1 || bundle.GetTenantId() == "" || bundle.GetBundleSequence() == 0 || bundle.GetPolicyVersion() == "" || bundle.GetCatalogVersion() == 0 || bundle.GetValidFromEpochMs() == 0 || bundle.GetValidFromEpochMs() > math.MaxInt64 || bundle.GetExpiresAtEpochMs() > math.MaxInt64 || bundle.GetExpiresAtEpochMs() <= bundle.GetValidFromEpochMs() || len(bundle.GetSignature()) != ed25519.SignatureSize || len(bundle.GetContentHash()) != sha256.Size || len(bundle.GetResourceGrants())+len(bundle.GetGrants()) > s.maxGrants {
		return nil, ErrInvalidBundle
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(bundle)
	if err != nil || !bytes.Equal(encoded, raw) {
		return nil, ErrInvalidBundle
	}
	unsigned := proto.Clone(bundle).(*authv1.AuthorizationPolicyBundle)
	unsigned.ContentHash, unsigned.Signature = nil, nil
	canonical, err := (proto.MarshalOptions{Deterministic: true}).Marshal(unsigned)
	if err != nil {
		return nil, ErrInvalidBundle
	}
	digest := sha256.Sum256(canonical)
	key := s.keys[bundle.GetSigningKeyId()]
	if !bytes.Equal(digest[:], bundle.GetContentHash()) || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, digest[:], bundle.GetSignature()) {
		return nil, ErrInvalidBundle
	}
	if !validCatalog(bundle) || !validGrantIDs(bundle) {
		return nil, ErrInvalidBundle
	}
	return bundle, nil
}

func validCatalog(bundle *authv1.AuthorizationPolicyBundle) bool {
	seen := make(map[string]struct{}, len(bundle.ResourceCatalog))
	for _, item := range bundle.ResourceCatalog {
		if item == nil || item.OwnerId == "" || item.ManifestVersion == "" || len(item.ManifestDigest) != sha256.Size || item.ResourceType == "" || item.Action == "" || item.Capability == "" {
			return false
		}
		key := strings.Join([]string{item.OwnerId, item.ResourceType, item.Action, item.Capability}, "\x00")
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validGrantIDs(bundle *authv1.AuthorizationPolicyBundle) bool {
	seen := make(map[string]struct{}, len(bundle.Grants)+len(bundle.ResourceGrants))
	for _, grant := range bundle.Grants {
		if grant == nil || grant.GrantId == "" {
			return false
		}
		if _, exists := seen[grant.GrantId]; exists {
			return false
		}
		seen[grant.GrantId] = struct{}{}
	}
	for _, grant := range bundle.ResourceGrants {
		if grant == nil || grant.GrantId == "" || grant.OwnerId == "" || grant.ResourceType == "" || grant.Action == "" || (len(grant.ActorIds) == 0 && len(grant.Roles) == 0) {
			return false
		}
		if _, exists := seen[grant.GrantId]; exists {
			return false
		}
		seen[grant.GrantId] = struct{}{}
		narrow := len(grant.BranchScopeIds)+len(grant.QueueScopeIds)+len(grant.ResourceScopeIds) > 0 || grant.OwnScope
		if grant.TenantScope == narrow {
			return false
		}
	}
	return true
}

type Authorizer struct {
	bundles         *BundleStore
	ownerID         string
	manifestVersion string
	manifestDigest  []byte
	operations      map[string]manifestOperation
	now             func() time.Time
}

func NewAuthorizer(bundles *BundleStore, ownerID, manifestPath string, now func() time.Time) (*Authorizer, error) {
	if bundles == nil || ownerID == "" || manifestPath == "" || now == nil {
		return nil, errors.New("authoring bundle evaluator dependencies are incomplete")
	}
	manifest, err := loadManifest(manifestPath)
	if err != nil || manifest.OwnerID != ownerID || manifest.ManifestVersion == "" || len(manifest.Entries) == 0 {
		return nil, errors.New("authoring authorization owner manifest is invalid")
	}
	rawEntries, err := json.Marshal(manifest.Entries)
	if err != nil {
		return nil, errors.New("authoring authorization owner manifest is invalid")
	}
	var canonicalEntries any
	if err = json.Unmarshal(rawEntries, &canonicalEntries); err != nil {
		return nil, errors.New("authoring authorization owner manifest is invalid")
	}
	encoded, err := canonicalJSON(canonicalEntries)
	if err != nil {
		return nil, errors.New("authoring authorization owner manifest is invalid")
	}
	digest := sha256.Sum256(encoded)
	operations := make(map[string]manifestOperation, len(manifest.Entries))
	for _, rawEntry := range manifest.Entries {
		var entry ownerManifestEntry
		if err = json.Unmarshal(rawEntry, &entry); err != nil {
			return nil, errors.New("authoring authorization owner manifest entry is invalid")
		}
		if entry.ResourceType == "" || entry.Action == "" || entry.Capability == "" {
			return nil, errors.New("authoring authorization owner manifest entry is invalid")
		}
		if _, exists := operations[entry.Capability]; exists {
			return nil, errors.New("authoring authorization owner manifest has duplicate capabilities")
		}
		operations[entry.Capability] = manifestOperation{ResourceType: entry.ResourceType, Action: entry.Action}
	}
	return &Authorizer{bundles: bundles, ownerID: ownerID, manifestVersion: manifest.ManifestVersion, manifestDigest: digest[:], operations: operations, now: now}, nil
}

func canonicalJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func (a *Authorizer) Authorize(_ context.Context, tenantID string, actor application.Actor, capability, resourceID string, requirements application.AuthorizationRequirements) error {
	operation, ok := a.operations[capability]
	if !ok {
		return application.ErrForbidden
	}
	return a.bundles.Authorize(tenantID, actor, a.ownerID, a.manifestVersion, a.manifestDigest, capability, operation.ResourceType, operation.Action, resourceID, requirements, a.now().UTC())
}

type manifestOperation struct{ ResourceType, Action string }

type ownerManifest struct {
	OwnerID         string            `json:"owner_id"`
	ManifestVersion string            `json:"manifest_version"`
	Entries         []json.RawMessage `json:"entries"`
}

type ownerManifestEntry struct {
	ResourceType string `json:"resource_type"`
	Action       string `json:"action"`
	Capability   string `json:"capability"`
}

func loadManifest(path string) (ownerManifest, error) {
	var manifest ownerManifest
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 1<<20 {
		return manifest, errors.New("owner manifest is unavailable or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&manifest); err != nil {
		return ownerManifest{}, err
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ownerManifest{}, errors.New("owner manifest must contain exactly one JSON value")
	}
	return manifest, nil
}

func subjectMatches(actorIDs, roles []string, actorID string, roleSet map[string]struct{}) bool {
	if contains(actorIDs, actorID) {
		return true
	}
	for _, role := range roles {
		if _, found := roleSet[role]; found {
			return true
		}
	}
	return false
}

func hasAll(values, required []string) bool {
	for _, value := range required {
		if !contains(values, value) {
			return false
		}
	}
	return true
}
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func makeSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func effectiveFields(grantFields []string, defaults map[string]struct{}) map[string]struct{} {
	if len(grantFields) == 0 {
		return defaults
	}
	return makeSet(grantFields)
}

func subset(values, allowed map[string]struct{}) bool {
	for value := range values {
		if _, found := allowed[value]; !found {
			return false
		}
	}
	return true
}

func mergeFields(destination, source map[string]struct{}) {
	for field := range source {
		destination[field] = struct{}{}
	}
}

var _ application.Authorizer = (*Authorizer)(nil)
