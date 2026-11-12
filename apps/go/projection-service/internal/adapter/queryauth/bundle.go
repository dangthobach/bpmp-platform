package queryauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	authorizationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

var ErrInvalidPolicyBundle = errors.New("invalid signed authorization policy bundle")

type BundlePersistence interface {
	Load(context.Context, func([]byte) error) error
	Persist(context.Context, string, uint64, uint64, uint64, []byte, []byte) ([]byte, error)
}

// BundleStore verifies signed artifacts before atomically replacing tenant policy.
type BundleStore struct {
	mu          sync.RWMutex
	keys        map[string]ed25519.PublicKey
	bundles     map[string]*authorizationv1.AuthorizationPolicyBundle
	hashes      map[string][]byte
	maxBytes    int
	maxGrants   int
	persistence BundlePersistence
}

// SetPersistence must be called during startup before Restore or Install.
func (s *BundleStore) SetPersistence(persistence BundlePersistence) {
	s.persistence = persistence
}

// Restore verifies every persisted signed bundle before making it visible to evaluators.
func (s *BundleStore) Restore(ctx context.Context) error {
	if s.persistence == nil {
		return nil
	}
	return s.persistence.Load(ctx, func(raw []byte) error {
		bundle, err := s.verify(raw)
		if err != nil {
			return err
		}
		return s.installVerified(bundle)
	})
}

func (s *BundleStore) Ready(tenantIDs []string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, tenantID := range tenantIDs {
		if s.bundles[tenantID] == nil {
			return fmt.Errorf("authorization policy bundle is not installed for tenant %s", tenantID)
		}
	}
	return nil
}

func NewBundleStore(keys map[string]ed25519.PublicKey, maxBytes, maxGrants int) (*BundleStore, error) {
	if len(keys) == 0 || maxBytes <= 0 || maxGrants <= 0 {
		return nil, ErrInvalidPolicyBundle
	}
	copyKeys := make(map[string]ed25519.PublicKey, len(keys))
	for id, key := range keys {
		if id == "" || len(key) != ed25519.PublicKeySize {
			return nil, ErrInvalidPolicyBundle
		}
		copyKeys[id] = append(ed25519.PublicKey(nil), key...)
	}
	return &BundleStore{keys: copyKeys, bundles: map[string]*authorizationv1.AuthorizationPolicyBundle{}, hashes: map[string][]byte{}, maxBytes: maxBytes, maxGrants: maxGrants}, nil
}

func (s *BundleStore) Install(raw []byte) error {
	return s.InstallContext(context.Background(), raw)
}

func (s *BundleStore) InstallContext(ctx context.Context, raw []byte) error {
	bundle, err := s.verify(raw)
	if err != nil {
		return err
	}
	s.mu.RLock()
	current := s.bundles[bundle.TenantId]
	currentHash := append([]byte(nil), s.hashes[bundle.TenantId]...)
	s.mu.RUnlock()
	if current != nil {
		if bundle.BundleSequence < current.BundleSequence {
			return nil
		}
		if bundle.BundleSequence == current.BundleSequence {
			if bytes.Equal(currentHash, bundle.ContentHash) {
				return nil
			}
			return ErrInvalidPolicyBundle
		}
	}
	if s.persistence != nil {
		latestRaw, err := s.persistence.Persist(ctx, bundle.TenantId, bundle.BundleSequence, bundle.CatalogVersion, bundle.RevokeEpoch, bundle.ContentHash, raw)
		if err != nil {
			return err
		}
		latest, err := s.verify(latestRaw)
		if err != nil || latest.TenantId != bundle.TenantId || latest.BundleSequence < bundle.BundleSequence {
			return ErrInvalidPolicyBundle
		}
		if latest.BundleSequence == bundle.BundleSequence && !bytes.Equal(latest.ContentHash, bundle.ContentHash) {
			return ErrInvalidPolicyBundle
		}
		bundle = latest
	}
	return s.installVerified(bundle)
}

func (s *BundleStore) verify(raw []byte) (*authorizationv1.AuthorizationPolicyBundle, error) {
	if len(raw) == 0 || len(raw) > s.maxBytes {
		return nil, ErrInvalidPolicyBundle
	}
	bundle := &authorizationv1.AuthorizationPolicyBundle{}
	if (&proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, bundle) != nil || bundle.SchemaVersion != 1 || bundle.TenantId == "" || bundle.BundleSequence == 0 || bundle.PolicyVersion == "" || bundle.CatalogVersion == 0 || bundle.ValidFromEpochMs == 0 || bundle.ValidFromEpochMs > math.MaxInt64 || bundle.ExpiresAtEpochMs > math.MaxInt64 || bundle.ExpiresAtEpochMs <= bundle.ValidFromEpochMs || len(bundle.Signature) != ed25519.SignatureSize || len(bundle.ContentHash) != sha256.Size || len(bundle.ResourceGrants)+len(bundle.Grants) > s.maxGrants {
		return nil, ErrInvalidPolicyBundle
	}
	full, err := (proto.MarshalOptions{Deterministic: true}).Marshal(bundle)
	if err != nil || !bytes.Equal(full, raw) {
		return nil, ErrInvalidPolicyBundle
	}
	unsigned := proto.Clone(bundle).(*authorizationv1.AuthorizationPolicyBundle)
	unsigned.ContentHash, unsigned.Signature = nil, nil
	canonical, err := (proto.MarshalOptions{Deterministic: true}).Marshal(unsigned)
	if err != nil {
		return nil, ErrInvalidPolicyBundle
	}
	hash := sha256.Sum256(canonical)
	key := s.keys[bundle.SigningKeyId]
	if !bytes.Equal(hash[:], bundle.ContentHash) || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, hash[:], bundle.Signature) {
		return nil, ErrInvalidPolicyBundle
	}
	if !canonicalLists(bundle) {
		return nil, ErrInvalidPolicyBundle
	}
	if !validResourceBindings(bundle) {
		return nil, ErrInvalidPolicyBundle
	}
	return bundle, nil
}

func (s *BundleStore) installVerified(bundle *authorizationv1.AuthorizationPolicyBundle) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.bundles[bundle.TenantId]; current != nil {
		if bundle.BundleSequence < current.BundleSequence {
			return nil
		}
		if bundle.BundleSequence == current.BundleSequence {
			if bytes.Equal(s.hashes[bundle.TenantId], bundle.ContentHash) {
				return nil
			}
			return ErrInvalidPolicyBundle
		}
		if bundle.RevokeEpoch < current.RevokeEpoch {
			return ErrInvalidPolicyBundle
		}
	}
	s.bundles[bundle.TenantId] = bundle
	s.hashes[bundle.TenantId] = append([]byte(nil), bundle.ContentHash...)
	return nil
}

type publicationClient interface {
	PollRecords(context.Context, int) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
}

type acknowledgementProducer interface {
	ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
}

func RunBundleConsumer(ctx context.Context, client publicationClient, producer acknowledgementProducer, store *BundleStore, config kafkaconfig.Consumer, acknowledgementTopic, consumerID, consumerType string) error {
	if client == nil || producer == nil || store == nil || config.Validate() != nil || acknowledgementTopic == "" || consumerID == "" || consumerType == "" {
		return ErrInvalidPolicyBundle
	}
	for ctx.Err() == nil {
		fetches := client.PollRecords(ctx, min(config.BatchSize, 100))
		if faults := fetches.Errors(); len(faults) > 0 {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("authorization bundle Kafka fetch: %v", faults[0])
		}
		for _, record := range fetches.Records() {
			if record.Topic != config.Topic || len(record.Value) == 0 || len(record.Value) > config.MaxMessageBytes {
				return ErrInvalidPolicyBundle
			}
			publication := &authorizationv1.AuthorizationPolicyBundlePublication{}
			if (&proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(record.Value, publication) != nil || publication.EventId == "" || publication.TenantId == "" || publication.BundleSequence == 0 || publication.CatalogVersion == 0 || publication.PublishedAtEpochMs == 0 || len(publication.BundleBytes) == 0 || !bytes.Equal(record.Key, []byte(publication.TenantId)) {
				return ErrInvalidPolicyBundle
			}
			canonicalPublication, marshalErr := (proto.MarshalOptions{Deterministic: true}).Marshal(publication)
			if marshalErr != nil || !bytes.Equal(canonicalPublication, record.Value) {
				return ErrInvalidPolicyBundle
			}
			bundle := &authorizationv1.AuthorizationPolicyBundle{}
			if proto.Unmarshal(publication.BundleBytes, bundle) != nil || bundle.TenantId != publication.TenantId || bundle.BundleSequence != publication.BundleSequence || bundle.CatalogVersion != publication.CatalogVersion {
				return ErrInvalidPolicyBundle
			}
			if err := store.InstallContext(ctx, publication.BundleBytes); err != nil {
				return err
			}
			acknowledgement := &authorizationv1.AuthorizationPolicyBundleAcknowledgement{
				SchemaVersion: 1, AcknowledgementId: fmt.Sprintf("%s/%s/%d", consumerID, publication.TenantId, publication.BundleSequence),
				ConsumerId: consumerID, ConsumerType: consumerType, TenantId: publication.TenantId,
				BundleSequence: publication.BundleSequence, CatalogVersion: publication.CatalogVersion,
				BundleContentHash: append([]byte(nil), bundle.ContentHash...), AcknowledgedAtEpochMs: uint64(time.Now().UnixMilli()),
			}
			ackBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(acknowledgement)
			if err != nil {
				return err
			}
			if err := producer.ProduceSync(ctx, &kgo.Record{Topic: acknowledgementTopic, Key: []byte(publication.TenantId), Value: ackBytes}).FirstErr(); err != nil {
				return fmt.Errorf("publish authorization bundle acknowledgement: %w", err)
			}
			if err := client.CommitRecords(ctx, record); err != nil {
				return err
			}
		}
	}
	return nil
}

func canonicalLists(bundle *authorizationv1.AuthorizationPolicyBundle) bool {
	for _, grant := range bundle.Grants {
		for _, values := range [][]string{grant.ActorIds, grant.Roles, grant.RequiredCapabilities} {
			if !sort.StringsAreSorted(values) || hasDuplicate(values) {
				return false
			}
		}
	}
	grantIDs := make([]string, 0, len(bundle.Grants))
	for _, grant := range bundle.Grants {
		grantIDs = append(grantIDs, grant.GrantId)
	}
	if !sort.StringsAreSorted(grantIDs) || hasDuplicate(grantIDs) {
		return false
	}
	for _, grant := range bundle.ResourceGrants {
		for _, values := range [][]string{grant.ActorIds, grant.Roles, grant.RequiredCapabilities, grant.BranchScopeIds, grant.QueueScopeIds, grant.ResourceScopeIds, grant.ReadableFields, grant.WritableFields} {
			if !sort.StringsAreSorted(values) || hasDuplicate(values) {
				return false
			}
		}
	}
	ids := make([]string, 0, len(bundle.ResourceGrants))
	for _, grant := range bundle.ResourceGrants {
		ids = append(ids, grant.GrantId)
	}
	if !sort.StringsAreSorted(ids) || hasDuplicate(ids) {
		return false
	}
	for _, definition := range bundle.ResourceCatalog {
		for _, values := range [][]string{definition.SupportedScopeTypes, definition.ReadableFields, definition.WritableFields} {
			if !sort.StringsAreSorted(values) || hasDuplicate(values) {
				return false
			}
		}
	}
	for index := 1; index < len(bundle.ResourceCatalog); index++ {
		previous, current := bundle.ResourceCatalog[index-1], bundle.ResourceCatalog[index]
		if !resourceDefinitionLess(previous, current) {
			return false
		}
	}
	for index := 1; index < len(bundle.ActorRevokeEpochs); index++ {
		if bundle.ActorRevokeEpochs[index-1].ActorId >= bundle.ActorRevokeEpochs[index].ActorId {
			return false
		}
	}
	return true
}

func resourceDefinitionLess(left, right *authorizationv1.AuthorizationResourceDefinition) bool {
	leftValues := []string{left.OwnerId, left.ResourceType, left.Action, left.Capability}
	rightValues := []string{right.OwnerId, right.ResourceType, right.Action, right.Capability}
	for index, value := range leftValues {
		if comparison := strings.Compare(value, rightValues[index]); comparison != 0 {
			return comparison < 0
		}
	}
	return false
}

func validResourceBindings(bundle *authorizationv1.AuthorizationPolicyBundle) bool {
	allIDs := map[string]bool{}
	for _, grant := range bundle.Grants {
		if grant.GrantId == "" || allIDs[grant.GrantId] {
			return false
		}
		allIDs[grant.GrantId] = true
	}
	for _, grant := range bundle.ResourceGrants {
		if grant.GrantId == "" || allIDs[grant.GrantId] || grant.OwnerId == "" || grant.ResourceType == "" || grant.Action == "" || (len(grant.ActorIds) == 0 && len(grant.Roles) == 0) || (grant.Effect != authorizationv1.AuthorizationPolicyEffect_AUTHORIZATION_POLICY_EFFECT_ALLOW && grant.Effect != authorizationv1.AuthorizationPolicyEffect_AUTHORIZATION_POLICY_EFFECT_DENY) {
			return false
		}
		allIDs[grant.GrantId] = true
		narrowerScope := len(grant.BranchScopeIds) > 0 || len(grant.QueueScopeIds) > 0 || grant.OwnScope || len(grant.ResourceScopeIds) > 0
		if grant.TenantScope == narrowerScope {
			return false
		}
		for _, set := range [][]string{grant.ActorIds, grant.Roles, grant.RequiredCapabilities, grant.BranchScopeIds, grant.QueueScopeIds, grant.ResourceScopeIds, grant.ReadableFields, grant.WritableFields} {
			for _, value := range set {
				if value == "" {
					return false
				}
			}
		}
		bound := false
		for _, definition := range bundle.ResourceCatalog {
			if definition.OwnerId != grant.OwnerId || definition.ResourceType != grant.ResourceType || definition.Action != grant.Action {
				continue
			}
			for _, capability := range grant.RequiredCapabilities {
				if capability != definition.Capability {
					continue
				}
				scopes := stringSet(definition.SupportedScopeTypes)
				if (len(grant.BranchScopeIds) > 0 && !scopes["branch"]) || (len(grant.QueueScopeIds) > 0 && !scopes["queue"]) || (grant.OwnScope && !scopes["own"]) || (len(grant.ResourceScopeIds) > 0 && !scopes["resource"]) {
					continue
				}
				bound = true
			}
		}
		if !bound {
			return false
		}
	}
	for _, definition := range bundle.ResourceCatalog {
		if definition.OwnerId == "" || definition.ManifestVersion == "" || len(definition.ManifestDigest) != 32 || definition.ResourceType == "" || definition.Action == "" || definition.Capability == "" || len(definition.SupportedScopeTypes) == 0 {
			return false
		}
		for _, set := range [][]string{definition.SupportedScopeTypes, definition.ReadableFields, definition.WritableFields} {
			for _, value := range set {
				if value == "" {
					return false
				}
			}
		}
	}
	return true
}

func hasDuplicate(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i-1] == values[i] {
			return true
		}
	}
	return false
}

func (s *BundleStore) Evaluate(ctx context.Context, request application.QueryAuthorizationRequest, subject string, roles, capabilities []string, revokeEpoch uint64, ownerID string, now time.Time, branchScopes BranchScopeResolver) (application.QueryAuthorization, error) {
	s.mu.RLock()
	bundle := s.bundles[request.TenantID]
	s.mu.RUnlock()
	if bundle == nil || now.UnixMilli() < int64(bundle.ValidFromEpochMs) || now.UnixMilli() >= int64(bundle.ExpiresAtEpochMs) {
		return application.QueryAuthorization{}, application.ErrQueryForbidden
	}
	if revokeEpoch < bundle.RevokeEpoch {
		return application.QueryAuthorization{}, application.ErrQueryForbidden
	}
	for _, revoked := range bundle.ActorRevokeEpochs {
		if revoked.ActorId == subject && revokeEpoch < revoked.RevokeEpoch {
			return application.QueryAuthorization{}, application.ErrQueryForbidden
		}
	}
	resourceType, action := request.Policy.QueryPDPResourceType, request.Policy.QueryPDPAction
	definitions := make([]*authorizationv1.AuthorizationResourceDefinition, 0)
	capSet := stringSet(capabilities)
	for _, def := range bundle.ResourceCatalog {
		if def.OwnerId == ownerID && selector(def.ResourceType, resourceType) && selector(def.Action, action) && capSet[def.Capability] {
			definitions = append(definitions, def)
		}
	}
	if len(definitions) == 0 {
		return application.QueryAuthorization{}, application.ErrQueryForbidden
	}
	roleSet, actorCapabilities := stringSet(roles), stringSet(capabilities)
	defaultFields := stringSet(intersectReadableFields(definitions))
	var allows, denies []*authorizationv1.AuthorizationResourceGrant
	for _, grant := range bundle.ResourceGrants {
		if grant.OwnerId != ownerID || !selector(grant.ResourceType, resourceType) || !selector(grant.Action, action) || !subjectMatches(grant.ActorIds, grant.Roles, subject, roleSet) || !allCapabilities(grant.RequiredCapabilities, actorCapabilities) {
			continue
		}
		if len(grant.QueueScopeIds) != 0 {
			return application.QueryAuthorization{}, application.ErrQueryForbidden
		}
		switch grant.Effect {
		case authorizationv1.AuthorizationPolicyEffect_AUTHORIZATION_POLICY_EFFECT_ALLOW:
			allows = append(allows, grant)
		case authorizationv1.AuthorizationPolicyEffect_AUTHORIZATION_POLICY_EFFECT_DENY:
			denies = append(denies, grant)
		default:
			return application.QueryAuthorization{}, application.ErrQueryForbidden
		}
	}
	if len(allows) == 0 {
		return application.QueryAuthorization{}, application.ErrQueryForbidden
	}
	branchRoots := make([]string, 0)
	branchRootSet := make(map[string]struct{})
	for _, grant := range append(append([]*authorizationv1.AuthorizationResourceGrant(nil), allows...), denies...) {
		for _, branchID := range grant.BranchScopeIds {
			if _, exists := branchRootSet[branchID]; !exists {
				branchRootSet[branchID] = struct{}{}
				branchRoots = append(branchRoots, branchID)
			}
		}
	}
	resolvedBranches := map[string][]string{}
	if len(branchRoots) > 0 {
		if branchScopes == nil {
			return application.QueryAuthorization{}, application.ErrQueryForbidden
		}
		var err error
		resolvedBranches, err = branchScopes.ResolveDescendants(ctx, request.TenantID, branchRoots)
		if err != nil {
			return application.QueryAuthorization{}, application.ErrQueryForbidden
		}
	}
	filterGroups := make([]application.ProcessExpression, 0, len(allows))
	allowAll := false
	readable := map[string]bool{}
	for _, grant := range allows {
		children := make([]application.ProcessExpression, 0, 2)
		if len(grant.BranchScopeIds) > 0 {
			branch, ok := branchFilter(grant.BranchScopeIds, grant.BranchIncludesDescendants, resolvedBranches)
			if !ok {
				return application.QueryAuthorization{}, application.ErrQueryForbidden
			}
			children = append(children, branch)
		}
		if grant.OwnScope {
			children = append(children, ownFilter(subject))
		}
		if len(grant.ResourceScopeIds) > 0 {
			children = append(children, resourceFilter(grant.ResourceScopeIds))
		}
		if len(children) == 1 {
			filterGroups = append(filterGroups, children[0])
		} else if len(children) > 1 {
			filterGroups = append(filterGroups, application.ProcessExpression{Logical: application.LogicalAnd, Children: children})
		} else {
			allowAll = true
		}
		fields := grant.ReadableFields
		if len(fields) == 0 {
			fields = intersectReadableFields(definitions)
		}
		for _, field := range fields {
			if !defaultFields[field] {
				return application.QueryAuthorization{}, application.ErrQueryForbidden
			}
			readable[field] = true
		}
	}
	denyGroups := make([]application.ProcessExpression, 0, len(denies))
	for _, grant := range denies {
		children := make([]application.ProcessExpression, 0, 2)
		if len(grant.BranchScopeIds) > 0 {
			branch, ok := branchFilter(grant.BranchScopeIds, grant.BranchIncludesDescendants, resolvedBranches)
			if !ok {
				return application.QueryAuthorization{}, application.ErrQueryForbidden
			}
			children = append(children, branch)
		}
		if grant.OwnScope {
			children = append(children, ownFilter(subject))
		}
		if len(grant.ResourceScopeIds) > 0 {
			children = append(children, resourceFilter(grant.ResourceScopeIds))
		}
		if len(children) == 0 {
			return application.QueryAuthorization{}, application.ErrQueryForbidden
		}
		if len(children) == 1 {
			denyGroups = append(denyGroups, children[0])
		} else {
			denyGroups = append(denyGroups, application.ProcessExpression{Logical: application.LogicalAnd, Children: children})
		}
	}
	var filter *application.ProcessExpression
	if !allowAll && len(filterGroups) == 1 {
		filter = &filterGroups[0]
	} else if !allowAll && len(filterGroups) > 1 {
		filter = &application.ProcessExpression{Logical: application.LogicalOr, Children: filterGroups}
	}
	if len(denyGroups) == 1 {
		denied := denyGroups[0]
		exclusion := application.ProcessExpression{Negated: &denied}
		filter = intersectPolicyFilters(filter, exclusion)
	}
	if len(denyGroups) > 1 {
		denied := application.ProcessExpression{Logical: application.LogicalOr, Children: denyGroups}
		exclusion := application.ProcessExpression{Negated: &denied}
		filter = intersectPolicyFilters(filter, exclusion)
	}
	fields := &application.FieldAuthorization{ReadFields: []string{}, QueryFields: []string{}}
	for _, field := range application.FieldNames {
		if readable[field] {
			fields.ReadFields = append(fields.ReadFields, field)
			fields.QueryFields = append(fields.QueryFields, field)
		}
	}
	if len(fields.ReadFields) == 0 {
		return application.QueryAuthorization{}, application.ErrQueryForbidden
	}
	return application.QueryAuthorization{SubjectID: subject, PolicyVersion: fmt.Sprintf("%s/%d", bundle.PolicyVersion, bundle.BundleSequence), Filter: filter, Fields: fields, Capabilities: append([]string(nil), capabilities...)}, nil
}

func branchFilter(branchIDs []string, descendants bool, current map[string][]string) (application.ProcessExpression, bool) {
	allowed := make([]string, 0)
	for _, branchID := range branchIDs {
		covered, exists := current[branchID]
		if !exists || len(covered) == 0 {
			return application.ProcessExpression{}, false
		}
		if descendants {
			allowed = append(allowed, covered...)
		} else {
			allowed = append(allowed, branchID)
		}
	}
	if len(allowed) == 0 || len(allowed) > 500 {
		return application.ProcessExpression{}, false
	}
	values := make([]application.QueryValue, 0, len(allowed))
	for _, branchID := range allowed {
		values = append(values, application.QueryValue{Kind: application.ValueString, String: branchID})
	}
	branchMatch := application.ProcessExpression{Predicate: &application.ProcessPredicate{
		Field:    application.ProcessResourceBranchID,
		Operator: application.CompareIn,
		Values:   values,
	}}
	validCanonicalScope := application.ProcessExpression{Predicate: &application.ProcessPredicate{
		Field:    application.ProcessOrganizationVersion,
		Operator: application.CompareGreaterThan,
		Values:   []application.QueryValue{{Kind: application.ValueInteger, Integer: 0}},
	}}
	return application.ProcessExpression{Logical: application.LogicalAnd, Children: []application.ProcessExpression{branchMatch, validCanonicalScope}}, true
}

func intersectReadableFields(definitions []*authorizationv1.AuthorizationResourceDefinition) []string {
	if len(definitions) == 0 {
		return nil
	}
	intersection := stringSet(definitions[0].ReadableFields)
	for _, definition := range definitions[1:] {
		current := stringSet(definition.ReadableFields)
		for field := range intersection {
			if !current[field] {
				delete(intersection, field)
			}
		}
	}
	fields := make([]string, 0, len(intersection))
	for field := range intersection {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func intersectPolicyFilters(left *application.ProcessExpression, right application.ProcessExpression) *application.ProcessExpression {
	if left == nil {
		return &right
	}
	return &application.ProcessExpression{Logical: application.LogicalAnd, Children: []application.ProcessExpression{*left, right}}
}

func selector(rule, value string) bool { return rule == "*" || rule == value }
func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}
func subjectMatches(actors, roles []string, subject string, actorRoles map[string]bool) bool {
	for _, actor := range actors {
		if actor == subject {
			return true
		}
	}
	for _, role := range roles {
		if actorRoles[role] {
			return true
		}
	}
	return false
}
func allCapabilities(required []string, actual map[string]bool) bool {
	for _, value := range required {
		if !actual[value] {
			return false
		}
	}
	return true
}
func ownFilter(subject string) application.ProcessExpression {
	return application.ProcessExpression{Predicate: &application.ProcessPredicate{Field: application.ProcessInitiatorActorID, Operator: application.CompareEqual, Values: []application.QueryValue{{Kind: application.ValueString, String: subject}}}}
}
func resourceFilter(ids []string) application.ProcessExpression {
	values := make([]application.QueryValue, len(ids))
	for index, id := range ids {
		values[index] = application.QueryValue{Kind: application.ValueString, String: id}
	}
	return application.ProcessExpression{Predicate: &application.ProcessPredicate{Field: application.ProcessInstanceID, Operator: application.CompareIn, Values: values}}
}
