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

func ParsePolicy(raw []byte) (*configurationv1.EnginePolicy, []byte, [32]byte, error) {
	var policy configurationv1.EnginePolicy
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, &policy); err != nil {
		return nil, nil, [32]byte{}, ErrInvalid
	}
	if err := ValidatePolicy(&policy); err != nil {
		return nil, nil, [32]byte{}, err
	}
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(&policy)
	if err != nil {
		return nil, nil, [32]byte{}, err
	}
	canonical, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(&policy)
	if err != nil {
		return nil, nil, [32]byte{}, err
	}
	return &policy, canonical, sha256.Sum256(wire), nil
}

func ValidatePolicy(policy *configurationv1.EnginePolicy) error {
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
