package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/crypto"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/provider"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/queue"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/renderer"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/schema"
	"github.com/google/uuid"
)

type Config struct {
	Environment        string
	TestAllowedDomains []string
	TestAllowedEmails  []string
	MaxRetryAttempts   int
	RetryBaseBackoff   time.Duration
	ProcessCatalog     ProcessContextCatalog
}

type Service struct {
	store             Store
	queue             queue.DispatchQueue
	crypto            *crypto.KeyRing
	renderer          *renderer.Engine
	router            *provider.Router
	processCatalog    ProcessContextCatalog
	environment       string
	now               func() time.Time
	newID             func() string
	maxRetryAttempts  int
	retryBaseBackoff  time.Duration
	mu                sync.RWMutex
	allowedTestDomain map[string]bool
	allowedTestAddr   map[string]bool
	inAppSeq          uint64
}

func NewService(
	store Store,
	dispatchQueue queue.DispatchQueue,
	keyRing *crypto.KeyRing,
	renderEngine *renderer.Engine,
	router *provider.Router,
	cfg Config,
) (*Service, error) {
	if store == nil || dispatchQueue == nil || keyRing == nil {
		return nil, errors.New("store, queue, and keyRing are required")
	}
	if renderEngine == nil {
		renderEngine = renderer.NewEngine(renderer.DefaultRenderLimits())
	}
	svc := &Service{
		store:             store,
		queue:             dispatchQueue,
		crypto:            keyRing,
		renderer:          renderEngine,
		router:            router,
		processCatalog:    cfg.ProcessCatalog,
		environment:       strings.ToLower(strings.TrimSpace(cfg.Environment)),
		now:               time.Now,
		newID:             newUUIDv7String,
		maxRetryAttempts:  cfg.MaxRetryAttempts,
		retryBaseBackoff:  cfg.RetryBaseBackoff,
		allowedTestDomain: make(map[string]bool),
		allowedTestAddr:   make(map[string]bool),
	}
	if svc.processCatalog == nil {
		svc.processCatalog = DefaultProcessContextCatalog()
	}
	if svc.maxRetryAttempts <= 0 {
		svc.maxRetryAttempts = 3
	}
	if svc.retryBaseBackoff <= 0 {
		svc.retryBaseBackoff = 5 * time.Second
	}
	for _, d := range cfg.TestAllowedDomains {
		clean := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "@"))
		if clean != "" {
			svc.allowedTestDomain[clean] = true
		}
	}
	for _, a := range cfg.TestAllowedEmails {
		clean := strings.ToLower(strings.TrimSpace(a))
		if clean != "" {
			svc.allowedTestAddr[clean] = true
		}
	}
	if svc.router == nil {
		svc.router = provider.NewRouter(
			[]provider.Driver{
				provider.NewSMTPDriver(),
				provider.NewInAppDriver(svc),
				provider.NewMockDriver(),
			},
			provider.NewBreakerRegistry(provider.DefaultBreakerPolicy()),
			nil,
			func() time.Time { return svc.now() },
		)
	}
	svc.router.Breakers().SetTransitionCallback(func(tenantID, providerID string, ev provider.BreakerTransitionEvent) {
		_ = svc.store.AppendBreakerTransition(context.Background(), BreakerTransition{
			TenantID:        tenantID,
			ID:              svc.newID(),
			ProviderID:      providerID,
			FromState:       string(ev.FromState),
			ToState:         string(ev.ToState),
			TriggerKind:     ev.TriggerKind,
			Reason:          ev.Reason,
			ErrorRateBps:    ev.ErrorRateBps,
			SlowCallRateBps: ev.SlowCallRateBps,
			OccurredAt:      ev.OccurredAt,
		})
	})
	return svc, nil
}

func (s *Service) SetClock(fn func() time.Time) {
	if fn != nil {
		s.now = fn
	}
}

func (s *Service) Router() *provider.Router   { return s.router }
func (s *Service) Store() Store               { return s.store }
func (s *Service) Queue() queue.DispatchQueue { return s.queue }

func newUUIDv7String() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString()
}

// DeliverInApp implements provider.InAppSink so the inapp driver persists the
// inbox message and schedules a payload-free InAppNotificationHint on outbox.
func (s *Service) DeliverInApp(ctx context.Context, msg provider.RenderedMessage) (string, error) {
	now := s.now().UTC()
	msgID := s.newID()
	title := strings.TrimSpace(msg.Subject)
	if title == "" {
		title = strings.TrimSpace(msg.TextBody)
	}
	bodyHTML := msg.HTMLBody
	if bodyHTML == "" {
		bodyHTML = renderer.SanitizeHTMLFragment(msg.TextBody)
	}
	inApp := InAppMessage{
		TenantID:    msg.TenantID,
		ID:          msgID,
		DeliveryID:  msg.DeliveryID,
		SubjectRef:  msg.RecipientSubjectRef,
		CategoryKey: msg.CategoryKey,
		Title:       title,
		BodyHTML:    bodyHTML,
		ActionURL:   msg.ActionURL,
		ExpiresAt:   now.Add(90 * 24 * time.Hour),
		CreatedAt:   now,
	}
	if err := s.store.SaveInAppMessage(ctx, inApp); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.inAppSeq++
	seq := s.inAppSeq
	s.mu.Unlock()

	hintPayload, _ := json.Marshal(map[string]any{
		"schema_version":        1,
		"event_id":              s.newID(),
		"tenant_id":             msg.TenantID,
		"recipient_subject_ref": msg.RecipientSubjectRef,
		"delivery_id":           msg.DeliveryID,
		"message_id":            msgID,
		"category_key":          msg.CategoryKey,
		"sequence":              seq,
		"occurred_at_epoch_ms":  now.UnixMilli(),
	})
	_ = s.store.AppendOutbox(ctx, OutboxMessage{
		TenantID:         msg.TenantID,
		ID:               s.newID(),
		EventID:          s.newID(),
		AggregateID:      msgID,
		AggregateVersion: 1,
		EventType:        "notification.inapp_hint",
		PayloadJSON:      hintPayload,
		CreatedAt:        now,
	})
	return msgID, nil
}

// --- Categories & Applications ---

func (s *Service) CreateCategory(ctx context.Context, cat Category) (Category, error) {
	if strings.TrimSpace(cat.TenantID) == "" || strings.TrimSpace(cat.CategoryKey) == "" || strings.TrimSpace(cat.Name) == "" {
		return Category{}, fmt.Errorf("%w: tenant_id, category_key, and name are required", ErrInvalid)
	}
	if cat.DefaultLane == "" {
		cat.DefaultLane = "NORMAL"
	}
	switch strings.ToUpper(cat.DefaultLane) {
	case "TRANSACTIONAL", "NORMAL", "BULK":
		cat.DefaultLane = strings.ToUpper(cat.DefaultLane)
	default:
		return Category{}, fmt.Errorf("%w: invalid default_lane", ErrInvalid)
	}
	if cat.AmbiguousOutcomePolicy == "" {
		cat.AmbiguousOutcomePolicy = string(provider.AmbiguousReconcileBeforeRetry)
	}
	switch provider.AmbiguousPolicy(cat.AmbiguousOutcomePolicy) {
	case provider.AmbiguousResendOnFailover, provider.AmbiguousReconcileBeforeRetry, provider.AmbiguousNoRetry:
	default:
		return Category{}, fmt.Errorf("%w: invalid ambiguous_outcome_policy", ErrInvalid)
	}
	if cat.RetentionClass == "" {
		cat.RetentionClass = "STANDARD"
	}
	if cat.Status == "" {
		cat.Status = "ACTIVE"
	}
	now := s.now().UTC()
	if cat.ID == "" {
		cat.ID = s.newID()
	}
	cat.CreatedAt = now
	cat.UpdatedAt = now
	if cat.CreatedBy == "" {
		cat.CreatedBy = "system"
	}
	cat.UpdatedBy = cat.CreatedBy
	cat.RowVersion = 1
	if err := s.store.SaveCategory(ctx, cat); err != nil {
		return Category{}, err
	}
	return cat, nil
}

func (s *Service) CreateApplication(ctx context.Context, app Application) (Application, error) {
	if strings.TrimSpace(app.TenantID) == "" || strings.TrimSpace(app.AppKey) == "" || strings.TrimSpace(app.Name) == "" {
		return Application{}, fmt.Errorf("%w: tenant_id, app_key, and name are required", ErrInvalid)
	}
	if app.AuthMode == "" {
		app.AuthMode = "WORKLOAD_MTLS"
	}
	if app.PrincipalRef == "" {
		app.PrincipalRef = app.AppKey
	}
	if len(app.AllowedNamespaces) == 0 {
		return Application{}, fmt.Errorf("%w: allowed_namespaces cannot be empty", ErrInvalid)
	}
	if len(app.AllowedChannels) == 0 {
		app.AllowedChannels = []string{"EMAIL", "SMS", "PUSH", "IN_APP", "ZNS"}
	}
	if app.DefaultLane == "" {
		app.DefaultLane = "NORMAL"
	}
	if app.Status == "" {
		app.Status = "ACTIVE"
	}
	now := s.now().UTC()
	if app.ID == "" {
		app.ID = s.newID()
	}
	app.CreatedAt = now
	app.UpdatedAt = now
	if app.CreatedBy == "" {
		app.CreatedBy = "system"
	}
	app.UpdatedBy = app.CreatedBy
	app.RowVersion = 1
	if err := s.store.SaveApplication(ctx, app); err != nil {
		return Application{}, err
	}
	return app, nil
}

func MatchNamespace(patterns []string, templateKey string) bool {
	for _, pat := range patterns {
		p := strings.TrimSpace(pat)
		if p == "*" || p == templateKey {
			return true
		}
		if strings.HasSuffix(p, ".*") {
			prefix := strings.TrimSuffix(p, "*")
			if strings.HasPrefix(templateKey, prefix) {
				return true
			}
		}
	}
	return false
}

// --- Templates, Versions & Releases ---

type CreateTemplateInput struct {
	TenantID       string
	ActorID        string
	CommandID      string
	IdempotencyKey string
	TemplateKey    string
	ApplicationID  string
	CategoryID     string
	Name           string
	DefaultLocale  string
}

func (s *Service) CreateTemplate(ctx context.Context, in CreateTemplateInput) (Template, bool, error) {
	if in.TenantID == "" || in.TemplateKey == "" || in.ApplicationID == "" || in.CategoryID == "" || in.Name == "" {
		return Template{}, false, fmt.Errorf("%w: missing required template fields", ErrInvalid)
	}
	if in.DefaultLocale == "" {
		in.DefaultLocale = "vi-VN"
	}
	digest := hashJSON(map[string]any{
		"op": "CreateTemplate", "key": in.TemplateKey, "app": in.ApplicationID,
		"cat": in.CategoryID, "name": in.Name, "locale": in.DefaultLocale,
	})
	receiptKey := commandKey(in.CommandID, in.IdempotencyKey)
	if receiptKey != "" {
		if rec, ok, err := s.store.GetCommandReceipt(ctx, in.TenantID, receiptKey); err != nil {
			return Template{}, false, err
		} else if ok {
			if rec.IntentDigest != digest {
				return Template{}, false, ErrConflict
			}
			var prev Template
			_ = json.Unmarshal(rec.ResultJSON, &prev)
			return prev, true, nil
		}
	}

	app, err := s.store.GetApplication(ctx, in.TenantID, in.ApplicationID)
	if err != nil {
		return Template{}, false, err
	}
	if !MatchNamespace(app.AllowedNamespaces, in.TemplateKey) {
		return Template{}, false, fmt.Errorf("%w: template_key %q is outside application allowed_namespaces", ErrForbidden, in.TemplateKey)
	}
	if _, err := s.store.GetCategory(ctx, in.TenantID, in.CategoryID); err != nil {
		return Template{}, false, err
	}

	now := s.now().UTC()
	actor := nonEmpty(in.ActorID, "system")
	tmpl := Template{
		TenantID:      in.TenantID,
		ID:            s.newID(),
		TemplateKey:   in.TemplateKey,
		ApplicationID: in.ApplicationID,
		CategoryID:    in.CategoryID,
		Name:          in.Name,
		DefaultLocale: in.DefaultLocale,
		Status:        "ACTIVE",
		RowVersion:    1,
		CreatedAt:     now,
		UpdatedAt:     now,
		CreatedBy:     actor,
		UpdatedBy:     actor,
	}
	if err := s.store.CreateTemplate(ctx, tmpl); err != nil {
		return Template{}, false, err
	}
	if receiptKey != "" {
		resBytes, _ := json.Marshal(tmpl)
		_ = s.store.SaveCommandReceipt(ctx, CommandReceipt{
			TenantID: in.TenantID, ID: s.newID(), CommandID: receiptKey,
			IntentDigest: digest, ActorRef: actorUUIDOrZero(actor),
			AggregateID: tmpl.ID, Status: "COMPLETED", ResultJSON: resBytes, CreatedAt: now,
		})
	}
	_ = s.store.AppendAudit(ctx, AuditRecord{
		TenantID: in.TenantID, ID: s.newID(), ActorRef: actorUUIDOrZero(actor),
		Action: "TEMPLATE_CREATED", ResourceRef: "template:" + tmpl.ID,
		CommandID: in.CommandID, AfterDigest: digest, OccurredAt: now,
	})
	return tmpl, false, nil
}

type SaveDraftInput struct {
	TenantID           string
	ActorID            string
	CommandID          string
	IdempotencyKey     string
	TemplateID         string
	VersionID          string
	SchemaMajor        uint32
	Syntax             string
	LayoutVersionID    string
	VariableSchemaJSON []byte
	Variants           []TemplateVariant
	SourceCommit       string
	ChangeNote         string
	ExpectedRowVersion uint64
}

func (s *Service) SaveTemplateVersionDraft(ctx context.Context, in SaveDraftInput) (TemplateVersion, error) {
	if in.TenantID == "" || in.TemplateID == "" || in.SchemaMajor == 0 {
		return TemplateVersion{}, fmt.Errorf("%w: tenant_id, template_id, and schema_major are required", ErrInvalid)
	}
	if _, err := s.store.GetTemplate(ctx, in.TenantID, in.TemplateID); err != nil {
		return TemplateVersion{}, err
	}
	if _, err := schema.Compile(in.VariableSchemaJSON); err != nil {
		return TemplateVersion{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	syntax := nonEmpty(in.Syntax, renderer.SyntaxGoTmplV1)
	if syntax != renderer.SyntaxGoTmplV1 && syntax != renderer.SyntaxHandlebarsV1 {
		return TemplateVersion{}, fmt.Errorf("%w: %v", ErrInvalid, renderer.ErrUnsupportedSyntax)
	}

	now := s.now().UTC()
	actor := nonEmpty(in.ActorID, "system")

	var versionNum uint64 = 1
	var existing *TemplateVersion
	if in.VersionID != "" {
		found, err := s.store.GetTemplateVersion(ctx, in.TenantID, in.VersionID)
		if err == nil {
			if isImmutableStatus(found.Status) {
				return TemplateVersion{}, ErrImmutable
			}
			existing = found
			versionNum = found.Version
		} else if !errors.Is(err, ErrNotFound) {
			return TemplateVersion{}, err
		}
	}
	if existing == nil {
		all, err := s.store.ListTemplateVersions(ctx, in.TenantID, in.TemplateID)
		if err != nil {
			return TemplateVersion{}, err
		}
		for _, v := range all {
			if v.Version >= versionNum {
				versionNum = v.Version + 1
			}
		}
	}
	verID := nonEmpty(in.VersionID, s.newID())
	variants := make([]TemplateVariant, len(in.Variants))
	seenPair := make(map[string]bool, len(in.Variants))
	for i, v := range in.Variants {
		ch := strings.ToUpper(strings.TrimSpace(v.Channel))
		loc := strings.TrimSpace(v.Locale)
		kind := nonEmpty(strings.ToUpper(strings.TrimSpace(v.VariantKind)), "INLINE")
		if ch == "" || loc == "" {
			return TemplateVersion{}, fmt.Errorf("%w: variant channel and locale are required", ErrInvalid)
		}
		pair := ch + ":" + loc
		if seenPair[pair] {
			return TemplateVersion{}, fmt.Errorf("%w: duplicate variant %s", ErrInvalid, pair)
		}
		seenPair[pair] = true
		vid := nonEmpty(v.ID, s.newID())
		vhash := computeVariantHash(kind, ch, loc, v.SubjectTmpl, v.HTMLTmpl, v.TextTmpl, v.ProviderTemplateID, v.ParamMappingJSON)
		variants[i] = TemplateVariant{
			TenantID:           in.TenantID,
			ID:                 vid,
			TemplateVersionID:  verID,
			Channel:            ch,
			Locale:             loc,
			VariantKind:        kind,
			SubjectTmpl:        v.SubjectTmpl,
			HTMLTmpl:           v.HTMLTmpl,
			TextTmpl:           v.TextTmpl,
			MJMLSource:         v.MJMLSource,
			ProviderTemplateID: v.ProviderTemplateID,
			ParamMappingJSON:   append([]byte(nil), v.ParamMappingJSON...),
			VariantHash:        vhash,
			RowVersion:         1,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
	}

	contentHash := computeVersionContentHash(syntax, in.SchemaMajor, in.LayoutVersionID, in.VariableSchemaJSON, variants, "")
	createdAt := now
	createdBy := actor
	if existing != nil {
		createdAt = existing.CreatedAt
		createdBy = existing.CreatedBy
	}
	tv := TemplateVersion{
		TenantID:           in.TenantID,
		ID:                 verID,
		TemplateID:         in.TemplateID,
		Version:            versionNum,
		SchemaMajor:        in.SchemaMajor,
		Syntax:             syntax,
		LayoutVersionID:    in.LayoutVersionID,
		VariableSchemaJSON: append([]byte(nil), in.VariableSchemaJSON...),
		ContentHash:        contentHash,
		SourceCommit:       in.SourceCommit,
		ChangeNote:         in.ChangeNote,
		Status:             "DRAFT",
		Variants:           variants,
		CreatedAt:          createdAt,
		UpdatedAt:          now,
		CreatedBy:          createdBy,
		UpdatedBy:          actor,
	}
	if err := s.store.SaveTemplateVersion(ctx, tv, in.ExpectedRowVersion); err != nil {
		return TemplateVersion{}, err
	}
	saved, err := s.store.GetTemplateVersion(ctx, in.TenantID, verID)
	if err != nil {
		return tv, nil
	}
	return *saved, nil
}

type PublishVersionInput struct {
	TenantID       string
	ActorID        string
	CommandID      string
	IdempotencyKey string
	TemplateID     string
	VersionID      string
	ApprovalRef    string
}

func (s *Service) PublishTemplateVersion(ctx context.Context, in PublishVersionInput) (TemplateVersion, bool, error) {
	if in.TenantID == "" || in.TemplateID == "" || in.VersionID == "" {
		return TemplateVersion{}, false, fmt.Errorf("%w: tenant_id, template_id, and version_id are required", ErrInvalid)
	}
	digest := hashJSON(map[string]any{"op": "PublishTemplateVersion", "tmpl": in.TemplateID, "ver": in.VersionID})
	receiptKey := commandKey(in.CommandID, in.IdempotencyKey)
	if receiptKey != "" {
		if rec, ok, err := s.store.GetCommandReceipt(ctx, in.TenantID, receiptKey); err != nil {
			return TemplateVersion{}, false, err
		} else if ok {
			if rec.IntentDigest != digest {
				return TemplateVersion{}, false, ErrConflict
			}
			var prev TemplateVersion
			_ = json.Unmarshal(rec.ResultJSON, &prev)
			return prev, true, nil
		}
	}

	tmpl, err := s.store.GetTemplate(ctx, in.TenantID, in.TemplateID)
	if err != nil {
		return TemplateVersion{}, false, err
	}
	tv, err := s.store.GetTemplateVersion(ctx, in.TenantID, in.VersionID)
	if err != nil {
		return TemplateVersion{}, false, err
	}
	if tv.TemplateID != tmpl.ID {
		return TemplateVersion{}, false, ErrNotFound
	}
	if isImmutableStatus(tv.Status) {
		return TemplateVersion{}, false, ErrImmutable
	}
	if len(tv.Variants) == 0 {
		return TemplateVersion{}, false, fmt.Errorf("%w: cannot publish template version without variants", ErrInvalid)
	}

	compiledSchema, err := schema.Compile(tv.VariableSchemaJSON)
	if err != nil {
		return TemplateVersion{}, false, fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	// Backward-compatibility gate within the same schema_major!
	allVersions, err := s.store.ListTemplateVersions(ctx, in.TenantID, in.TemplateID)
	if err != nil {
		return TemplateVersion{}, false, err
	}
	var latestPublished *TemplateVersion
	for i := range allVersions {
		cand := allVersions[i]
		if cand.ID != tv.ID && cand.SchemaMajor == tv.SchemaMajor && isImmutableStatus(cand.Status) {
			if latestPublished == nil || cand.Version > latestPublished.Version {
				cp := cand
				latestPublished = &cp
			}
		}
	}
	if latestPublished != nil {
		if compatErr := schema.CheckBackwardCompatible(latestPublished.VariableSchemaJSON, tv.VariableSchemaJSON); compatErr != nil {
			return TemplateVersion{}, false, fmt.Errorf("%w: %v", ErrIncompatibleSchema, compatErr)
		}
	}

	// Ensure default_locale exists for every supported channel.
	channelHasDefaultLocale := make(map[string]bool)
	channelsPresent := make(map[string]bool)
	for _, v := range tv.Variants {
		channelsPresent[v.Channel] = true
		if v.Locale == tmpl.DefaultLocale {
			channelHasDefaultLocale[v.Channel] = true
		}
	}
	for ch := range channelsPresent {
		if !channelHasDefaultLocale[ch] {
			return TemplateVersion{}, false, fmt.Errorf("%w: channel %s must include a variant for default_locale %s", ErrInvalid, ch, tmpl.DefaultLocale)
		}
	}

	var layoutHTML, layoutHash string
	if tv.LayoutVersionID != "" {
		lv, err := s.store.GetLayoutVersion(ctx, in.TenantID, tv.LayoutVersionID)
		if err != nil {
			return TemplateVersion{}, false, fmt.Errorf("%w: pinned layout_version_id not found", ErrInvalid)
		}
		if lv.Status != "PUBLISHED" {
			return TemplateVersion{}, false, fmt.Errorf("%w: pinned layout version must be PUBLISHED", ErrInvalid)
		}
		layoutHTML = lv.HTMLTemplate
		layoutHash = lv.ContentHash
	}

	// Example Render Gate across all variants!
	exampleData := compiledSchema.BuildExampleData()
	exampleJSON, _ := json.Marshal(exampleData)
	validatedExample, err := compiledSchema.ValidatePayload(exampleJSON)
	if err != nil {
		return TemplateVersion{}, false, fmt.Errorf("%w: schema example data failed validation: %v", ErrInvalid, err)
	}
	sampleCtx := defaultRenderContext(validatedExample, tmpl.DefaultLocale)

	for _, v := range tv.Variants {
		if v.VariantKind == "PROVIDER_TEMPLATE" {
			if strings.TrimSpace(v.ProviderTemplateID) == "" {
				return TemplateVersion{}, false, fmt.Errorf("%w: PROVIDER_TEMPLATE variant requires provider_template_id", ErrInvalid)
			}
			continue
		}
		vLayout := ""
		if v.Channel == "EMAIL" {
			vLayout = layoutHTML
		}
		compiledVar, err := renderer.CompileEphemeral(renderer.VariantSource{
			TemplateVersionID:  tv.ID,
			VariantID:          v.ID,
			Syntax:             tv.Syntax,
			Channel:            v.Channel,
			Locale:             v.Locale,
			SubjectTmpl:        v.SubjectTmpl,
			HTMLTmpl:           v.HTMLTmpl,
			TextTmpl:           v.TextTmpl,
			LayoutHTMLTmpl:     vLayout,
			DeclaredDataFields: compiledSchema.FieldSpecs(),
		})
		if err != nil {
			return TemplateVersion{}, false, fmt.Errorf("%w: variant %s/%s: %v", ErrInvalid, v.Channel, v.Locale, err)
		}
		if _, err := s.renderer.Render(compiledVar, sampleCtx); err != nil {
			return TemplateVersion{}, false, fmt.Errorf("%w: example render gate failed for %s/%s: %v", ErrInvalid, v.Channel, v.Locale, err)
		}
	}

	now := s.now().UTC()
	actor := nonEmpty(in.ActorID, "system")
	beforeDigest := tv.ContentHash
	tv.ContentHash = computeVersionContentHash(tv.Syntax, tv.SchemaMajor, tv.LayoutVersionID, tv.VariableSchemaJSON, tv.Variants, layoutHash)
	tv.Status = "PUBLISHED"
	tv.ApprovalRef = in.ApprovalRef
	tv.PublishedAt = now
	tv.UpdatedAt = now
	tv.UpdatedBy = actor

	if err := s.store.SaveTemplateVersion(ctx, *tv, tv.RowVersion); err != nil {
		return TemplateVersion{}, false, err
	}
	// Pre-populate immutable cache for INLINE variants.
	for _, v := range tv.Variants {
		if v.VariantKind == "INLINE" {
			vLayout := ""
			if v.Channel == "EMAIL" {
				vLayout = layoutHTML
			}
			_, _ = s.renderer.CompilePublished(renderer.VariantSource{
				TemplateVersionID:  tv.ID,
				VariantID:          v.ID,
				Syntax:             tv.Syntax,
				Channel:            v.Channel,
				Locale:             v.Locale,
				SubjectTmpl:        v.SubjectTmpl,
				HTMLTmpl:           v.HTMLTmpl,
				TextTmpl:           v.TextTmpl,
				LayoutHTMLTmpl:     vLayout,
				DeclaredDataFields: compiledSchema.FieldSpecs(),
			})
		}
	}

	published, _ := s.store.GetTemplateVersion(ctx, in.TenantID, tv.ID)
	if published != nil {
		tv = published
	}
	if receiptKey != "" {
		resBytes, _ := json.Marshal(tv)
		_ = s.store.SaveCommandReceipt(ctx, CommandReceipt{
			TenantID: in.TenantID, ID: s.newID(), CommandID: receiptKey,
			IntentDigest: digest, ActorRef: actorUUIDOrZero(actor),
			AggregateID: tv.ID, Status: "COMPLETED", ResultJSON: resBytes, CreatedAt: now,
		})
	}
	_ = s.store.AppendAudit(ctx, AuditRecord{
		TenantID: in.TenantID, ID: s.newID(), ActorRef: actorUUIDOrZero(actor),
		Action: "TEMPLATE_VERSION_PUBLISHED", ResourceRef: "template_version:" + tv.ID,
		CommandID: in.CommandID, BeforeDigest: beforeDigest, AfterDigest: tv.ContentHash, OccurredAt: now,
	})
	return *tv, false, nil
}

type ActivateReleaseInput struct {
	TenantID          string
	ActorID           string
	CommandID         string
	IdempotencyKey    string
	TemplateID        string
	SchemaMajor       uint32
	TemplateVersionID string
	ActivateAt        time.Time
	ApprovalRef       string
}

func (s *Service) ActivateTemplateRelease(ctx context.Context, in ActivateReleaseInput) (TemplateRelease, bool, error) {
	if in.TenantID == "" || in.TemplateID == "" || in.SchemaMajor == 0 || in.TemplateVersionID == "" {
		return TemplateRelease{}, false, fmt.Errorf("%w: tenant_id, template_id, schema_major, and template_version_id are required", ErrInvalid)
	}
	digest := hashJSON(map[string]any{
		"op": "ActivateTemplateRelease", "tmpl": in.TemplateID,
		"major": in.SchemaMajor, "ver": in.TemplateVersionID, "at": in.ActivateAt.UnixMilli(),
	})
	receiptKey := commandKey(in.CommandID, in.IdempotencyKey)
	if receiptKey != "" {
		if rec, ok, err := s.store.GetCommandReceipt(ctx, in.TenantID, receiptKey); err != nil {
			return TemplateRelease{}, false, err
		} else if ok {
			if rec.IntentDigest != digest {
				return TemplateRelease{}, false, ErrConflict
			}
			var prev TemplateRelease
			_ = json.Unmarshal(rec.ResultJSON, &prev)
			return prev, true, nil
		}
	}

	tv, err := s.store.GetTemplateVersion(ctx, in.TenantID, in.TemplateVersionID)
	if err != nil {
		return TemplateRelease{}, false, err
	}
	if tv.TemplateID != in.TemplateID || tv.SchemaMajor != in.SchemaMajor {
		return TemplateRelease{}, false, fmt.Errorf("%w: template_version does not belong to template/schema_major", ErrInvalid)
	}
	if tv.Status != "PUBLISHED" {
		return TemplateRelease{}, false, fmt.Errorf("%w: only PUBLISHED template versions can be released", ErrInvalid)
	}

	existingReleases, err := s.store.ListTemplateReleases(ctx, in.TenantID, in.TemplateID)
	if err != nil {
		return TemplateRelease{}, false, err
	}
	var maxOrdinal uint64
	for _, r := range existingReleases {
		if r.SchemaMajor == in.SchemaMajor && r.Ordinal > maxOrdinal {
			maxOrdinal = r.Ordinal
		}
	}

	now := s.now().UTC()
	activateAt := in.ActivateAt.UTC()
	if activateAt.IsZero() {
		activateAt = now
	}
	status := "ACTIVE"
	var activatedAt time.Time
	if activateAt.After(now) {
		status = "SCHEDULED"
	} else {
		activatedAt = now
	}
	actor := nonEmpty(in.ActorID, "system")
	rel := TemplateRelease{
		TenantID:          in.TenantID,
		ID:                s.newID(),
		TemplateID:        in.TemplateID,
		SchemaMajor:       in.SchemaMajor,
		TemplateVersionID: in.TemplateVersionID,
		Ordinal:           maxOrdinal + 1,
		PreviousOrdinal:   maxOrdinal,
		ActivateAt:        activateAt,
		ActivatedAt:       activatedAt,
		ApprovalRef:       in.ApprovalRef,
		Status:            status,
		RowVersion:        1,
		CreatedAt:         now,
		UpdatedAt:         now,
		CreatedBy:         actor,
	}
	if err := s.store.SaveTemplateRelease(ctx, rel); err != nil {
		return TemplateRelease{}, false, err
	}
	invPayload, _ := json.Marshal(map[string]any{
		"schema_version":       1,
		"event_id":             s.newID(),
		"tenant_id":            in.TenantID,
		"artifact_kind":        "TEMPLATE_RELEASE",
		"artifact_id":          in.TemplateID,
		"ordinal":              rel.Ordinal,
		"occurred_at_epoch_ms": now.UnixMilli(),
	})
	_ = s.store.AppendOutbox(ctx, OutboxMessage{
		TenantID: in.TenantID, ID: s.newID(), EventID: s.newID(),
		AggregateID: in.TemplateID, AggregateVersion: rel.Ordinal,
		EventType: "notification.invalidation", PayloadJSON: invPayload, CreatedAt: now,
	})
	if receiptKey != "" {
		resBytes, _ := json.Marshal(rel)
		_ = s.store.SaveCommandReceipt(ctx, CommandReceipt{
			TenantID: in.TenantID, ID: s.newID(), CommandID: receiptKey,
			IntentDigest: digest, ActorRef: actorUUIDOrZero(actor),
			AggregateID: rel.ID, Status: "COMPLETED", ResultJSON: resBytes, CreatedAt: now,
		})
	}
	_ = s.store.AppendAudit(ctx, AuditRecord{
		TenantID: in.TenantID, ID: s.newID(), ActorRef: actorUUIDOrZero(actor),
		Action: "TEMPLATE_RELEASE_ACTIVATED", ResourceRef: fmt.Sprintf("template:%s@%d#ord%d", in.TemplateID, in.SchemaMajor, rel.Ordinal),
		CommandID: in.CommandID, AfterDigest: tv.ContentHash, OccurredAt: now,
	})
	return rel, false, nil
}

// --- 3-Mode Preview & Guarded Test Send ---

type PreviewInput struct {
	TenantID                string
	ActorID                 string
	ActorCapabilities       []string
	Mode                    string // "DRAFT" | "PUBLISHED_VERSION" | "DELIVERY_REPLAY"
	Syntax                  string
	TemplateVersionID       string
	DeliveryID              string
	Channel                 string
	Locale                  string
	DraftVariableSchemaJSON []byte
	DraftVariant            *TemplateVariant
	SampleDataJSON          []byte
}

type PreviewResult struct {
	renderer.RenderOutput
	RenderedWithSampleData bool
}

func (s *Service) RenderPreview(ctx context.Context, in PreviewInput) (PreviewResult, error) {
	if in.TenantID == "" {
		return PreviewResult{}, fmt.Errorf("%w: tenant_id is required", ErrInvalid)
	}
	switch strings.ToUpper(strings.TrimSpace(in.Mode)) {
	case "DRAFT":
		if in.DraftVariant == nil {
			return PreviewResult{}, fmt.Errorf("%w: draft_variant is required for DRAFT preview", ErrInvalid)
		}
		compiledSchema, err := schema.Compile(in.DraftVariableSchemaJSON)
		if err != nil {
			return PreviewResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		var data map[string]any
		withSample := false
		if len(strings.TrimSpace(string(in.SampleDataJSON))) > 0 {
			data, err = compiledSchema.ValidatePayload(in.SampleDataJSON)
			if err != nil {
				return PreviewResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
			}
		} else {
			data = compiledSchema.BuildExampleData()
			withSample = true
		}
		ch := nonEmpty(in.Channel, in.DraftVariant.Channel)
		loc := nonEmpty(in.Locale, in.DraftVariant.Locale)
		compiledVar, err := renderer.CompileEphemeral(renderer.VariantSource{
			Syntax:             nonEmpty(in.Syntax, renderer.SyntaxGoTmplV1),
			Channel:            ch,
			Locale:             loc,
			SubjectTmpl:        in.DraftVariant.SubjectTmpl,
			HTMLTmpl:           in.DraftVariant.HTMLTmpl,
			TextTmpl:           in.DraftVariant.TextTmpl,
			DeclaredDataFields: compiledSchema.FieldSpecs(),
		})
		if err != nil {
			return PreviewResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		out, err := s.renderer.Render(compiledVar, defaultRenderContext(data, loc))
		if err != nil {
			return PreviewResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		return PreviewResult{RenderOutput: out, RenderedWithSampleData: withSample}, nil

	case "PUBLISHED_VERSION":
		tv, err := s.store.GetTemplateVersion(ctx, in.TenantID, in.TemplateVersionID)
		if err != nil {
			return PreviewResult{}, err
		}
		if !isImmutableStatus(tv.Status) {
			return PreviewResult{}, fmt.Errorf("%w: template version is not published", ErrInvalid)
		}
		tmpl, err := s.store.GetTemplate(ctx, in.TenantID, tv.TemplateID)
		if err != nil {
			return PreviewResult{}, err
		}
		compiledSchema, err := schema.Compile(tv.VariableSchemaJSON)
		if err != nil {
			return PreviewResult{}, err
		}
		variant, err := pickVariant(tv.Variants, strings.ToUpper(in.Channel), in.Locale, tmpl.DefaultLocale)
		if err != nil {
			return PreviewResult{}, err
		}
		layoutHTML := s.resolveLayoutHTML(ctx, in.TenantID, tv.LayoutVersionID, variant.Channel)
		compiledVar, err := s.renderer.CompilePublished(renderer.VariantSource{
			TemplateVersionID:  tv.ID,
			VariantID:          variant.ID,
			Syntax:             tv.Syntax,
			Channel:            variant.Channel,
			Locale:             variant.Locale,
			SubjectTmpl:        variant.SubjectTmpl,
			HTMLTmpl:           variant.HTMLTmpl,
			TextTmpl:           variant.TextTmpl,
			LayoutHTMLTmpl:     layoutHTML,
			DeclaredDataFields: compiledSchema.FieldSpecs(),
		})
		if err != nil {
			return PreviewResult{}, err
		}
		var data map[string]any
		withSample := false
		if len(strings.TrimSpace(string(in.SampleDataJSON))) > 0 {
			data, err = compiledSchema.ValidatePayload(in.SampleDataJSON)
			if err != nil {
				return PreviewResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
			}
		} else {
			data = compiledSchema.MaskData(compiledSchema.BuildExampleData(), true)
			withSample = true
		}
		out, err := s.renderer.Render(compiledVar, defaultRenderContext(data, variant.Locale))
		if err != nil {
			return PreviewResult{}, err
		}
		return PreviewResult{RenderOutput: out, RenderedWithSampleData: withSample}, nil

	case "DELIVERY_REPLAY":
		if in.DeliveryID == "" {
			return PreviewResult{}, fmt.Errorf("%w: delivery_id is required for DELIVERY_REPLAY", ErrInvalid)
		}
		del, payload, err := s.store.GetDelivery(ctx, in.TenantID, in.DeliveryID)
		if err != nil {
			return PreviewResult{}, err
		}
		tv, err := s.store.GetTemplateVersion(ctx, in.TenantID, del.TemplateVersionID)
		if err != nil {
			return PreviewResult{}, err
		}
		compiledSchema, err := schema.Compile(tv.VariableSchemaJSON)
		if err != nil {
			return PreviewResult{}, err
		}
		var variant *TemplateVariant
		for i := range tv.Variants {
			if tv.Variants[i].ID == del.VariantID {
				variant = &tv.Variants[i]
				break
			}
		}
		if variant == nil {
			return PreviewResult{}, ErrNotFound
		}
		layoutHTML := s.resolveLayoutHTML(ctx, in.TenantID, tv.LayoutVersionID, variant.Channel)
		compiledVar, err := s.renderer.CompilePublished(renderer.VariantSource{
			TemplateVersionID:  tv.ID,
			VariantID:          variant.ID,
			Syntax:             tv.Syntax,
			Channel:            variant.Channel,
			Locale:             variant.Locale,
			SubjectTmpl:        variant.SubjectTmpl,
			HTMLTmpl:           variant.HTMLTmpl,
			TextTmpl:           variant.TextTmpl,
			LayoutHTMLTmpl:     layoutHTML,
			DeclaredDataFields: compiledSchema.FieldSpecs(),
		})
		if err != nil {
			return PreviewResult{}, err
		}

		now := s.now().UTC()
		var renderData map[string]any
		withSample := false
		var extraWarnings []string

		if payload != nil && now.Before(payload.ExpiresAt) && len(payload.EncryptedData) > 0 {
			if !hasAnyCapability(in.ActorCapabilities, "notification.delivery.inspect_payload") {
				return PreviewResult{}, fmt.Errorf("%w: notification.delivery.inspect_payload capability is required to inspect retained delivery payloads", ErrForbidden)
			}
			plainBytes, decErr := s.crypto.Decrypt(in.TenantID, del.ID, payload.KeyRef, payload.EncryptedData)
			if decErr != nil {
				return PreviewResult{}, decErr
			}
			var rawMap map[string]any
			if err := json.Unmarshal(plainBytes, &rawMap); err != nil {
				return PreviewResult{}, err
			}
			// Secrets are wiped upon terminal state and always masked as "******" on replay!
			renderData = compiledSchema.MaskData(rawMap, false)
			// Mandatory Audit Evidence before returning decrypted payload preview!
			if err := s.store.AppendAudit(ctx, AuditRecord{
				TenantID:    in.TenantID,
				ID:          s.newID(),
				ActorRef:    actorUUIDOrZero(nonEmpty(in.ActorID, "system")),
				Action:      "DELIVERY_PAYLOAD_INSPECTED",
				ResourceRef: "delivery:" + del.ID,
				OccurredAt:  now,
			}); err != nil {
				return PreviewResult{}, err
			}
		} else {
			renderData = compiledSchema.MaskData(compiledSchema.BuildExampleData(), true)
			withSample = true
			extraWarnings = append(extraWarnings, "retained delivery payload expired; rendered with version sample data")
		}

		out, err := s.renderer.Render(compiledVar, defaultRenderContext(renderData, variant.Locale))
		if err != nil {
			return PreviewResult{}, err
		}
		out.Warnings = append(out.Warnings, extraWarnings...)
		return PreviewResult{RenderOutput: out, RenderedWithSampleData: withSample}, nil

	default:
		return PreviewResult{}, fmt.Errorf("%w: unsupported preview mode %q", ErrInvalid, in.Mode)
	}
}

type TestSendInput struct {
	TenantID                 string
	ActorID                  string
	CommandID                string
	IdempotencyKey           string
	TemplateVersionID        string
	Channel                  string
	Locale                   string
	VerifiedRecipientAddress string
	SampleDataJSON           []byte
}

func (s *Service) SendTestNotification(ctx context.Context, in TestSendInput) (NotificationRequestRecord, Delivery, error) {
	if in.TenantID == "" || in.TemplateVersionID == "" || in.Channel == "" || in.VerifiedRecipientAddress == "" {
		return NotificationRequestRecord{}, Delivery{}, fmt.Errorf("%w: missing required test send fields", ErrInvalid)
	}
	ch := strings.ToUpper(strings.TrimSpace(in.Channel))
	if !s.isTestRecipientAllowed(ctx, in.TenantID, in.ActorID, ch, in.VerifiedRecipientAddress) {
		return NotificationRequestRecord{}, Delivery{}, fmt.Errorf("%w: recipient address %q is not in actor verified contacts or tenant test allowlist", ErrForbidden, in.VerifiedRecipientAddress)
	}

	tv, err := s.store.GetTemplateVersion(ctx, in.TenantID, in.TemplateVersionID)
	if err != nil {
		return NotificationRequestRecord{}, Delivery{}, err
	}
	tmpl, err := s.store.GetTemplate(ctx, in.TenantID, tv.TemplateID)
	if err != nil {
		return NotificationRequestRecord{}, Delivery{}, err
	}
	app, err := s.store.GetApplication(ctx, in.TenantID, tmpl.ApplicationID)
	if err != nil {
		return NotificationRequestRecord{}, Delivery{}, err
	}
	cat, err := s.store.GetCategory(ctx, in.TenantID, tmpl.CategoryID)
	if err != nil {
		return NotificationRequestRecord{}, Delivery{}, err
	}
	dataBytes := in.SampleDataJSON
	if len(strings.TrimSpace(string(dataBytes))) == 0 {
		cs, err := schema.Compile(tv.VariableSchemaJSON)
		if err != nil {
			return NotificationRequestRecord{}, Delivery{}, err
		}
		dataBytes, _ = json.Marshal(cs.BuildExampleData())
	}

	subRes, err := s.SubmitNotification(ctx, SubmitInput{
		TenantID:       in.TenantID,
		ActorID:        nonEmpty(in.ActorID, "system"),
		ApplicationKey: app.AppKey,
		CommandID:      in.CommandID,
		IdempotencyKey: nonEmpty(in.IdempotencyKey, "test-"+s.newID()),
		TemplateKey:    tmpl.TemplateKey,
		SchemaMajor:    tv.SchemaMajor,
		PinnedVersion:  tv.Version,
		CategoryKey:    cat.CategoryKey,
		Locale:         in.Locale,
		DataJSON:       dataBytes,
		IsTest:         true,
		Recipients: []RecipientInput{{
			SubjectRef:      in.ActorID,
			Channel:         ch,
			ExternalAddress: in.VerifiedRecipientAddress,
			Locale:          in.Locale,
		}},
	})
	if err != nil {
		return NotificationRequestRecord{}, Delivery{}, err
	}
	_ = s.store.AppendAudit(ctx, AuditRecord{
		TenantID: in.TenantID, ID: s.newID(), ActorRef: actorUUIDOrZero(nonEmpty(in.ActorID, "system")),
		Action: "TEST_NOTIFICATION_SENT", ResourceRef: "request:" + subRes.Request.ID,
		CommandID: in.CommandID, OccurredAt: s.now().UTC(),
	})
	return subRes.Request, subRes.Deliveries[0], nil
}

func (s *Service) isTestRecipientAllowed(ctx context.Context, tenantID, actorID, channel, rawAddr string) bool {
	if channel == "IN_APP" && actorID != "" && rawAddr == actorID {
		return true
	}
	if actorID != "" {
		if contact, err := s.store.GetSubjectContact(ctx, tenantID, actorID, channel); err == nil && contact.Verified {
			if contact.AddressHash == s.crypto.HashAddress(tenantID, channel, rawAddr) {
				return true
			}
		}
	}
	norm := strings.ToLower(strings.TrimSpace(rawAddr))
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.allowedTestAddr[norm] {
		return true
	}
	if at := strings.LastIndexByte(norm, '@'); at > 0 && at < len(norm)-1 {
		domain := norm[at+1:]
		if s.allowedTestDomain[domain] {
			return true
		}
	}
	return false
}

// --- SubmitNotification & Dispatcher Runtime ---

type RecipientInput struct {
	SubjectRef      string
	Channel         string
	ExternalAddress string
	DisplayName     string
	Locale          string
	Timezone        string
}

type SubmitInput struct {
	TenantID       string
	ActorID        string
	ApplicationKey string
	CommandID      string
	IdempotencyKey string
	CorrelationID  string
	TemplateKey    string
	SchemaMajor    uint32
	PinnedVersion  uint64
	CategoryKey    string
	LaneOverride   string
	Priority       int
	Locale         string
	DataJSON       []byte
	Recipients     []RecipientInput
	SendAt         time.Time
	ExpiresAt      time.Time
	IsTest         bool
}

type SubmitResult struct {
	Request    NotificationRequestRecord
	Deliveries []Delivery
	Replayed   bool
}

func (s *Service) SubmitNotification(ctx context.Context, in SubmitInput) (SubmitResult, error) {
	if in.TenantID == "" || in.IdempotencyKey == "" || in.TemplateKey == "" || in.SchemaMajor == 0 || len(in.Recipients) == 0 {
		return SubmitResult{}, fmt.Errorf("%w: tenant_id, idempotency_key, template_key, schema_major, and recipients are required", ErrInvalid)
	}
	tmpl, err := s.store.GetTemplateByKey(ctx, in.TenantID, in.TemplateKey)
	if err != nil {
		return SubmitResult{}, err
	}
	var app *Application
	if in.ApplicationKey != "" {
		app, err = s.store.GetApplicationByKey(ctx, in.TenantID, in.ApplicationKey)
	} else {
		app, err = s.store.GetApplication(ctx, in.TenantID, tmpl.ApplicationID)
	}
	if err != nil {
		return SubmitResult{}, err
	}
	if !MatchNamespace(app.AllowedNamespaces, tmpl.TemplateKey) {
		return SubmitResult{}, fmt.Errorf("%w: application %q cannot send template %q", ErrForbidden, app.AppKey, tmpl.TemplateKey)
	}

	reqDigest := computeSubmitDigest(in)
	if prevReq, prevDels, found, err := s.store.FindRequestByIdempotency(ctx, in.TenantID, app.ID, in.IdempotencyKey); err != nil {
		return SubmitResult{}, err
	} else if found {
		if prevReq.RequestDigest != reqDigest {
			return SubmitResult{}, fmt.Errorf("%w: idempotency_key %q reused with a different request payload", ErrConflict, in.IdempotencyKey)
		}
		return SubmitResult{Request: *prevReq, Deliveries: prevDels, Replayed: true}, nil
	}

	now := s.now().UTC()
	if !in.IsTest {
		if app.QuotaConfig.MaxPerMinute > 0 {
			cnt, err := s.store.CountApplicationRequestsSince(ctx, in.TenantID, app.ID, now.Add(-time.Minute))
			if err != nil {
				return SubmitResult{}, err
			}
			if cnt >= app.QuotaConfig.MaxPerMinute {
				return SubmitResult{}, ErrQuotaExceeded
			}
		}
		if app.QuotaConfig.MaxPerDay > 0 {
			cnt, err := s.store.CountApplicationRequestsSince(ctx, in.TenantID, app.ID, now.Add(-24*time.Hour))
			if err != nil {
				return SubmitResult{}, err
			}
			if cnt >= app.QuotaConfig.MaxPerDay {
				return SubmitResult{}, ErrQuotaExceeded
			}
		}
	}

	var cat *Category
	if in.CategoryKey != "" {
		cat, err = s.store.GetCategoryByKey(ctx, in.TenantID, in.CategoryKey)
	} else {
		cat, err = s.store.GetCategory(ctx, in.TenantID, tmpl.CategoryID)
	}
	if err != nil {
		return SubmitResult{}, err
	}

	var tv *TemplateVersion
	var releaseOrdinal uint64
	if in.PinnedVersion > 0 {
		versions, err := s.store.ListTemplateVersions(ctx, in.TenantID, tmpl.ID)
		if err != nil {
			return SubmitResult{}, err
		}
		for i := range versions {
			if versions[i].Version == in.PinnedVersion && versions[i].SchemaMajor == in.SchemaMajor {
				cp := versions[i]
				tv = &cp
				break
			}
		}
		if tv == nil {
			return SubmitResult{}, ErrNotFound
		}
	} else {
		rel, err := s.store.GetActiveTemplateRelease(ctx, in.TenantID, tmpl.ID, in.SchemaMajor, now)
		if err != nil {
			return SubmitResult{}, fmt.Errorf("%w: no active release for %s@%d", ErrNotFound, tmpl.TemplateKey, in.SchemaMajor)
		}
		releaseOrdinal = rel.Ordinal
		tv, err = s.store.GetTemplateVersion(ctx, in.TenantID, rel.TemplateVersionID)
		if err != nil {
			return SubmitResult{}, err
		}
	}

	compiledSchema, err := schema.Compile(tv.VariableSchemaJSON)
	if err != nil {
		return SubmitResult{}, err
	}
	cleanData, secrets, err := compiledSchema.ValidateAndSplit(in.DataJSON)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	lane := normalizeLane(nonEmpty(in.LaneOverride, nonEmpty(cat.DefaultLane, app.DefaultLane)))
	priority := queue.NormalizePriority(in.Priority)
	reqID := s.newID()
	alreadyExpired := !in.ExpiresAt.IsZero() && !now.Before(in.ExpiresAt.UTC())
	reqStatus := "ACCEPTED"
	if alreadyExpired {
		reqStatus = "EXPIRED"
	}

	reqRec := NotificationRequestRecord{
		TenantID:       in.TenantID,
		ID:             reqID,
		ApplicationID:  app.ID,
		IdempotencyKey: in.IdempotencyKey,
		RequestDigest:  reqDigest,
		TemplateID:     tmpl.ID,
		TemplateKey:    tmpl.TemplateKey,
		SchemaMajor:    in.SchemaMajor,
		PinnedVersion:  in.PinnedVersion,
		CategoryID:     cat.ID,
		CategoryKey:    cat.CategoryKey,
		Lane:           lane,
		Priority:       priority,
		CorrelationID:  in.CorrelationID,
		IsTest:         in.IsTest,
		SendAt:         in.SendAt.UTC(),
		ExpiresAt:      in.ExpiresAt.UTC(),
		Status:         reqStatus,
		CreatedAt:      now,
		UpdatedAt:      now,
		CreatedBy:      nonEmpty(in.ActorID, app.AppKey),
	}

	cleanJSON, _ := json.Marshal(cleanData)
	var secretJSON []byte
	if len(secrets) > 0 {
		secretJSON, _ = json.Marshal(secrets)
	}

	deliveries := make([]Delivery, 0, len(in.Recipients))
	payloads := make([]DeliveryPayload, 0, len(in.Recipients))
	outbox := make([]OutboxMessage, 0, len(in.Recipients))

	for _, rec := range in.Recipients {
		ch := strings.ToUpper(strings.TrimSpace(rec.Channel))
		if ch == "" {
			ch = "EMAIL"
		}
		rawAddr := strings.TrimSpace(rec.ExternalAddress)
		contactLocale := ""
		if rawAddr == "" && rec.SubjectRef != "" {
			if ch == "IN_APP" {
				rawAddr = rec.SubjectRef
			} else if contact, cerr := s.store.GetSubjectContact(ctx, in.TenantID, rec.SubjectRef, ch); cerr == nil {
				plainAddr, derr := s.crypto.Decrypt(in.TenantID, contact.ID, contact.KeyRef, contact.EncryptedAddress)
				if derr == nil {
					rawAddr = string(plainAddr)
				}
				contactLocale = contact.Locale
			}
		}
		if rawAddr == "" {
			return SubmitResult{}, fmt.Errorf("%w: recipient address could not be resolved for channel %s", ErrInvalid, ch)
		}

		preferredLocale := firstNonEmpty(rec.Locale, in.Locale, contactLocale, tmpl.DefaultLocale)
		variant, err := pickVariant(tv.Variants, ch, preferredLocale, tmpl.DefaultLocale)
		if err != nil {
			return SubmitResult{}, err
		}

		delID := s.newID()
		encAddr, keyRef, err := s.crypto.Encrypt(in.TenantID, delID, []byte(rawAddr))
		if err != nil {
			return SubmitResult{}, err
		}
		encData, _, err := s.crypto.Encrypt(in.TenantID, delID, cleanJSON)
		if err != nil {
			return SubmitResult{}, err
		}
		var encSecrets []byte
		var secretsErasedAt time.Time
		if len(secretJSON) > 0 && !alreadyExpired {
			encSecrets, _, err = s.crypto.Encrypt(in.TenantID, delID, secretJSON)
			if err != nil {
				return SubmitResult{}, err
			}
		} else if len(secretJSON) > 0 && alreadyExpired {
			secretsErasedAt = now
		}

		delStatus := "QUEUED"
		var completedAt time.Time
		if alreadyExpired {
			delStatus = "EXPIRED"
			completedAt = now
		} else if !in.SendAt.IsZero() && in.SendAt.UTC().After(now) {
			delStatus = "SCHEDULED"
		}

		retentionDays := 30
		if lane == queue.LaneBulk {
			retentionDays = 7
		}
		del := Delivery{
			TenantID:            in.TenantID,
			ID:                  delID,
			RequestID:           reqID,
			ApplicationID:       app.ID,
			TemplateID:          tmpl.ID,
			TemplateVersionID:   tv.ID,
			VariantID:           variant.ID,
			ReleaseOrdinal:      releaseOrdinal,
			RecipientSubjectRef: rec.SubjectRef,
			AddressHash:         s.crypto.HashAddress(in.TenantID, ch, rawAddr),
			MaskedAddress:       crypto.MaskAddress(ch, rawAddr),
			Channel:             ch,
			Lane:                lane,
			Status:              delStatus,
			ContentHash:         tv.ContentHash,
			CorrelationID:       in.CorrelationID,
			ExpiresAt:           in.ExpiresAt.UTC(),
			CompletedAt:         completedAt,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		deliveries = append(deliveries, del)
		payloads = append(payloads, DeliveryPayload{
			TenantID:         in.TenantID,
			ID:               s.newID(),
			DeliveryID:       delID,
			EncryptedAddress: encAddr,
			EncryptedData:    encData,
			EncryptedSecrets: encSecrets,
			KeyRef:           keyRef,
			SecretsErasedAt:  secretsErasedAt,
			ExpiresAt:        now.Add(time.Duration(retentionDays) * 24 * time.Hour),
			CreatedAt:        now,
		})
		outbox = append(outbox, s.buildDeliveryOutbox(del, tmpl.TemplateKey, in.SchemaMajor, "", now))
	}

	if err := s.store.CreateRequestWithDeliveries(ctx, reqRec, deliveries, payloads, outbox); err != nil {
		return SubmitResult{}, err
	}
	for _, d := range deliveries {
		_ = s.store.AppendDeliveryEvent(ctx, DeliveryEvent{
			TenantID:   in.TenantID,
			ID:         s.newID(),
			DeliveryID: d.ID,
			EventType:  "REQUEST_ACCEPTED",
			FromStatus: "ACCEPTED",
			ToStatus:   d.Status,
			Detail:     map[string]any{"lane": lane, "priority": priority},
			OccurredAt: now,
		})
	}

	if !alreadyExpired {
		tenantUUID := mustOrDeterministicUUID(in.TenantID)
		sched := now
		if !in.SendAt.IsZero() && in.SendAt.UTC().After(now) {
			sched = in.SendAt.UTC()
		}
		for _, d := range deliveries {
			delUUID := mustOrDeterministicUUID(d.ID)
			if err := s.queue.EnqueueWithPriority(ctx, tenantUUID, delUUID, lane, priority, sched); err != nil {
				return SubmitResult{}, err
			}
		}
	}

	return SubmitResult{Request: reqRec, Deliveries: deliveries, Replayed: false}, nil
}

// ProcessQueueBatch claims up to `limit` ready jobs on `lane`, executes
// pre-send guards (expiry, suppression, preference), renders, routes across
// providers with circuit breakers, and erases OTP secrets immediately on
// terminal states.
func (s *Service) ProcessQueueBatch(ctx context.Context, lane string, limit int, leaseDuration time.Duration) (int, error) {
	now := s.now().UTC()
	jobs, err := s.queue.ClaimBatch(ctx, lane, limit, leaseDuration, now)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, job := range jobs {
		if err := s.processClaimedJob(ctx, job); err != nil {
			continue
		}
		processed++
	}
	return processed, nil
}

func (s *Service) processClaimedJob(ctx context.Context, job queue.Job) error {
	now := s.now().UTC()
	tenantID := job.TenantID.String()
	deliveryID := job.DeliveryID.String()

	del, payload, err := s.store.GetDelivery(ctx, tenantID, deliveryID)
	if err != nil {
		_ = s.queue.Fail(ctx, job.ID, job.LeaseToken, "delivery not found", now)
		return nil
	}
	tmpl, err := s.store.GetTemplate(ctx, tenantID, del.TemplateID)
	if err != nil {
		_ = s.queue.Fail(ctx, job.ID, job.LeaseToken, "template not found", now)
		return nil
	}
	tv, err := s.store.GetTemplateVersion(ctx, tenantID, del.TemplateVersionID)
	if err != nil {
		_ = s.queue.Fail(ctx, job.ID, job.LeaseToken, "template version not found", now)
		return nil
	}
	cat, err := s.store.GetCategory(ctx, tenantID, tmpl.CategoryID)
	if err != nil {
		_ = s.queue.Fail(ctx, job.ID, job.LeaseToken, "category not found", now)
		return nil
	}

	// 1. Expiry guard (never send stale OTP / expired messages!)
	if !del.ExpiresAt.IsZero() && !now.Before(del.ExpiresAt) {
		del.Status = "EXPIRED"
		del.CompletedAt = now
		del.UpdatedAt = now
		ev := s.buildDeliveryOutbox(*del, tmpl.TemplateKey, tv.SchemaMajor, "EXPIRED_BEFORE_SEND", now)
		if err := s.store.UpdateDeliveryOutcome(ctx, *del, true, now, nil, []OutboxMessage{ev}); err != nil {
			return err
		}
		return s.queue.Complete(ctx, job.ID, job.LeaseToken, now)
	}

	// 2. Suppression & recipient preference guards
	suppressed, err := s.store.IsSuppressed(ctx, tenantID, del.Channel, del.AddressHash, now)
	if err != nil {
		return err
	}
	if !suppressed && cat.UserUnsubscribable && del.RecipientSubjectRef != "" {
		prefs, _ := s.store.ListPreferences(ctx, tenantID, del.RecipientSubjectRef)
		for _, p := range prefs {
			if p.CategoryID == cat.ID && p.Channel == del.Channel && !p.Enabled {
				suppressed = true
				break
			}
		}
	}
	if suppressed {
		del.Status = "SUPPRESSED"
		del.CompletedAt = now
		del.UpdatedAt = now
		ev := s.buildDeliveryOutbox(*del, tmpl.TemplateKey, tv.SchemaMajor, "RECIPIENT_SUPPRESSED_OR_OPTED_OUT", now)
		if err := s.store.UpdateDeliveryOutcome(ctx, *del, true, now, nil, []OutboxMessage{ev}); err != nil {
			return err
		}
		return s.queue.Complete(ctx, job.ID, job.LeaseToken, now)
	}

	if payload == nil {
		del.Status = "FAILED"
		del.CompletedAt = now
		del.UpdatedAt = now
		ev := s.buildDeliveryOutbox(*del, tmpl.TemplateKey, tv.SchemaMajor, "PAYLOAD_MISSING", now)
		_ = s.store.UpdateDeliveryOutcome(ctx, *del, true, now, nil, []OutboxMessage{ev})
		return s.queue.Fail(ctx, job.ID, job.LeaseToken, "payload missing", now)
	}

	// 3. Decrypt address + data + secrets in RAM
	addrBytes, err := s.crypto.Decrypt(tenantID, del.ID, payload.KeyRef, payload.EncryptedAddress)
	if err != nil {
		return err
	}
	var cleanMap, secretMap map[string]any
	if len(payload.EncryptedData) > 0 {
		plainData, derr := s.crypto.Decrypt(tenantID, del.ID, payload.KeyRef, payload.EncryptedData)
		if derr != nil {
			return derr
		}
		_ = json.Unmarshal(plainData, &cleanMap)
	}
	if len(payload.EncryptedSecrets) > 0 {
		plainSec, serr := s.crypto.Decrypt(tenantID, del.ID, payload.KeyRef, payload.EncryptedSecrets)
		if serr != nil {
			return serr
		}
		_ = json.Unmarshal(plainSec, &secretMap)
	}
	fullData := schema.MergeDataAndSecrets(cleanMap, secretMap)

	var variant *TemplateVariant
	for i := range tv.Variants {
		if tv.Variants[i].ID == del.VariantID {
			variant = &tv.Variants[i]
			break
		}
	}
	if variant == nil {
		del.Status = "FAILED"
		del.CompletedAt = now
		del.UpdatedAt = now
		_ = s.store.UpdateDeliveryOutcome(ctx, *del, true, now, nil, nil)
		return s.queue.Fail(ctx, job.ID, job.LeaseToken, "variant missing", now)
	}

	renderedMsg := provider.RenderedMessage{
		TenantID:            tenantID,
		DeliveryID:          del.ID,
		Channel:             del.Channel,
		CategoryKey:         cat.CategoryKey,
		RecipientSubjectRef: del.RecipientSubjectRef,
		RecipientAddress:    string(addrBytes),
		CorrelationID:       del.CorrelationID,
	}
	if cat.UserUnsubscribable && del.Channel == "EMAIL" {
		renderedMsg.UnsubscribeURL = fmt.Sprintf("https://notifications.bpmp.local/v1/unsubscribe/%s", del.ID)
	}

	if variant.VariantKind == "PROVIDER_TEMPLATE" {
		renderedMsg.ProviderTemplateID = variant.ProviderTemplateID
		renderedMsg.TemplateParams = fullData
	} else {
		compiledSchema, serr := schema.Compile(tv.VariableSchemaJSON)
		if serr != nil {
			return serr
		}
		layoutHTML := s.resolveLayoutHTML(ctx, tenantID, tv.LayoutVersionID, variant.Channel)
		compiledVar, cerr := s.renderer.CompilePublished(renderer.VariantSource{
			TemplateVersionID:  tv.ID,
			VariantID:          variant.ID,
			Syntax:             tv.Syntax,
			Channel:            variant.Channel,
			Locale:             variant.Locale,
			SubjectTmpl:        variant.SubjectTmpl,
			HTMLTmpl:           variant.HTMLTmpl,
			TextTmpl:           variant.TextTmpl,
			LayoutHTMLTmpl:     layoutHTML,
			DeclaredDataFields: compiledSchema.FieldSpecs(),
		})
		if cerr != nil {
			return cerr
		}
		rctx := defaultRenderContext(fullData, variant.Locale)
		rctx.System["correlationId"] = del.CorrelationID
		if renderedMsg.UnsubscribeURL != "" {
			rctx.System["unsubscribeUrl"] = renderedMsg.UnsubscribeURL
		}
		out, rerr := s.renderer.Render(compiledVar, rctx)
		if rerr != nil {
			del.Status = "REJECTED"
			del.CompletedAt = now
			del.UpdatedAt = now
			ev := s.buildDeliveryOutbox(*del, tmpl.TemplateKey, tv.SchemaMajor, "RENDER_REJECTED", now)
			_ = s.store.UpdateDeliveryOutcome(ctx, *del, true, now, nil, []OutboxMessage{ev})
			return s.queue.Fail(ctx, job.ID, job.LeaseToken, rerr.Error(), now)
		}
		renderedMsg.Subject = out.Subject
		renderedMsg.HTMLBody = out.HTML
		renderedMsg.TextBody = out.Text
	}

	// 4. Resolve routing tiers and dispatch through provider Router
	tiers, err := s.resolveRouteTiers(ctx, tenantID, del.ApplicationID, cat.ID, del.Channel)
	if err != nil || len(tiers) == 0 {
		nextAt := now.Add(s.retryBaseBackoff)
		del.Status = "DEFERRED"
		del.NextAttemptAt = nextAt
		del.UpdatedAt = now
		_ = s.store.UpdateDeliveryOutcome(ctx, *del, false, time.Time{}, nil, nil)
		return s.queue.Snooze(ctx, job.ID, job.LeaseToken, nextAt, "no healthy route configured", now)
	}

	execRes := s.router.Dispatch(ctx, tiers, provider.AmbiguousPolicy(cat.AmbiguousOutcomePolicy), hashSeed(del.ID, job.Attempts), renderedMsg)
	finished := s.now().UTC()
	var attemptRecords []DeliveryAttempt
	for _, a := range execRes.Attempts {
		attemptRecords = append(attemptRecords, DeliveryAttempt{
			TenantID:           tenantID,
			ID:                 s.newID(),
			DeliveryID:         del.ID,
			ProviderID:         a.ProviderID,
			ProviderRevisionID: a.ProviderRevisionID,
			AttemptNo:          del.AttemptCount + a.AttemptNo,
			OutcomeClass:       string(a.Result.Outcome),
			ProviderStatusCode: a.Result.ProviderStatusCode,
			ProviderMessageID:  a.Result.ProviderMessageID,
			ErrorCode:          a.Result.ErrorCode,
			LatencyMS:          int(a.Result.Latency.Milliseconds()),
			StartedAt:          now,
			FinishedAt:         finished,
		})
	}
	del.AttemptCount += len(attemptRecords)
	del.SelectedProviderID = execRes.SelectedProviderID
	del.UpdatedAt = finished

	switch execRes.Outcome {
	case provider.DispatchSent:
		del.Status = "SENT"
		del.ProviderMessageID = execRes.ProviderMessageID
		del.CompletedAt = finished
		ev := s.buildDeliveryOutbox(*del, tmpl.TemplateKey, tv.SchemaMajor, "", finished)
		// Immediately wipe encrypted_secrets upon terminal SENT!
		if err := s.store.UpdateDeliveryOutcome(ctx, *del, true, finished, attemptRecords, []OutboxMessage{ev}); err != nil {
			return err
		}
		return s.queue.Complete(ctx, job.ID, job.LeaseToken, finished)

	case provider.DispatchFailedPermanent, provider.DispatchFailedAmbiguous:
		errCode := "PROVIDER_PERMANENT_FAILURE"
		if execRes.Outcome == provider.DispatchFailedAmbiguous {
			errCode = "AMBIGUOUS_NO_RETRY"
		}
		del.Status = "FAILED"
		del.CompletedAt = finished
		ev := s.buildDeliveryOutbox(*del, tmpl.TemplateKey, tv.SchemaMajor, errCode, finished)
		// Immediately wipe encrypted_secrets upon terminal FAILED!
		if err := s.store.UpdateDeliveryOutcome(ctx, *del, true, finished, attemptRecords, []OutboxMessage{ev}); err != nil {
			return err
		}
		return s.queue.Fail(ctx, job.ID, job.LeaseToken, errCode, finished)

	default:
		// DispatchDeferred, DispatchReconcileRequired, DispatchFailedRetryable
		if execRes.Outcome == provider.DispatchFailedRetryable && job.Attempts >= s.maxRetryAttempts {
			del.Status = "FAILED"
			del.CompletedAt = finished
			ev := s.buildDeliveryOutbox(*del, tmpl.TemplateKey, tv.SchemaMajor, "RETRY_BUDGET_EXHAUSTED", finished)
			if err := s.store.UpdateDeliveryOutcome(ctx, *del, true, finished, attemptRecords, []OutboxMessage{ev}); err != nil {
				return err
			}
			return s.queue.Fail(ctx, job.ID, job.LeaseToken, "retry budget exhausted", finished)
		}
		backoff := s.retryBaseBackoff * time.Duration(1<<minInt(job.Attempts-1, 4))
		nextAt := finished.Add(backoff)
		if !del.ExpiresAt.IsZero() && nextAt.After(del.ExpiresAt) {
			nextAt = del.ExpiresAt
		}
		del.Status = "DEFERRED"
		del.NextAttemptAt = nextAt
		ev := s.buildDeliveryOutbox(*del, tmpl.TemplateKey, tv.SchemaMajor, string(execRes.Outcome), finished)
		if err := s.store.UpdateDeliveryOutcome(ctx, *del, false, time.Time{}, attemptRecords, []OutboxMessage{ev}); err != nil {
			return err
		}
		return s.queue.Snooze(ctx, job.ID, job.LeaseToken, nextAt, string(execRes.Outcome), finished)
	}
}

func (s *Service) resolveRouteTiers(ctx context.Context, tenantID, appID, categoryID, channel string) ([]provider.RouteTier, error) {
	profiles, err := s.store.ListProviderProfiles(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	profMap := make(map[string]ProviderProfile, len(profiles))
	for _, p := range profiles {
		if p.OperationalState == "ACTIVE" && p.ActiveRevision != nil {
			profMap[p.ID] = p
		}
	}
	policies, err := s.store.ListRoutingPolicies(ctx, tenantID, channel)
	if err != nil {
		return nil, err
	}
	// Pick most specific matching policy: (app, cat, chan) > (*, cat, chan) > (*, *, chan)
	var chosen *RoutingPolicy
	bestScore := -1
	for i := range policies {
		pol := policies[i]
		if pol.Status != "ACTIVE" {
			continue
		}
		if pol.ApplicationID != "" && pol.ApplicationID != appID {
			continue
		}
		if pol.CategoryID != "" && pol.CategoryID != categoryID {
			continue
		}
		score := 0
		if pol.CategoryID != "" {
			score += 2
		}
		if pol.ApplicationID != "" {
			score += 4
		}
		if score > bestScore {
			cp := pol
			chosen = &cp
			bestScore = score
		}
	}

	if chosen != nil && len(chosen.Tiers) > 0 {
		var tiers []provider.RouteTier
		for _, tierWeights := range chosen.Tiers {
			var cands []provider.RouteCandidate
			for _, w := range tierWeights {
				p, ok := profMap[w.ProviderID]
				if !ok {
					continue
				}
				cands = append(cands, toRouteCandidate(p, w.Weight))
			}
			if len(cands) > 0 {
				tiers = append(tiers, provider.RouteTier{Candidates: cands})
			}
		}
		return tiers, nil
	}

	// Default fallback: any active provider profile matching the channel.
	var fallback []provider.RouteCandidate
	for _, p := range profiles {
		if p.Channel == channel && p.OperationalState == "ACTIVE" && p.ActiveRevision != nil {
			fallback = append(fallback, toRouteCandidate(p, 1))
		}
	}
	if len(fallback) == 0 {
		return nil, nil
	}
	return []provider.RouteTier{{Candidates: fallback}}, nil
}

func toRouteCandidate(p ProviderProfile, weight uint32) provider.RouteCandidate {
	rev := p.ActiveRevision
	return provider.RouteCandidate{
		ProviderID: p.ID,
		Weight:     weight,
		Revision: provider.ProviderRevision{
			TenantID:       p.TenantID,
			ProviderID:     p.ID,
			ProviderKey:    p.ProviderKey,
			RevisionID:     rev.ID,
			Revision:       rev.Revision,
			Channel:        p.Channel,
			DriverKind:     p.DriverKind,
			PresetKey:      p.PresetKey,
			ConfigJSON:     rev.ConfigJSON,
			CredentialsRef: rev.CredentialsRef,
			SenderIdentity: rev.SenderIdentity,
		},
	}
}

// --- Provider Health, Breaker Overrides, Resend, Inbox & Preferences ---

type ProviderHealthView struct {
	Profile          ProviderProfile          `json:"profile"`
	Breaker          provider.BreakerSnapshot `json:"breaker"`
	Sent24h          uint64                   `json:"sent_24h"`
	FallbackProvider string                   `json:"fallback_provider,omitempty"`
}

func (s *Service) RegisterProvider(ctx context.Context, p ProviderProfile, cfgJSON []byte, credentialsRef string) (ProviderProfile, error) {
	if p.TenantID == "" || p.ProviderKey == "" || p.Channel == "" || p.DriverKind == "" {
		return ProviderProfile{}, fmt.Errorf("%w: tenant_id, provider_key, channel, and driver_kind are required", ErrInvalid)
	}
	now := s.now().UTC()
	if p.ID == "" {
		p.ID = s.newID()
	}
	p.Channel = strings.ToUpper(strings.TrimSpace(p.Channel))
	if p.Name == "" {
		p.Name = p.ProviderKey
	}
	if p.OperationalState == "" {
		p.OperationalState = "ACTIVE"
	}
	p.ActiveOrdinal = 1
	p.CreatedAt = now
	p.UpdatedAt = now
	if len(cfgJSON) == 0 {
		cfgJSON = []byte(`{}`)
	}
	p.ActiveRevision = &ProviderRevision{
		TenantID:       p.TenantID,
		ID:             s.newID(),
		ProviderID:     p.ID,
		Revision:       1,
		ConfigJSON:     append([]byte(nil), cfgJSON...),
		CredentialsRef: credentialsRef,
		ConfigHash:     hashBytes(cfgJSON),
		Status:         "PUBLISHED",
		PublishedAt:    now,
	}
	if err := s.store.SaveProviderProfile(ctx, p); err != nil {
		return ProviderProfile{}, err
	}
	return p, nil
}

func (s *Service) ListProviderHealth(ctx context.Context, tenantID string) ([]ProviderHealthView, error) {
	profiles, err := s.store.ListProviderProfiles(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	policies, _ := s.store.ListRoutingPolicies(ctx, tenantID, "")
	profKeyByID := make(map[string]string, len(profiles))
	for _, p := range profiles {
		profKeyByID[p.ID] = p.ProviderKey
	}
	fallbackByID := make(map[string]string)
	for _, pol := range policies {
		if pol.Status != "ACTIVE" || len(pol.Tiers) < 2 {
			continue
		}
		var fbKey string
		for _, w := range pol.Tiers[1] {
			if k, ok := profKeyByID[w.ProviderID]; ok {
				fbKey = k
				break
			}
		}
		if fbKey != "" {
			for _, w := range pol.Tiers[0] {
				fallbackByID[w.ProviderID] = fbKey
			}
		}
	}
	now := s.now().UTC()
	since24h := now.Add(-24 * time.Hour)
	out := make([]ProviderHealthView, 0, len(profiles))
	for _, p := range profiles {
		snap := s.router.Breakers().Snapshot(tenantID, p.ID, now)
		sent24h, _ := s.store.CountProviderDeliveriesSince(ctx, tenantID, p.ID, since24h)
		out = append(out, ProviderHealthView{
			Profile:          p,
			Breaker:          snap,
			Sent24h:          sent24h,
			FallbackProvider: fallbackByID[p.ID],
		})
	}
	return out, nil
}

type SetOverrideInput struct {
	TenantID       string
	ActorID        string
	CommandID      string
	IdempotencyKey string
	ProviderID     string
	Mode           provider.OverrideMode
	Reason         string
	TTL            time.Duration
}

func (s *Service) SetBreakerOverride(ctx context.Context, in SetOverrideInput) (ProviderHealthView, error) {
	if in.TenantID == "" || in.ProviderID == "" || strings.TrimSpace(in.Reason) == "" || in.TTL <= 0 {
		return ProviderHealthView{}, fmt.Errorf("%w: tenant_id, provider_id, reason, and positive ttl are required", ErrInvalid)
	}
	prof, err := s.store.GetProviderProfile(ctx, in.TenantID, in.ProviderID)
	if err != nil {
		return ProviderHealthView{}, err
	}
	now := s.now().UTC()
	ordinal := uint64(now.UnixNano())
	snap, err := s.router.Breakers().SetOverride(in.TenantID, in.ProviderID, in.Mode, in.Reason, in.TTL, ordinal, now)
	if err != nil {
		return ProviderHealthView{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	actor := nonEmpty(in.ActorID, "system")
	ov := BreakerOverrideRecord{
		TenantID:     in.TenantID,
		ID:           s.newID(),
		ProviderID:   in.ProviderID,
		OverrideMode: string(in.Mode),
		Reason:       in.Reason,
		Ordinal:      ordinal,
		ExpiresAt:    now.Add(in.TTL),
		Status:       "ACTIVE",
		CreatedAt:    now,
		CreatedBy:    actor,
	}
	if err := s.store.SaveBreakerOverride(ctx, ov); err != nil {
		return ProviderHealthView{}, err
	}
	invPayload, _ := json.Marshal(map[string]any{
		"schema_version":       1,
		"event_id":             s.newID(),
		"tenant_id":            in.TenantID,
		"artifact_kind":        "BREAKER_OVERRIDE",
		"artifact_id":          in.ProviderID,
		"ordinal":              ordinal,
		"override_mode":        string(in.Mode),
		"expires_at_epoch_ms":  ov.ExpiresAt.UnixMilli(),
		"occurred_at_epoch_ms": now.UnixMilli(),
	})
	_ = s.store.AppendOutbox(ctx, OutboxMessage{
		TenantID: in.TenantID, ID: s.newID(), EventID: s.newID(),
		AggregateID: in.ProviderID, AggregateVersion: ordinal,
		EventType: "notification.invalidation", PayloadJSON: invPayload, CreatedAt: now,
	})
	_ = s.store.AppendAudit(ctx, AuditRecord{
		TenantID: in.TenantID, ID: s.newID(), ActorRef: actorUUIDOrZero(actor),
		Action: "BREAKER_OVERRIDE_SET", ResourceRef: "provider:" + in.ProviderID,
		CommandID: in.CommandID, AfterDigest: string(in.Mode), OccurredAt: now,
	})
	return ProviderHealthView{Profile: *prof, Breaker: snap}, nil
}

func (s *Service) QueryDeliveries(ctx context.Context, q DeliveryQuery) ([]Delivery, time.Time, string, error) {
	if q.TenantID == "" {
		return nil, time.Time{}, "", fmt.Errorf("%w: tenant_id is required", ErrInvalid)
	}
	return s.store.QueryDeliveries(ctx, q)
}

type ResendInput struct {
	TenantID       string
	ActorID        string
	CommandID      string
	IdempotencyKey string
	DeliveryID     string
}

func (s *Service) ResendDelivery(ctx context.Context, in ResendInput) (Delivery, bool, error) {
	if in.TenantID == "" || in.DeliveryID == "" {
		return Delivery{}, false, fmt.Errorf("%w: tenant_id and delivery_id are required", ErrInvalid)
	}
	digest := hashJSON(map[string]any{"op": "ResendDelivery", "delivery_id": in.DeliveryID})
	receiptKey := commandKey(in.CommandID, in.IdempotencyKey)
	if receiptKey != "" {
		if rec, ok, err := s.store.GetCommandReceipt(ctx, in.TenantID, receiptKey); err != nil {
			return Delivery{}, false, err
		} else if ok {
			if rec.IntentDigest != digest {
				return Delivery{}, false, ErrConflict
			}
			var prev Delivery
			_ = json.Unmarshal(rec.ResultJSON, &prev)
			return prev, true, nil
		}
	}

	orig, origPayload, err := s.store.GetDelivery(ctx, in.TenantID, in.DeliveryID)
	if err != nil {
		return Delivery{}, false, err
	}
	tv, err := s.store.GetTemplateVersion(ctx, in.TenantID, orig.TemplateVersionID)
	if err != nil {
		return Delivery{}, false, err
	}
	compiledSchema, err := schema.Compile(tv.VariableSchemaJSON)
	if err != nil {
		return Delivery{}, false, err
	}
	// Block resend if template declares secret variables (ADR-013 §5.1 / §12)!
	if compiledSchema.HasSecrets() {
		return Delivery{}, false, ErrSecretResendForbidden
	}
	now := s.now().UTC()
	if origPayload == nil || !now.Before(origPayload.ExpiresAt) || len(origPayload.EncryptedAddress) == 0 {
		return Delivery{}, false, fmt.Errorf("%w: original delivery payload has expired and cannot be resent", ErrInvalid)
	}

	plainAddr, err := s.crypto.Decrypt(in.TenantID, orig.ID, origPayload.KeyRef, origPayload.EncryptedAddress)
	if err != nil {
		return Delivery{}, false, err
	}
	var plainData []byte
	if len(origPayload.EncryptedData) > 0 {
		plainData, err = s.crypto.Decrypt(in.TenantID, orig.ID, origPayload.KeyRef, origPayload.EncryptedData)
		if err != nil {
			return Delivery{}, false, err
		}
	}

	newID := s.newID()
	encAddr, keyRef, err := s.crypto.Encrypt(in.TenantID, newID, plainAddr)
	if err != nil {
		return Delivery{}, false, err
	}
	encData, _, err := s.crypto.Encrypt(in.TenantID, newID, plainData)
	if err != nil {
		return Delivery{}, false, err
	}

	tmpl, err := s.store.GetTemplate(ctx, in.TenantID, orig.TemplateID)
	if err != nil {
		return Delivery{}, false, err
	}
	resent := Delivery{
		TenantID:            in.TenantID,
		ID:                  newID,
		RequestID:           orig.RequestID,
		ApplicationID:       orig.ApplicationID,
		TemplateID:          orig.TemplateID,
		TemplateVersionID:   orig.TemplateVersionID,
		VariantID:           orig.VariantID,
		ReleaseOrdinal:      orig.ReleaseOrdinal,
		RecipientSubjectRef: orig.RecipientSubjectRef,
		AddressHash:         orig.AddressHash,
		MaskedAddress:       orig.MaskedAddress,
		Channel:             orig.Channel,
		Lane:                orig.Lane,
		Status:              "QUEUED",
		ContentHash:         orig.ContentHash,
		ResendOfDeliveryID:  orig.ID,
		CorrelationID:       orig.CorrelationID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	newPayload := DeliveryPayload{
		TenantID:         in.TenantID,
		ID:               s.newID(),
		DeliveryID:       newID,
		EncryptedAddress: encAddr,
		EncryptedData:    encData,
		KeyRef:           keyRef,
		ExpiresAt:        origPayload.ExpiresAt,
		CreatedAt:        now,
	}
	ev := s.buildDeliveryOutbox(resent, tmpl.TemplateKey, tv.SchemaMajor, "", now)
	if err := s.store.CreateRequestWithDeliveries(ctx, NotificationRequestRecord{}, []Delivery{resent}, []DeliveryPayload{newPayload}, []OutboxMessage{ev}); err != nil {
		return Delivery{}, false, err
	}
	if err := s.queue.Enqueue(ctx, mustOrDeterministicUUID(in.TenantID), mustOrDeterministicUUID(newID), resent.Lane, now); err != nil {
		return Delivery{}, false, err
	}

	actor := nonEmpty(in.ActorID, "system")
	if receiptKey != "" {
		resBytes, _ := json.Marshal(resent)
		_ = s.store.SaveCommandReceipt(ctx, CommandReceipt{
			TenantID: in.TenantID, ID: s.newID(), CommandID: receiptKey,
			IntentDigest: digest, ActorRef: actorUUIDOrZero(actor),
			AggregateID: resent.ID, Status: "COMPLETED", ResultJSON: resBytes, CreatedAt: now,
		})
	}
	_ = s.store.AppendAudit(ctx, AuditRecord{
		TenantID: in.TenantID, ID: s.newID(), ActorRef: actorUUIDOrZero(actor),
		Action: "DELIVERY_RESENT", ResourceRef: "delivery:" + resent.ID,
		CommandID: in.CommandID, BeforeDigest: orig.ID, AfterDigest: resent.ID, OccurredAt: now,
	})
	return resent, false, nil
}

func (s *Service) ListInAppMessages(ctx context.Context, tenantID, subjectRef string, unreadOnly bool, pageSize int, pageToken string) ([]InAppMessage, string, uint32, error) {
	if tenantID == "" || subjectRef == "" {
		return nil, "", 0, fmt.Errorf("%w: tenant_id and authenticated subject_ref are required", ErrInvalid)
	}
	return s.store.ListInAppMessages(ctx, tenantID, subjectRef, unreadOnly, pageSize, pageToken)
}

func (s *Service) MarkInAppMessagesRead(ctx context.Context, tenantID, subjectRef string, ids []string) (uint32, error) {
	if tenantID == "" || subjectRef == "" {
		return 0, fmt.Errorf("%w: tenant_id and subject_ref are required", ErrInvalid)
	}
	return s.store.MarkInAppMessagesRead(ctx, tenantID, subjectRef, ids, s.now().UTC())
}

func (s *Service) GetMyPreferences(ctx context.Context, tenantID, subjectRef string) ([]Preference, error) {
	cats, err := s.store.ListCategories(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	saved, err := s.store.ListPreferences(ctx, tenantID, subjectRef)
	if err != nil {
		return nil, err
	}
	savedMap := make(map[string]Preference, len(saved))
	for _, p := range saved {
		savedMap[p.CategoryID+":"+p.Channel] = p
	}
	var out []Preference
	for _, cat := range cats {
		for _, ch := range []string{"EMAIL", "SMS", "PUSH", "IN_APP"} {
			if p, ok := savedMap[cat.ID+":"+ch]; ok {
				p.CategoryKey = cat.CategoryKey
				p.UserUnsubscribable = cat.UserUnsubscribable
				if !cat.UserUnsubscribable {
					p.Enabled = true
				}
				out = append(out, p)
			} else {
				out = append(out, Preference{
					TenantID:           tenantID,
					SubjectRef:         subjectRef,
					CategoryID:         cat.ID,
					CategoryKey:        cat.CategoryKey,
					Channel:            ch,
					Enabled:            true,
					UserUnsubscribable: cat.UserUnsubscribable,
				})
			}
		}
	}
	return out, nil
}

func (s *Service) UpdateMyPreferences(ctx context.Context, tenantID, subjectRef string, updates []Preference) ([]Preference, error) {
	if tenantID == "" || subjectRef == "" {
		return nil, fmt.Errorf("%w: tenant_id and subject_ref are required", ErrInvalid)
	}
	for _, u := range updates {
		cat, err := s.store.GetCategoryByKey(ctx, tenantID, u.CategoryKey)
		if err != nil {
			return nil, err
		}
		if !cat.UserUnsubscribable && !u.Enabled {
			return nil, fmt.Errorf("%w: category %q is mandatory and cannot be disabled", ErrInvalid, cat.CategoryKey)
		}
		ch := strings.ToUpper(strings.TrimSpace(u.Channel))
		if err := s.store.UpsertPreference(ctx, Preference{
			TenantID:           tenantID,
			ID:                 s.newID(),
			SubjectRef:         subjectRef,
			CategoryID:         cat.ID,
			CategoryKey:        cat.CategoryKey,
			Channel:            ch,
			Enabled:            u.Enabled,
			UserUnsubscribable: cat.UserUnsubscribable,
		}); err != nil {
			return nil, err
		}
	}
	return s.GetMyPreferences(ctx, tenantID, subjectRef)
}

// --- Helpers ---

func (s *Service) resolveLayoutHTML(ctx context.Context, tenantID, layoutVersionID, channel string) string {
	if layoutVersionID == "" || channel != "EMAIL" {
		return ""
	}
	lv, err := s.store.GetLayoutVersion(ctx, tenantID, layoutVersionID)
	if err != nil {
		return ""
	}
	return lv.HTMLTemplate
}

func (s *Service) buildDeliveryOutbox(del Delivery, templateKey string, schemaMajor uint32, errorCode string, now time.Time) OutboxMessage {
	evID := s.newID()
	payload, _ := json.Marshal(map[string]any{
		"schema_version":       1,
		"event_id":             evID,
		"tenant_id":            del.TenantID,
		"request_id":           del.RequestID,
		"delivery_id":          del.ID,
		"application_id":       del.ApplicationID,
		"template_key":         templateKey,
		"schema_major":         schemaMajor,
		"template_version_id":  del.TemplateVersionID,
		"channel":              del.Channel,
		"lane":                 del.Lane,
		"status":               del.Status,
		"provider_id":          del.SelectedProviderID,
		"error_code":           errorCode,
		"correlation_id":       del.CorrelationID,
		"occurred_at_epoch_ms": now.UnixMilli(),
	})
	return OutboxMessage{
		TenantID:         del.TenantID,
		ID:               s.newID(),
		EventID:          evID,
		AggregateID:      del.ID,
		AggregateVersion: uint64(del.AttemptCount + 1),
		EventType:        "notification.delivery_event",
		PayloadJSON:      payload,
		CreatedAt:        now,
	}
}

func pickVariant(variants []TemplateVariant, channel, preferredLocale, defaultLocale string) (*TemplateVariant, error) {
	for i := range variants {
		if variants[i].Channel == channel && strings.EqualFold(variants[i].Locale, preferredLocale) {
			cp := variants[i]
			return &cp, nil
		}
	}
	for i := range variants {
		if variants[i].Channel == channel && strings.EqualFold(variants[i].Locale, defaultLocale) {
			cp := variants[i]
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("%w: no variant for channel %s (locale %s or default %s)", ErrInvalid, channel, preferredLocale, defaultLocale)
}

func defaultRenderContext(data map[string]any, locale string) renderer.RenderContext {
	return renderer.RenderContext{
		Data: data,
		Recipient: map[string]any{
			"displayName": "Valued User",
			"locale":      locale,
			"timezone":    "Asia/Ho_Chi_Minh",
		},
		Tenant: map[string]any{
			"name": "BPMP Platform",
		},
		Brand: map[string]any{
			"supportHotline": "1900-0000",
			"logoUrl":        "https://cdn.bpmp.example.com/logo.png",
		},
		System: map[string]any{
			"unsubscribeUrl": "https://notifications.bpmp.local/v1/unsubscribe/sample",
			"correlationId":  "preview-correlation",
			"sentYear":       2026,
		},
	}
}

func computeVariantHash(kind, channel, locale, subj, html, text, provID string, paramMap []byte) string {
	return hashJSON(map[string]any{
		"kind": kind, "channel": channel, "locale": locale,
		"subject": subj, "html": html, "text": text,
		"provider_template_id": provID, "params": string(paramMap),
	})
}

func computeVersionContentHash(syntax string, major uint32, layoutVerID string, varSchema []byte, variants []TemplateVariant, layoutHash string) string {
	vHashes := make([]string, len(variants))
	for i, v := range variants {
		vHashes[i] = v.Channel + ":" + v.Locale + ":" + v.VariantHash
	}
	sort.Strings(vHashes)
	return hashJSON(map[string]any{
		"syntax": syntax, "schema_major": major, "layout_version_id": layoutVerID,
		"layout_hash": layoutHash, "schema": hashBytes(varSchema), "variants": vHashes,
	})
}

func computeSubmitDigest(in SubmitInput) string {
	recs := make([]string, len(in.Recipients))
	for i, r := range in.Recipients {
		recs[i] = fmt.Sprintf("%s|%s|%s|%s", r.SubjectRef, strings.ToUpper(r.Channel), r.ExternalAddress, r.Locale)
	}
	var canonicalData any
	_ = json.Unmarshal(in.DataJSON, &canonicalData)
	return hashJSON(map[string]any{
		"tmpl": in.TemplateKey, "major": in.SchemaMajor, "pin": in.PinnedVersion,
		"cat": in.CategoryKey, "lane": in.LaneOverride, "locale": in.Locale,
		"data": canonicalData, "recipients": recs,
		"send_at": in.SendAt.UnixMilli(), "expires_at": in.ExpiresAt.UnixMilli(),
	})
}

func normalizeLane(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "transactional":
		return queue.LaneTransactional
	case "bulk":
		return queue.LaneBulk
	default:
		return queue.LaneNormal
	}
}

func commandKey(commandID, idempotencyKey string) string {
	if idempotencyKey != "" {
		return idempotencyKey
	}
	return commandID
}

func hashJSON(v any) string {
	b, _ := json.Marshal(v)
	return hashBytes(b)
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashSeed(id string, attempt int) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	_, _ = h.Write([]byte{byte(attempt)})
	return h.Sum64()
}

func mustOrDeterministicUUID(raw string) uuid.UUID {
	if parsed, err := uuid.Parse(raw); err == nil {
		return parsed
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(raw))
}

func actorUUIDOrZero(raw string) string {
	return mustOrDeterministicUUID(raw).String()
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func hasAnyCapability(granted []string, allowed ...string) bool {
	for _, want := range allowed {
		for _, got := range granted {
			if got == want {
				return true
			}
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
