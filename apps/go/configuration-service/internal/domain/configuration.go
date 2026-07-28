package domain

import (
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type ScopeType string
type VersionStatus string
type Owner string

const (
	ScopePlatform                 ScopeType = "PLATFORM"
	ScopeEnvironment              ScopeType = "ENVIRONMENT"
	ScopeTenant                   ScopeType = "TENANT"
	ScopeWorkflowType             ScopeType = "WORKFLOW_TYPE"
	ScopeWorkflowVersion          ScopeType = "WORKFLOW_VERSION"
	ScopeApprovedInstanceOverride ScopeType = "APPROVED_INSTANCE_OVERRIDE"

	StatusDraft     VersionStatus = "DRAFT"
	StatusPublished VersionStatus = "PUBLISHED"
	StatusRetired   VersionStatus = "RETIRED"

	OwnerEngine       Owner = "ENGINE"
	OwnerAPIGateway   Owner = "API_GATEWAY"
	OwnerHumanRuntime Owner = "HUMAN_RUNTIME"
	OwnerProjection   Owner = "PROJECTION"
	OwnerGovernance   Owner = "GOVERNANCE"
)

var (
	ErrInvalid      = errors.New("configuration is invalid")
	ErrNotFound     = errors.New("configuration was not found")
	ErrConflict     = errors.New("configuration version conflict")
	ErrUnauthorized = errors.New("configuration action is unauthorized")
)

type Actor struct {
	TenantID       string
	ActorID        string
	CorrelationID  string
	Capabilities   map[string]struct{}
	CommandID      string
	IdempotencyKey string
	RequestDigest  [32]byte
}

type Scope struct {
	Type      ScopeType `json:"type"`
	Reference string    `json:"reference"`
}

type Profile struct {
	ID                        string    `json:"id"`
	TenantID                  string    `json:"tenant_id"`
	Owner                     Owner     `json:"owner"`
	Name                      string    `json:"name"`
	Scope                     Scope     `json:"scope"`
	AggregateVersion          int64     `json:"aggregate_version"`
	CurrentPublishedVersionID string    `json:"current_published_version_id,omitempty"`
	IsDeleted                 bool      `json:"is_deleted"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
	Latest                    *Version  `json:"latest,omitempty"`
}

type Version struct {
	ID            string        `json:"id"`
	ProfileID     string        `json:"profile_id"`
	TenantID      string        `json:"tenant_id"`
	Ordinal       int64         `json:"ordinal"`
	ConfigVersion string        `json:"config_version"`
	PolicyVersion string        `json:"policy_version"`
	SchemaVersion uint32        `json:"schema_version"`
	Status        VersionStatus `json:"status"`
	ValuesJSON    []byte        `json:"-"`
	ContentHash   [32]byte      `json:"-"`
	Reason        string        `json:"reason"`
	CreatedAt     time.Time     `json:"created_at"`
	CreatedBy     string        `json:"created_by"`
	PublishedAt   *time.Time    `json:"published_at,omitempty"`
	PublishedBy   string        `json:"published_by,omitempty"`
}

type ResolutionLookup struct {
	TenantID             string
	Owner                Owner
	WorkflowType         string
	WorkflowVersion      string
	PlatformReference    string
	EnvironmentReference string
	InstanceID           string
}

type ResolvedConfiguration struct {
	Profile Profile
	Version Version
}

type VersionView struct {
	ID            string        `json:"id"`
	Ordinal       int64         `json:"ordinal"`
	ConfigVersion string        `json:"config_version"`
	PolicyVersion string        `json:"policy_version"`
	SchemaVersion uint32        `json:"schema_version"`
	Status        VersionStatus `json:"status"`
	Values        any           `json:"values"`
	ContentHash   string        `json:"content_hash"`
	Reason        string        `json:"reason"`
	CreatedAt     time.Time     `json:"created_at"`
	CreatedBy     string        `json:"created_by"`
	PublishedAt   *time.Time    `json:"published_at,omitempty"`
	PublishedBy   string        `json:"published_by,omitempty"`
}

func ValidateScope(scope Scope) error {
	if strings.TrimSpace(scope.Reference) == "" {
		return ErrInvalid
	}
	switch scope.Type {
	case ScopePlatform, ScopeEnvironment, ScopeTenant, ScopeWorkflowType,
		ScopeWorkflowVersion, ScopeApprovedInstanceOverride:
		return nil
	default:
		return ErrInvalid
	}
}

type ParsedPolicy struct {
	Engine       *configurationv1.EnginePolicy
	APIGateway   *configurationv1.ApiGatewayPolicy
	HumanRuntime *configurationv1.HumanRuntimePolicy
	Projection   *configurationv1.ProjectionPolicy
	Governance   *configurationv1.GovernancePolicy
}

func ValidateOwner(owner Owner) error {
	switch owner {
	case OwnerEngine, OwnerAPIGateway, OwnerHumanRuntime, OwnerProjection, OwnerGovernance:
		return nil
	default:
		return ErrInvalid
	}
}

func ParsePolicy(owner Owner, raw []byte) (ParsedPolicy, []byte, [32]byte, error) {
	var policy proto.Message
	parsed := ParsedPolicy{}
	switch owner {
	case OwnerEngine:
		parsed.Engine = &configurationv1.EnginePolicy{}
		policy = parsed.Engine
	case OwnerAPIGateway:
		parsed.APIGateway = &configurationv1.ApiGatewayPolicy{}
		policy = parsed.APIGateway
	case OwnerHumanRuntime:
		parsed.HumanRuntime = &configurationv1.HumanRuntimePolicy{}
		policy = parsed.HumanRuntime
	case OwnerProjection:
		parsed.Projection = &configurationv1.ProjectionPolicy{}
		policy = parsed.Projection
	case OwnerGovernance:
		parsed.Governance = &configurationv1.GovernancePolicy{}
		policy = parsed.Governance
	default:
		return ParsedPolicy{}, nil, [32]byte{}, ErrInvalid
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, policy); err != nil {
		return ParsedPolicy{}, nil, [32]byte{}, ErrInvalid
	}
	if err := ValidateParsedPolicy(parsed); err != nil {
		return ParsedPolicy{}, nil, [32]byte{}, err
	}
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(policy)
	if err != nil {
		return ParsedPolicy{}, nil, [32]byte{}, err
	}
	canonical, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(policy)
	if err != nil {
		return ParsedPolicy{}, nil, [32]byte{}, err
	}
	return parsed, canonical, sha256.Sum256(wire), nil
}

func ValidateParsedPolicy(policy ParsedPolicy) error {
	switch {
	case policy.Engine != nil:
		return ValidateEnginePolicy(policy.Engine)
	case policy.APIGateway != nil:
		return validateAPIGatewayPolicy(policy.APIGateway)
	case policy.HumanRuntime != nil:
		return validateHumanRuntimePolicy(policy.HumanRuntime)
	case policy.Projection != nil:
		return validateProjectionPolicy(policy.Projection)
	case policy.Governance != nil:
		return validateGovernancePolicy(policy.Governance)
	default:
		return ErrInvalid
	}
}

func ValidateEnginePolicy(policy *configurationv1.EnginePolicy) error {
	retry := policy.GetOptimisticConflictRetry()
	wasm := policy.GetLocalWasm()
	boundary := policy.GetBoundaryRuntime()
	if policy.GetSnapshotIntervalEvents() == 0 ||
		policy.GetMaxEventsPerDecision() == 0 ||
		policy.GetCommandTimeoutMs() == 0 ||
		retry.GetMaxAttempts() == 0 ||
		retry.GetInitialBackoffMs() == 0 ||
		retry.GetMaxBackoffMs() < retry.GetInitialBackoffMs() ||
		retry.GetMultiplierMillis() < 1000 ||
		strings.TrimSpace(policy.GetEventPayloadKeyScope()) == "" ||
		strings.TrimSpace(policy.GetAuthorizationAuditKeyScope()) == "" ||
		policy.GetMaxMultiInstanceCardinality() == 0 ||
		policy.GetDefaultMultiInstanceParallelism() == 0 ||
		policy.GetDefaultMultiInstanceParallelism() > policy.GetMaxMultiInstanceCardinality() ||
		wasm.GetMaxModuleBytes() == 0 ||
		wasm.GetMaxInputBytes() == 0 ||
		wasm.GetMaxOutputBytes() == 0 ||
		wasm.GetMaxMemoryBytes() == 0 ||
		wasm.GetFuel() == 0 ||
		boundary.GetProjectionBatchSize() == 0 ||
		boundary.GetDispatchBatchSize() == 0 ||
		boundary.GetMaxDispatchAttempts() == 0 ||
		boundary.GetRetryDelayMs() == 0 ||
		boundary.GetLeaseDurationMs() == 0 ||
		boundary.GetMaxTimerHorizonMs() == 0 ||
		boundary.GetMaxExpressionBytes() == 0 ||
		strings.TrimSpace(boundary.GetWorkerId()) == "" ||
		boundary.GetMaxSignalIdBytes() == 0 ||
		boundary.GetMaxReferenceBytes() == 0 ||
		boundary.GetMaxSubscriptionsPerInstance() == 0 {
		return ErrInvalid
	}
	return nil
}

func validateAPIGatewayPolicy(policy *configurationv1.ApiGatewayPolicy) error {
	retry := policy.GetUpstreamRetry()
	if policy.GetRateLimitRequests() == 0 ||
		policy.GetRateLimitWindowMs() == 0 ||
		policy.GetUpstreamTimeoutMs() == 0 ||
		policy.GetCircuitBreakerFailureThreshold() == 0 ||
		policy.GetCircuitBreakerOpenMs() == 0 ||
		policy.GetBulkheadMaxConcurrency() == 0 ||
		policy.GetMaxRequestBodyBytes() == 0 ||
		policy.GetMaxUpstreamResponseBytes() == 0 ||
		policy.GetBatchChunkSize() == 0 ||
		policy.GetBatchConcurrency() == 0 ||
		policy.GetBatchConcurrency() > policy.GetBatchChunkSize() ||
		retry.GetMaxAttempts() == 0 ||
		retry.GetInitialBackoffMs() == 0 ||
		retry.GetMaxBackoffMs() < retry.GetInitialBackoffMs() ||
		retry.GetMultiplierMillis() < 1000 ||
		strings.TrimSpace(policy.GetEncryptionKeyScope()) == "" {
		return ErrInvalid
	}
	return nil
}

func validateHumanRuntimePolicy(policy *configurationv1.HumanRuntimePolicy) error {
	retry := policy.GetEngineRetry()
	if policy.GetProjectionBatchSize() == 0 ||
		policy.GetEscalationBatchSize() == 0 ||
		policy.GetEscalationLeaseMs() == 0 ||
		policy.GetEscalationRetryMs() == 0 ||
		policy.GetEscalationPollMs() == 0 ||
		policy.GetEngineCommandTimeoutMs() == 0 ||
		policy.GetMaxAssignmentCandidates() == 0 ||
		policy.GetMaxDelegationDepth() == 0 ||
		policy.GetQueryDefaultPageSize() == 0 ||
		policy.GetQueryMaxPageSize() < policy.GetQueryDefaultPageSize() ||
		retry.GetMaxAttempts() == 0 ||
		retry.GetInitialBackoffMs() == 0 ||
		retry.GetMaxBackoffMs() < retry.GetInitialBackoffMs() ||
		retry.GetMultiplierMillis() < 1000 ||
		policy.GetEngineCircuitBreakerFailureThreshold() == 0 ||
		policy.GetEngineCircuitBreakerOpenMs() == 0 ||
		len(policy.GetEngineRetryableCodes()) == 0 {
		return ErrInvalid
	}
	for _, code := range policy.GetEngineRetryableCodes() {
		switch code {
		case "UNAVAILABLE", "RESOURCE_EXHAUSTED", "DEADLINE_EXCEEDED", "ABORTED":
		default:
			return ErrInvalid
		}
	}
	return nil
}

func validateProjectionPolicy(policy *configurationv1.ProjectionPolicy) error {
	if policy.GetConsumeBatchSize() == 0 ||
		policy.GetRebuildBatchSize() == 0 ||
		policy.GetQueryDefaultPageSize() == 0 ||
		policy.GetQueryMaxPageSize() < policy.GetQueryDefaultPageSize() ||
		policy.GetRealtimePublishBatchSize() == 0 ||
		policy.GetCheckpointFlushMs() == 0 ||
		policy.GetMaxProjectionLagMs() == 0 {
		return ErrInvalid
	}
	return nil
}

func validateGovernancePolicy(policy *configurationv1.GovernancePolicy) error {
	retry := policy.GetKmsRetry()
	keyIDs := make(map[string]struct{}, len(policy.GetApprovalKeys()))
	enabledKeys := 0
	for _, key := range policy.GetApprovalKeys() {
		if key.GetKeyId() == "" || len(key.GetEd25519PublicKey()) != 32 {
			return ErrInvalid
		}
		if _, exists := keyIDs[key.GetKeyId()]; exists {
			return ErrInvalid
		}
		keyIDs[key.GetKeyId()] = struct{}{}
		if key.GetEnabled() {
			enabledKeys++
		}
	}
	if policy.GetApprovalTtlMs() == 0 ||
		policy.GetFreshAuthenticationMaxAgeMs() == 0 ||
		policy.GetKmsRequestTimeoutMs() == 0 ||
		retry.GetMaxAttempts() == 0 ||
		retry.GetInitialBackoffMs() == 0 ||
		retry.GetMaxBackoffMs() < retry.GetInitialBackoffMs() ||
		retry.GetMultiplierMillis() < 1000 ||
		policy.GetKeyCacheTtlMs() == 0 ||
		policy.GetRevocationBarrierTimeoutMs() == 0 ||
		policy.GetReconciliationBatchSize() == 0 ||
		policy.GetMaxPendingCompensations() == 0 ||
		policy.GetAbortCapability() == "" ||
		len(policy.GetAcceptedAuthAssurance()) == 0 ||
		len(policy.GetApprovalKeys()) == 0 ||
		enabledKeys == 0 ||
		policy.GetRequiredApproverCount() == 0 {
		return ErrInvalid
	}
	for _, assurance := range policy.GetAcceptedAuthAssurance() {
		if assurance == "" {
			return ErrInvalid
		}
	}
	return nil
}
