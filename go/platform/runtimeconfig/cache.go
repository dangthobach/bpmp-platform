package runtimeconfig

import (
	"errors"
	"sync"

	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	"google.golang.org/protobuf/proto"
)

var (
	ErrMissingSnapshot = errors.New("runtime configuration snapshot is not installed")
	ErrInvalidSnapshot = errors.New("runtime configuration snapshot is invalid")
	ErrStaleSnapshot   = errors.New("runtime configuration snapshot is stale")
)

// Cache swaps complete immutable snapshots under one short lock. A request
// clones exactly one snapshot and cannot observe a partially applied policy.
type Cache struct {
	mu            sync.RWMutex
	owner         configurationv1.ConfigurationOwner
	scoped        map[CacheKey]*configurationv1.ResolvedConfigurationSnapshot
	latestOrdinal map[CacheKey]uint64
}

type CacheKey struct {
	TenantID        string
	WorkflowType    string
	WorkflowVersion string
	InstanceID      string
}

func (key CacheKey) valid() bool {
	return key.TenantID != "" &&
		(key.WorkflowVersion == "" || key.WorkflowType != "")
}

func NewCache(owner configurationv1.ConfigurationOwner) (*Cache, error) {
	if owner == configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_UNSPECIFIED {
		return nil, ErrInvalidSnapshot
	}
	return &Cache{
		owner:         owner,
		scoped:        make(map[CacheKey]*configurationv1.ResolvedConfigurationSnapshot),
		latestOrdinal: make(map[CacheKey]uint64),
	}, nil
}

func (c *Cache) InstallInstance(
	tenantID string,
	instanceID string,
	snapshot *configurationv1.ResolvedConfigurationSnapshot,
) error {
	return c.InstallScoped(CacheKey{TenantID: tenantID, InstanceID: instanceID}, snapshot)
}

func (c *Cache) Install(tenantID string, snapshot *configurationv1.ResolvedConfigurationSnapshot) error {
	return c.InstallScoped(CacheKey{TenantID: tenantID}, snapshot)
}

func (c *Cache) InstallScoped(
	key CacheKey,
	snapshot *configurationv1.ResolvedConfigurationSnapshot,
) error {
	if !key.valid() || !validSnapshot(c.owner, snapshot) || !scopeMatchesKey(snapshot, key) {
		return ErrInvalidSnapshot
	}
	cloned := proto.Clone(snapshot).(*configurationv1.ResolvedConfigurationSnapshot)
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.scoped[key]
	latest := c.latestOrdinal[key]
	if cloned.GetOrdinal() < latest ||
		(cloned.GetOrdinal() == latest && current == nil) {
		return ErrStaleSnapshot
	}
	c.scoped[key] = cloned
	c.latestOrdinal[key] = cloned.GetOrdinal()
	return nil
}

// RetireScoped installs a tombstone before a Kafka offset is committed. Replays
// are idempotent and an older publication cannot resurrect retired policy.
func (c *Cache) RetireScoped(key CacheKey, ordinal uint64) error {
	if !key.valid() || ordinal == 0 {
		return ErrInvalidSnapshot
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ordinal < c.latestOrdinal[key] {
		return ErrStaleSnapshot
	}
	delete(c.scoped, key)
	c.latestOrdinal[key] = ordinal
	return nil
}

func (c *Cache) GetForInstance(
	tenantID string,
	instanceID string,
) (*configurationv1.ResolvedConfigurationSnapshot, error) {
	return c.GetScoped(CacheKey{TenantID: tenantID, InstanceID: instanceID})
}

func (c *Cache) Get(tenantID string) (*configurationv1.ResolvedConfigurationSnapshot, error) {
	return c.GetScoped(CacheKey{TenantID: tenantID})
}

func (c *Cache) GetScoped(
	key CacheKey,
) (*configurationv1.ResolvedConfigurationSnapshot, error) {
	if !key.valid() {
		return nil, ErrMissingSnapshot
	}
	c.mu.RLock()
	var snapshot *configurationv1.ResolvedConfigurationSnapshot
	for _, candidate := range lookupKeys(key) {
		if snapshot = c.scoped[candidate]; snapshot != nil {
			break
		}
	}
	c.mu.RUnlock()
	if snapshot == nil {
		return nil, ErrMissingSnapshot
	}
	return proto.Clone(snapshot).(*configurationv1.ResolvedConfigurationSnapshot), nil
}

func lookupKeys(key CacheKey) []CacheKey {
	keys := make([]CacheKey, 0, 4)
	if key.InstanceID != "" {
		keys = append(keys, CacheKey{TenantID: key.TenantID, InstanceID: key.InstanceID})
	}
	if key.WorkflowType != "" && key.WorkflowVersion != "" {
		keys = append(keys, CacheKey{
			TenantID: key.TenantID, WorkflowType: key.WorkflowType,
			WorkflowVersion: key.WorkflowVersion,
		})
	}
	if key.WorkflowType != "" {
		keys = append(keys, CacheKey{TenantID: key.TenantID, WorkflowType: key.WorkflowType})
	}
	return append(keys, CacheKey{TenantID: key.TenantID})
}

func scopeMatchesKey(
	snapshot *configurationv1.ResolvedConfigurationSnapshot,
	key CacheKey,
) bool {
	switch {
	case key.InstanceID != "":
		return hasScope(snapshot,
			configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_APPROVED_INSTANCE_OVERRIDE,
			key.InstanceID)
	case key.WorkflowVersion != "":
		return hasScope(snapshot,
			configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_WORKFLOW_VERSION,
			key.WorkflowType+":"+key.WorkflowVersion)
	case key.WorkflowType != "":
		return hasScope(snapshot,
			configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_WORKFLOW_TYPE,
			key.WorkflowType)
	default:
		for _, scope := range snapshot.GetResolvedScopes() {
			switch scope.GetType() {
			case configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_PLATFORM,
				configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_ENVIRONMENT,
				configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_TENANT:
				return true
			}
		}
		return false
	}
}

func hasScope(
	snapshot *configurationv1.ResolvedConfigurationSnapshot,
	scopeType configurationv1.ConfigurationScopeType,
	reference string,
) bool {
	for _, scope := range snapshot.GetResolvedScopes() {
		if scope.GetType() == scopeType && scope.GetReference() == reference {
			return true
		}
	}
	return false
}

func (c *Cache) Ready(tenantIDs []string) error {
	for _, tenantID := range tenantIDs {
		if _, err := c.Get(tenantID); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cache) TenantIDs() []string {
	c.mu.RLock()
	seen := make(map[string]struct{}, len(c.scoped))
	for key := range c.scoped {
		seen[key.TenantID] = struct{}{}
	}
	c.mu.RUnlock()
	tenantIDs := make([]string, 0, len(seen))
	for tenantID := range seen {
		tenantIDs = append(tenantIDs, tenantID)
	}
	return tenantIDs
}

func validSnapshot(
	owner configurationv1.ConfigurationOwner,
	snapshot *configurationv1.ResolvedConfigurationSnapshot,
) bool {
	if snapshot == nil ||
		snapshot.GetOwner() != owner ||
		snapshot.GetConfigId() == "" ||
		snapshot.GetConfigVersion() == "" ||
		snapshot.GetPolicyVersion() == "" ||
		snapshot.GetSchemaVersion() == 0 ||
		snapshot.GetOrdinal() == 0 ||
		len(snapshot.GetContentHash()) != 32 ||
		len(snapshot.GetResolvedScopes()) == 0 {
		return false
	}
	switch owner {
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_ENGINE:
		return snapshot.GetEngine() != nil
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY:
		return snapshot.GetApiGateway() != nil
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_HUMAN_RUNTIME:
		return snapshot.GetHumanRuntime() != nil
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_PROJECTION:
		return snapshot.GetProjection() != nil
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_GOVERNANCE:
		return snapshot.GetGovernance() != nil
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_CONFIGURATION_SERVICE:
		return snapshot.GetConfigurationService() != nil
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_COCKPIT_GATEWAY:
		return snapshot.GetCockpitGateway() != nil
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_AUTHZ_CONTROL_PLANE:
		return snapshot.GetAuthzControlPlane() != nil
	default:
		return false
	}
}
