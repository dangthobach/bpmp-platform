package application

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalid               = errors.New("invalid notification request")
	ErrNotFound              = errors.New("notification resource not found")
	ErrConflict              = errors.New("conflicting command or resource state")
	ErrForbidden             = errors.New("caller lacks permission or namespace access")
	ErrImmutable             = errors.New("published artifact is immutable")
	ErrIncompatibleSchema    = errors.New("schema breaks backward compatibility within schema_major")
	ErrQuotaExceeded         = errors.New("application notification quota exceeded")
	ErrSecretResendForbidden = errors.New("delivery using secret variables cannot be resent")
)

type Category struct {
	TenantID               string    `json:"tenant_id"`
	ID                     string    `json:"id"`
	CategoryKey            string    `json:"category_key"`
	Name                   string    `json:"name"`
	DefaultLane            string    `json:"default_lane"`
	UserUnsubscribable     bool      `json:"user_unsubscribable"`
	AmbiguousOutcomePolicy string    `json:"ambiguous_outcome_policy"`
	RetentionClass         string    `json:"retention_class"`
	RetainRenderedCopy     bool      `json:"retain_rendered_copy"`
	Status                 string    `json:"status"`
	RowVersion             uint64    `json:"row_version"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	CreatedBy              string    `json:"created_by"`
	UpdatedBy              string    `json:"updated_by"`
}

type QuotaConfig struct {
	MaxPerMinute int `json:"max_per_minute"`
	MaxPerDay    int `json:"max_per_day"`
}

type Application struct {
	TenantID              string            `json:"tenant_id"`
	ID                    string            `json:"id"`
	AppKey                string            `json:"app_key"`
	Name                  string            `json:"name"`
	AuthMode              string            `json:"auth_mode"`
	PrincipalRef          string            `json:"principal_ref"`
	AllowedNamespaces     []string          `json:"allowed_namespaces"`
	AllowedChannels       []string          `json:"allowed_channels"`
	DefaultLane           string            `json:"default_lane"`
	QuotaConfig           QuotaConfig       `json:"quota_config"`
	CallbackURL           string            `json:"callback_url,omitempty"`
	CallbackSecretRef     string            `json:"callback_secret_ref,omitempty"`
	OwnerTeam             string            `json:"owner_team,omitempty"`
	DefaultSenderIdentity map[string]string `json:"default_sender_identity,omitempty"`
	UsageToday            int               `json:"usage_today,omitempty"`
	Status                string            `json:"status"`
	RowVersion            uint64            `json:"row_version"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
	CreatedBy             string            `json:"created_by"`
	UpdatedBy             string            `json:"updated_by"`
}

type LayoutVersion struct {
	TenantID       string    `json:"tenant_id"`
	ID             string    `json:"id"`
	LayoutID       string    `json:"layout_id"`
	Version        uint64    `json:"version"`
	Syntax         string    `json:"syntax"`
	VariableSchema []byte    `json:"variable_schema"`
	HTMLTemplate   string    `json:"html_template"`
	ContentHash    string    `json:"content_hash"`
	Status         string    `json:"status"`
	PublishedAt    time.Time `json:"published_at,omitempty"`
	RowVersion     uint64    `json:"row_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Template struct {
	TenantID      string    `json:"tenant_id"`
	ID            string    `json:"id"`
	TemplateKey   string    `json:"template_key"`
	ApplicationID string    `json:"application_id"`
	CategoryID    string    `json:"category_id"`
	Name          string    `json:"name"`
	DefaultLocale string    `json:"default_locale"`
	Tags          []string  `json:"tags"`
	Channels      []string  `json:"channels,omitempty"`
	ActiveRelease uint64    `json:"active_release,omitempty"`
	Usage24h      uint64    `json:"usage_24h,omitempty"`
	Status        string    `json:"status"`
	RowVersion    uint64    `json:"row_version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CreatedBy     string    `json:"created_by"`
	UpdatedBy     string    `json:"updated_by"`
	IsDeleted     bool      `json:"is_deleted,omitempty"`
	DeletedAt     time.Time `json:"deleted_at,omitempty"`
	DeletedBy     string    `json:"deleted_by,omitempty"`
	DeleteReason  string    `json:"delete_reason,omitempty"`
}

type TemplateVariant struct {
	TenantID           string    `json:"tenant_id"`
	ID                 string    `json:"id"`
	TemplateVersionID  string    `json:"template_version_id"`
	Channel            string    `json:"channel"`
	Locale             string    `json:"locale"`
	VariantKind        string    `json:"variant_kind"`
	SubjectTmpl        string    `json:"subject_tmpl,omitempty"`
	HTMLTmpl           string    `json:"html_tmpl,omitempty"`
	TextTmpl           string    `json:"text_tmpl,omitempty"`
	MJMLSource         string    `json:"mjml_source,omitempty"`
	ProviderTemplateID string    `json:"provider_template_id,omitempty"`
	ParamMappingJSON   []byte    `json:"param_mapping_json,omitempty"`
	VariantHash        string    `json:"variant_hash"`
	RowVersion         uint64    `json:"row_version"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type TemplateVersion struct {
	TenantID           string            `json:"tenant_id"`
	ID                 string            `json:"id"`
	TemplateID         string            `json:"template_id"`
	Version            uint64            `json:"version"`
	SchemaMajor        uint32            `json:"schema_major"`
	Syntax             string            `json:"syntax"`
	LayoutVersionID    string            `json:"layout_version_id,omitempty"`
	VariableSchemaJSON []byte            `json:"variable_schema_json"`
	ContentHash        string            `json:"content_hash"`
	SourceCommit       string            `json:"source_commit,omitempty"`
	ChangeNote         string            `json:"change_note,omitempty"`
	ApprovalRef        string            `json:"approval_ref,omitempty"`
	Status             string            `json:"status"`
	PublishedAt        time.Time         `json:"published_at,omitempty"`
	ReleaseOrdinal     uint64            `json:"release_ordinal,omitempty"`
	Variants           []TemplateVariant `json:"variants"`
	RowVersion         uint64            `json:"row_version"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	CreatedBy          string            `json:"created_by"`
	UpdatedBy          string            `json:"updated_by"`
}

type TemplateRelease struct {
	TenantID          string    `json:"tenant_id"`
	ID                string    `json:"id"`
	TemplateID        string    `json:"template_id"`
	SchemaMajor       uint32    `json:"schema_major"`
	TemplateVersionID string    `json:"template_version_id"`
	Ordinal           uint64    `json:"ordinal"`
	PreviousOrdinal   uint64    `json:"previous_ordinal,omitempty"`
	ActivateAt        time.Time `json:"activate_at"`
	ActivatedAt       time.Time `json:"activated_at,omitempty"`
	ApprovalRef       string    `json:"approval_ref,omitempty"`
	Status            string    `json:"status"`
	RowVersion        uint64    `json:"row_version"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	CreatedBy         string    `json:"created_by"`
}

type ProviderProfile struct {
	TenantID         string            `json:"tenant_id"`
	ID               string            `json:"id"`
	ProviderKey      string            `json:"provider_key"`
	Name             string            `json:"name"`
	Channel          string            `json:"channel"`
	DriverKind       string            `json:"driver_kind"`
	PresetKey        string            `json:"preset_key,omitempty"`
	ActiveOrdinal    uint64            `json:"active_ordinal"`
	OperationalState string            `json:"operational_state"`
	ActiveRevision   *ProviderRevision `json:"active_revision,omitempty"`
	RowVersion       uint64            `json:"row_version"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type ProviderRevision struct {
	TenantID       string            `json:"tenant_id"`
	ID             string            `json:"id"`
	ProviderID     string            `json:"provider_id"`
	Revision       uint64            `json:"revision"`
	ConfigJSON     []byte            `json:"config_json"`
	CredentialsRef string            `json:"credentials_ref,omitempty"`
	SenderIdentity map[string]string `json:"sender_identity"`
	ConfigHash     string            `json:"config_hash"`
	Status         string            `json:"status"`
	PublishedAt    time.Time         `json:"published_at,omitempty"`
}

type RoutingWeight struct {
	ProviderID string `json:"provider_id"`
	Weight     uint32 `json:"weight"`
}

type RoutingPolicy struct {
	TenantID      string            `json:"tenant_id"`
	ID            string            `json:"id"`
	PolicyKey     string            `json:"policy_key"`
	ApplicationID string            `json:"application_id,omitempty"`
	CategoryID    string            `json:"category_id,omitempty"`
	Channel       string            `json:"channel"`
	Tiers         [][]RoutingWeight `json:"tiers"`
	Status        string            `json:"status"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type SubjectContact struct {
	TenantID         string    `json:"tenant_id"`
	ID               string    `json:"id"`
	SubjectRef       string    `json:"subject_ref"`
	Channel          string    `json:"channel"`
	EncryptedAddress []byte    `json:"-"`
	KeyRef           string    `json:"key_ref"`
	AddressHash      string    `json:"address_hash"`
	MaskedAddress    string    `json:"masked_address"`
	Locale           string    `json:"locale,omitempty"`
	Timezone         string    `json:"timezone,omitempty"`
	Verified         bool      `json:"verified"`
	CreatedAt        time.Time `json:"created_at"`
}

type Preference struct {
	TenantID           string `json:"tenant_id"`
	ID                 string `json:"id"`
	SubjectRef         string `json:"subject_ref"`
	CategoryID         string `json:"category_id"`
	CategoryKey        string `json:"category_key"`
	Channel            string `json:"channel"`
	Enabled            bool   `json:"enabled"`
	UserUnsubscribable bool   `json:"user_unsubscribable"`
}

type Suppression struct {
	TenantID      string    `json:"tenant_id"`
	ID            string    `json:"id"`
	Channel       string    `json:"channel"`
	AddressHash   string    `json:"address_hash"`
	MaskedAddress string    `json:"masked_address"`
	Reason        string    `json:"reason"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

type NotificationRequestRecord struct {
	TenantID       string    `json:"tenant_id"`
	ID             string    `json:"id"`
	ApplicationID  string    `json:"application_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	RequestDigest  string    `json:"request_digest"`
	TemplateID     string    `json:"template_id"`
	TemplateKey    string    `json:"template_key"`
	SchemaMajor    uint32    `json:"schema_major"`
	PinnedVersion  uint64    `json:"pinned_version,omitempty"`
	CategoryID     string    `json:"category_id"`
	CategoryKey    string    `json:"category_key"`
	Lane           string    `json:"lane"`
	Priority       int       `json:"priority"`
	CorrelationID  string    `json:"correlation_id,omitempty"`
	IsTest         bool      `json:"is_test"`
	SendAt         time.Time `json:"send_at,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	CreatedBy      string    `json:"created_by"`
}

type Delivery struct {
	TenantID            string    `json:"tenant_id"`
	ID                  string    `json:"id"`
	RequestID           string    `json:"request_id"`
	ApplicationID       string    `json:"application_id"`
	TemplateID          string    `json:"template_id"`
	TemplateVersionID   string    `json:"template_version_id"`
	VariantID           string    `json:"variant_id"`
	ReleaseOrdinal      uint64    `json:"release_ordinal"`
	RecipientSubjectRef string    `json:"recipient_subject_ref,omitempty"`
	AddressHash         string    `json:"address_hash"`
	MaskedAddress       string    `json:"masked_address"`
	Channel             string    `json:"channel"`
	Lane                string    `json:"lane"`
	Status              string    `json:"status"`
	SelectedProviderID  string    `json:"selected_provider_id,omitempty"`
	ProviderMessageID   string    `json:"provider_message_id,omitempty"`
	ContentHash         string    `json:"content_hash"`
	ResendOfDeliveryID  string    `json:"resend_of_delivery_id,omitempty"`
	CorrelationID       string    `json:"correlation_id,omitempty"`
	AttemptCount        int       `json:"attempt_count"`
	NextAttemptAt       time.Time `json:"next_attempt_at,omitempty"`
	ExpiresAt           time.Time `json:"expires_at,omitempty"`
	CompletedAt         time.Time `json:"completed_at,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type DeliveryPayload struct {
	TenantID              string    `json:"tenant_id"`
	ID                    string    `json:"id"`
	DeliveryID            string    `json:"delivery_id"`
	EncryptedAddress      []byte    `json:"-"`
	EncryptedData         []byte    `json:"-"`
	EncryptedSecrets      []byte    `json:"-"`
	RenderedCopyEncrypted []byte    `json:"-"`
	KeyRef                string    `json:"key_ref"`
	SecretsErasedAt       time.Time `json:"secrets_erased_at,omitempty"`
	ExpiresAt             time.Time `json:"expires_at"`
	CreatedAt             time.Time `json:"created_at"`
}

type DeliveryAttempt struct {
	TenantID           string    `json:"tenant_id"`
	ID                 string    `json:"id"`
	DeliveryID         string    `json:"delivery_id"`
	ProviderID         string    `json:"provider_id"`
	ProviderRevisionID string    `json:"provider_revision_id"`
	AttemptNo          int       `json:"attempt_no"`
	OutcomeClass       string    `json:"outcome_class"`
	ProviderStatusCode string    `json:"provider_status_code,omitempty"`
	ProviderMessageID  string    `json:"provider_message_id,omitempty"`
	ErrorCode          string    `json:"error_code,omitempty"`
	LatencyMS          int       `json:"latency_ms"`
	StartedAt          time.Time `json:"started_at"`
	FinishedAt         time.Time `json:"finished_at"`
}

type InAppMessage struct {
	TenantID    string    `json:"tenant_id"`
	ID          string    `json:"id"`
	DeliveryID  string    `json:"delivery_id"`
	SubjectRef  string    `json:"subject_ref"`
	CategoryKey string    `json:"category_key"`
	Title       string    `json:"title"`
	BodyHTML    string    `json:"body_html"`
	ActionURL   string    `json:"action_url,omitempty"`
	ReadAt      time.Time `json:"read_at,omitempty"`
	ArchivedAt  time.Time `json:"archived_at,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
}

type AuditRecord struct {
	TenantID     string         `json:"tenant_id"`
	ID           string         `json:"id"`
	ActorRef     string         `json:"actor_ref"`
	ActorDisplay string         `json:"actor_display,omitempty"`
	ActorRole    string         `json:"actor_role,omitempty"`
	ClientIP     string         `json:"client_ip,omitempty"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceRef  string         `json:"resource_ref"`
	Severity     string         `json:"severity"`
	CommandID    string         `json:"command_id,omitempty"`
	BeforeDigest string         `json:"before_digest,omitempty"`
	AfterDigest  string         `json:"after_digest,omitempty"`
	Details      map[string]any `json:"details,omitempty"`
	OccurredAt   time.Time      `json:"occurred_at"`
}

type BreakerTransition struct {
	TenantID        string    `json:"tenant_id"`
	ID              string    `json:"id"`
	ProviderID      string    `json:"provider_id"`
	FromState       string    `json:"from_state"`
	ToState         string    `json:"to_state"`
	TriggerKind     string    `json:"trigger_kind"`
	Reason          string    `json:"reason,omitempty"`
	ActorRef        string    `json:"actor_ref,omitempty"`
	ErrorRateBps    uint32    `json:"error_rate_bps"`
	SlowCallRateBps uint32    `json:"slow_call_rate_bps"`
	OccurredAt      time.Time `json:"occurred_at"`
}

type DeliveryEvent struct {
	TenantID   string         `json:"tenant_id"`
	ID         string         `json:"id"`
	DeliveryID string         `json:"delivery_id"`
	EventType  string         `json:"event_type"`
	FromStatus string         `json:"from_status,omitempty"`
	ToStatus   string         `json:"to_status"`
	Detail     map[string]any `json:"detail,omitempty"`
	OccurredAt time.Time      `json:"occurred_at"`
}

type DeliveryTraceStep struct {
	Step       string    `json:"step"`
	Status     string    `json:"status"` // OK | WARN | ERROR
	LatencyMS  int       `json:"latency_ms"`
	Detail     string    `json:"detail"`
	OccurredAt time.Time `json:"occurred_at"`
}

type TriggerRuleRevision struct {
	TenantID           string            `json:"tenant_id"`
	ID                 string            `json:"id"`
	RuleID             string            `json:"rule_id"`
	Revision           uint64            `json:"revision"`
	VersionSelector    string            `json:"version_selector"`
	ConditionCEL       string            `json:"condition_cel,omitempty"`
	RecipientSpecJSON  []byte            `json:"recipient_spec_json"`
	DataMappingCEL     map[string]string `json:"data_mapping_cel"`
	ReferencedVars     []string          `json:"referenced_vars"`
	DelaySeconds       int               `json:"delay_seconds"`
	DedupWindowSeconds int               `json:"dedup_window_seconds"`
	Digest             string            `json:"digest"`
	Status             string            `json:"status"`
	PublishedAt        time.Time         `json:"published_at,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	CreatedBy          string            `json:"created_by"`
}

type TriggerRule struct {
	TenantID       string               `json:"tenant_id"`
	ID             string               `json:"id"`
	RuleKey        string               `json:"rule_key"`
	Name           string               `json:"name"`
	WorkflowType   string               `json:"workflow_type"`
	NodeID         string               `json:"node_id,omitempty"`
	TriggerKind    string               `json:"trigger_kind"`
	TemplateID     string               `json:"template_id"`
	SchemaMajor    uint32               `json:"schema_major"`
	ActiveOrdinal  uint64               `json:"active_ordinal"`
	Status         string               `json:"status"`
	ActiveRevision *TriggerRuleRevision `json:"active_revision,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
	CreatedBy      string               `json:"created_by"`
}

type AuditFilter struct {
	TenantID     string
	ResourceType string
	Severity     string
	Query        string
	Limit        int
}

type TemplateListFilter struct {
	TenantID      string
	ApplicationID string
	Channel       string
	Tag           string
	Status        string
	Query         string
	PageSize      int
	PageToken     string
}

type CommandReceipt struct {
	TenantID     string    `json:"tenant_id"`
	ID           string    `json:"id"`
	CommandID    string    `json:"command_id"`
	IntentDigest string    `json:"intent_digest"`
	ActorRef     string    `json:"actor_ref"`
	AggregateID  string    `json:"aggregate_id"`
	Status       string    `json:"status"`
	ResultJSON   []byte    `json:"result_json"`
	CreatedAt    time.Time `json:"created_at"`
}

type OutboxMessage struct {
	TenantID         string    `json:"tenant_id"`
	ID               string    `json:"id"`
	EventID          string    `json:"event_id"`
	AggregateID      string    `json:"aggregate_id"`
	AggregateVersion uint64    `json:"aggregate_version"`
	EventType        string    `json:"event_type"`
	PayloadJSON      []byte    `json:"payload_json"`
	PublishedAt      time.Time `json:"published_at,omitempty"`
	Attempts         int       `json:"attempts"`
	CreatedAt        time.Time `json:"created_at"`
}

type BreakerOverrideRecord struct {
	TenantID     string    `json:"tenant_id"`
	ID           string    `json:"id"`
	ProviderID   string    `json:"provider_id"`
	OverrideMode string    `json:"override_mode"`
	Reason       string    `json:"reason"`
	Ordinal      uint64    `json:"ordinal"`
	ExpiresAt    time.Time `json:"expires_at"`
	RevokedAt    time.Time `json:"revoked_at,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	CreatedBy    string    `json:"created_by"`
}

type DeliveryQuery struct {
	TenantID            string
	ApplicationID       string
	TemplateID          string
	Channel             string
	Status              string
	CorrelationID       string
	RecipientSubjectRef string
	Limit               int
	BeforeCreatedAt     time.Time
	BeforeID            string
}

type Store interface {
	// Receipt & Audit & Outbox & Inbox
	GetCommandReceipt(ctx context.Context, tenantID, commandID string) (*CommandReceipt, bool, error)
	SaveCommandReceipt(ctx context.Context, rec CommandReceipt) error
	AppendAudit(ctx context.Context, rec AuditRecord) error
	ListAudit(ctx context.Context, tenantID string, limit int) ([]AuditRecord, error)
	ListAuditFiltered(ctx context.Context, filter AuditFilter) ([]AuditRecord, error)
	AppendOutbox(ctx context.Context, msg OutboxMessage) error
	ClaimPendingOutbox(ctx context.Context, limit int) ([]OutboxMessage, error)
	MarkOutboxPublished(ctx context.Context, tenantID, id string, now time.Time) error
	RecordInboxEvent(ctx context.Context, tenantID, source, eventID, digest string, now time.Time) (alreadyApplied bool, err error)

	// Categories & Applications
	SaveCategory(ctx context.Context, cat Category) error
	GetCategory(ctx context.Context, tenantID, id string) (*Category, error)
	GetCategoryByKey(ctx context.Context, tenantID, key string) (*Category, error)
	ListCategories(ctx context.Context, tenantID string) ([]Category, error)

	SaveApplication(ctx context.Context, app Application) error
	GetApplication(ctx context.Context, tenantID, id string) (*Application, error)
	GetApplicationByKey(ctx context.Context, tenantID, appKey string) (*Application, error)
	ListApplications(ctx context.Context, tenantID string) ([]Application, error)
	CountApplicationRequestsSince(ctx context.Context, tenantID, appID string, since time.Time) (int, error)

	// Layouts, Templates, Versions, Releases
	SaveLayoutVersion(ctx context.Context, lv LayoutVersion) error
	GetLayoutVersion(ctx context.Context, tenantID, id string) (*LayoutVersion, error)

	CreateTemplate(ctx context.Context, tmpl Template) error
	UpdateTemplate(ctx context.Context, tmpl Template) error
	GetTemplate(ctx context.Context, tenantID, id string) (*Template, error)
	GetTemplateIncludingDeleted(ctx context.Context, tenantID, id string) (*Template, error)
	GetTemplateByKey(ctx context.Context, tenantID, key string) (*Template, error)
	ListTemplates(ctx context.Context, tenantID, appID string, pageSize int, pageToken string) ([]Template, string, error)
	ListTemplateTags(ctx context.Context, tenantID string) ([]string, error)
	CountTemplateDeliveriesSince(ctx context.Context, tenantID, templateID string, since time.Time) (uint64, error)

	SaveTemplateVersion(ctx context.Context, tv TemplateVersion, expectedRowVersion uint64) error
	GetTemplateVersion(ctx context.Context, tenantID, versionID string) (*TemplateVersion, error)
	ListTemplateVersions(ctx context.Context, tenantID, templateID string) ([]TemplateVersion, error)

	SaveTemplateRelease(ctx context.Context, rel TemplateRelease) error
	GetActiveTemplateRelease(ctx context.Context, tenantID, templateID string, schemaMajor uint32, now time.Time) (*TemplateRelease, error)
	ListTemplateReleases(ctx context.Context, tenantID, templateID string) ([]TemplateRelease, error)
	ActivateDueScheduledReleases(ctx context.Context, now time.Time) ([]TemplateRelease, error)

	// Providers, Routing & Breakers
	SaveProviderProfile(ctx context.Context, p ProviderProfile) error
	GetProviderProfile(ctx context.Context, tenantID, id string) (*ProviderProfile, error)
	ListProviderProfiles(ctx context.Context, tenantID string) ([]ProviderProfile, error)
	SaveRoutingPolicy(ctx context.Context, rp RoutingPolicy) error
	ListRoutingPolicies(ctx context.Context, tenantID, channel string) ([]RoutingPolicy, error)
	SaveBreakerOverride(ctx context.Context, ov BreakerOverrideRecord) error
	GetActiveBreakerOverride(ctx context.Context, tenantID, providerID string, now time.Time) (*BreakerOverrideRecord, error)
	AppendBreakerTransition(ctx context.Context, tr BreakerTransition) error
	ListBreakerTransitions(ctx context.Context, tenantID, providerID string, limit int) ([]BreakerTransition, error)
	CountProviderDeliveriesSince(ctx context.Context, tenantID, providerID string, since time.Time) (uint64, error)

	// Trigger Rules
	SaveTriggerRule(ctx context.Context, rule TriggerRule) error
	GetTriggerRule(ctx context.Context, tenantID, id string) (*TriggerRule, error)
	ListTriggerRules(ctx context.Context, tenantID, workflowType string) ([]TriggerRule, error)
	SaveTriggerRuleRevision(ctx context.Context, rev TriggerRuleRevision) error
	ListTriggerRuleRevisions(ctx context.Context, tenantID, ruleID string) ([]TriggerRuleRevision, error)

	// Contacts, Preferences, Suppressions & In-App
	SaveSubjectContact(ctx context.Context, c SubjectContact) error
	GetSubjectContact(ctx context.Context, tenantID, subjectRef, channel string) (*SubjectContact, error)
	ListPreferences(ctx context.Context, tenantID, subjectRef string) ([]Preference, error)
	UpsertPreference(ctx context.Context, p Preference) error
	IsSuppressed(ctx context.Context, tenantID, channel, addressHash string, now time.Time) (bool, error)
	SaveSuppression(ctx context.Context, s Suppression) error

	SaveInAppMessage(ctx context.Context, m InAppMessage) error
	ListInAppMessages(ctx context.Context, tenantID, subjectRef string, unreadOnly bool, pageSize int, pageToken string) ([]InAppMessage, string, uint32, error)
	MarkInAppMessagesRead(ctx context.Context, tenantID, subjectRef string, ids []string, now time.Time) (uint32, error)

	// Requests & Deliveries
	FindRequestByIdempotency(ctx context.Context, tenantID, appID, idempotencyKey string) (*NotificationRequestRecord, []Delivery, bool, error)
	CreateRequestWithDeliveries(ctx context.Context, req NotificationRequestRecord, deliveries []Delivery, payloads []DeliveryPayload, outbox []OutboxMessage) error
	GetNotificationRequest(ctx context.Context, tenantID, requestID string) (*NotificationRequestRecord, []Delivery, error)
	GetDelivery(ctx context.Context, tenantID, deliveryID string) (*Delivery, *DeliveryPayload, error)
	ListDeliveryAttempts(ctx context.Context, tenantID, deliveryID string) ([]DeliveryAttempt, error)
	AppendDeliveryEvent(ctx context.Context, ev DeliveryEvent) error
	ListDeliveryEvents(ctx context.Context, tenantID, deliveryID string) ([]DeliveryEvent, error)
	UpdateDeliveryOutcome(ctx context.Context, d Delivery, eraseSecrets bool, erasedAt time.Time, attempts []DeliveryAttempt, outbox []OutboxMessage) error
	QueryDeliveries(ctx context.Context, q DeliveryQuery) ([]Delivery, time.Time, string, error)
}
