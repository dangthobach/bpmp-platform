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
	mu        sync.RWMutex
	owner     configurationv1.ConfigurationOwner
	tenants   map[string]*configurationv1.ResolvedConfigurationSnapshot
	instances map[string]map[string]*configurationv1.ResolvedConfigurationSnapshot
}

func NewCache(owner configurationv1.ConfigurationOwner) (*Cache, error) {
	if owner == configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_UNSPECIFIED {
		return nil, ErrInvalidSnapshot
	}
	return &Cache{
		owner:     owner,
		tenants:   make(map[string]*configurationv1.ResolvedConfigurationSnapshot),
		instances: make(map[string]map[string]*configurationv1.ResolvedConfigurationSnapshot),
	}, nil
}

func (c *Cache) InstallInstance(
	tenantID string,
	instanceID string,
	snapshot *configurationv1.ResolvedConfigurationSnapshot,
) error {
	if tenantID == "" || instanceID == "" || !validSnapshot(c.owner, snapshot) ||
		!hasScope(snapshot, configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_APPROVED_INSTANCE_OVERRIDE, instanceID) {
		return ErrInvalidSnapshot
	}
	cloned := proto.Clone(snapshot).(*configurationv1.ResolvedConfigurationSnapshot)
	c.mu.Lock()
	defer c.mu.Unlock()
	tenantInstances := c.instances[tenantID]
	if tenantInstances == nil {
		tenantInstances = make(map[string]*configurationv1.ResolvedConfigurationSnapshot)
		c.instances[tenantID] = tenantInstances
	}
	current := tenantInstances[instanceID]
	if current != nil && cloned.GetOrdinal() < current.GetOrdinal() {
		return ErrStaleSnapshot
	}
	tenantInstances[instanceID] = cloned
	return nil
}

func (c *Cache) Install(tenantID string, snapshot *configurationv1.ResolvedConfigurationSnapshot) error {
	if tenantID == "" || !validSnapshot(c.owner, snapshot) {
		return ErrInvalidSnapshot
	}
	cloned := proto.Clone(snapshot).(*configurationv1.ResolvedConfigurationSnapshot)
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.tenants[tenantID]
	if current != nil && cloned.GetOrdinal() < current.GetOrdinal() {
		return ErrStaleSnapshot
	}
	c.tenants[tenantID] = cloned
	return nil
}

func (c *Cache) GetForInstance(
	tenantID string,
	instanceID string,
) (*configurationv1.ResolvedConfigurationSnapshot, error) {
	c.mu.RLock()
	var snapshot *configurationv1.ResolvedConfigurationSnapshot
	if tenantInstances := c.instances[tenantID]; tenantInstances != nil {
		snapshot = tenantInstances[instanceID]
	}
	if snapshot == nil {
		snapshot = c.tenants[tenantID]
	}
	c.mu.RUnlock()
	if snapshot == nil {
		return nil, ErrMissingSnapshot
	}
	return proto.Clone(snapshot).(*configurationv1.ResolvedConfigurationSnapshot), nil
}

func (c *Cache) Get(tenantID string) (*configurationv1.ResolvedConfigurationSnapshot, error) {
	c.mu.RLock()
	snapshot := c.tenants[tenantID]
	c.mu.RUnlock()
	if snapshot == nil {
		return nil, ErrMissingSnapshot
	}
	return proto.Clone(snapshot).(*configurationv1.ResolvedConfigurationSnapshot), nil
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
	default:
		return false
	}
}
