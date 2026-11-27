package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/provider"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/renderer"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/schema"
)

// --- Process Context Catalog for Variable Mapping ---

type ProcessContextField struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	SourcePath string `json:"source_path"`
}

type ProcessContextSpec struct {
	ProcessKey string                `json:"process_key"`
	Fields     []ProcessContextField `json:"fields"`
}

type ProcessContextCatalog interface {
	ListProcesses(ctx context.Context, tenantID string) ([]ProcessContextSpec, error)
}

type StaticProcessContextCatalog struct {
	Processes []ProcessContextSpec
}

func (c *StaticProcessContextCatalog) ListProcesses(_ context.Context, _ string) ([]ProcessContextSpec, error) {
	out := make([]ProcessContextSpec, len(c.Processes))
	copy(out, c.Processes)
	return out, nil
}

func DefaultProcessContextCatalog() ProcessContextCatalog {
	return &StaticProcessContextCatalog{
		Processes: []ProcessContextSpec{
			{
				ProcessKey: "loan.approval",
				Fields: []ProcessContextField{
					{Name: "customerName", Type: "string", SourcePath: "vars.customerName"},
					{Name: "amount", Type: "money", SourcePath: "vars.amount"},
					{Name: "dueDate", Type: "date", SourcePath: "vars.dueDate"},
					{Name: "branchName", Type: "string", SourcePath: "vars.branchName"},
				},
			},
			{
				ProcessKey: "account.onboarding",
				Fields: []ProcessContextField{
					{Name: "customerName", Type: "string", SourcePath: "vars.customerName"},
					{Name: "accountNumber", Type: "string", SourcePath: "vars.accountNumber"},
				},
			},
		},
	}
}

func (s *Service) SetProcessCatalog(cat ProcessContextCatalog) {
	if cat != nil {
		s.processCatalog = cat
	}
}

func (s *Service) SetEnvironment(env string) {
	s.environment = strings.ToLower(strings.TrimSpace(env))
}

// --- Stateless Template Validation ---

type ValidateTemplateInput struct {
	Syntax             string
	VariableSchemaJSON []byte
	SubjectTmpl        string
	HTMLTmpl           string
	TextTmpl           string
}

func (s *Service) ValidateTemplateDraft(_ context.Context, in ValidateTemplateInput) ([]renderer.Diagnostic, bool, error) {
	syntax := nonEmpty(in.Syntax, renderer.SyntaxHandlebarsV1)
	var declared map[string]renderer.FieldSpec
	var diags []renderer.Diagnostic

	if len(strings.TrimSpace(string(in.VariableSchemaJSON))) > 0 {
		cs, err := schema.Compile(in.VariableSchemaJSON)
		if err != nil {
			diags = append(diags, renderer.Diagnostic{
				Code:     "INVALID_EXPRESSION_SYNTAX",
				Severity: "error",
				Target:   "html",
				Line:     1,
				Column:   1,
				Message:  "variable_schema error: " + err.Error(),
			})
			return diags, false, nil
		}
		declared = cs.FieldSpecs()
	} else {
		declared = map[string]renderer.FieldSpec{}
	}

	diags = append(diags, renderer.Diagnose(syntax, declared, in.SubjectTmpl, in.HTMLTmpl, in.TextTmpl)...)
	valid := true
	for _, d := range diags {
		if d.Severity == "error" {
			valid = false
			break
		}
	}
	return diags, valid, nil
}

// --- Template Batch Actions, Restore, Tags, Enriched List & Versions ---

type BatchTemplateActionsInput struct {
	TenantID       string
	ActorID        string
	ActorRole      string
	ClientIP       string
	CommandID      string
	IdempotencyKey string
	Action         string   // SET_STATUS | TAGS | DELETE
	IDs            []string // template IDs
	Status         string   // for SET_STATUS
	TagMode        string   // add | remove | replace
	Tags           []string
	Reason         string
}

type BatchTemplateActionResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (s *Service) BatchTemplateActions(ctx context.Context, in BatchTemplateActionsInput) ([]BatchTemplateActionResult, error) {
	action := strings.ToUpper(strings.TrimSpace(in.Action))
	if in.TenantID == "" || len(in.IDs) == 0 {
		return nil, fmt.Errorf("%w: tenant_id and ids are required", ErrInvalid)
	}
	switch action {
	case "SET_STATUS", "TAGS", "DELETE":
	default:
		return nil, fmt.Errorf("%w: unsupported batch action %q", ErrInvalid, in.Action)
	}
	if action == "SET_STATUS" && strings.TrimSpace(in.Status) == "" {
		return nil, fmt.Errorf("%w: status is required for SET_STATUS", ErrInvalid)
	}
	tagMode := strings.ToLower(strings.TrimSpace(in.TagMode))
	if action == "TAGS" && tagMode != "add" && tagMode != "remove" && tagMode != "replace" {
		return nil, fmt.Errorf("%w: tag_mode must be add, remove, or replace", ErrInvalid)
	}

	now := s.now().UTC()
	actor := nonEmpty(in.ActorID, "system")
	results := make([]BatchTemplateActionResult, 0, len(in.IDs))

	for _, id := range in.IDs {
		tmpl, err := s.store.GetTemplate(ctx, in.TenantID, id)
		if err != nil {
			results = append(results, BatchTemplateActionResult{ID: id, OK: false, Error: err.Error()})
			continue
		}
		switch action {
		case "SET_STATUS":
			tmpl.Status = strings.ToUpper(strings.TrimSpace(in.Status))
			tmpl.UpdatedAt = now
			tmpl.UpdatedBy = actor
			if err := s.store.UpdateTemplate(ctx, *tmpl); err != nil {
				results = append(results, BatchTemplateActionResult{ID: id, OK: false, Error: err.Error()})
				continue
			}
		case "TAGS":
			tmpl.Tags = applyTagMutation(tmpl.Tags, tagMode, in.Tags)
			tmpl.UpdatedAt = now
			tmpl.UpdatedBy = actor
			if err := s.store.UpdateTemplate(ctx, *tmpl); err != nil {
				results = append(results, BatchTemplateActionResult{ID: id, OK: false, Error: err.Error()})
				continue
			}
		case "DELETE":
			releases, err := s.store.ListTemplateReleases(ctx, in.TenantID, tmpl.ID)
			if err != nil {
				results = append(results, BatchTemplateActionResult{ID: id, OK: false, Error: err.Error()})
				continue
			}
			hasActive := false
			for _, rel := range releases {
				if rel.Status == "ACTIVE" {
					hasActive = true
					break
				}
			}
			if hasActive {
				results = append(results, BatchTemplateActionResult{
					ID:    id,
					OK:    false,
					Error: "cannot delete template with active release",
				})
				continue
			}
			tmpl.IsDeleted = true
			tmpl.DeletedAt = now
			tmpl.DeletedBy = actor
			tmpl.DeleteReason = nonEmpty(in.Reason, "batch soft delete")
			tmpl.UpdatedAt = now
			tmpl.UpdatedBy = actor
			if err := s.store.UpdateTemplate(ctx, *tmpl); err != nil {
				results = append(results, BatchTemplateActionResult{ID: id, OK: false, Error: err.Error()})
				continue
			}
		}
		results = append(results, BatchTemplateActionResult{ID: id, OK: true})
	}

	_ = s.store.AppendAudit(ctx, AuditRecord{
		TenantID:     in.TenantID,
		ID:           s.newID(),
		ActorRef:     actorUUIDOrZero(actor),
		ActorDisplay: actor,
		ActorRole:    in.ActorRole,
		ClientIP:     in.ClientIP,
		Action:       "TEMPLATE_BATCH_" + action,
		ResourceType: "TEMPLATE",
		ResourceRef:  fmt.Sprintf("templates:batch(%d)", len(in.IDs)),
		Severity:     "WARN",
		CommandID:    in.CommandID,
		Details:      map[string]any{"action": action, "count": len(in.IDs)},
		OccurredAt:   now,
	})
	return results, nil
}

func applyTagMutation(existing []string, mode string, tags []string) []string {
	clean := func(list []string) []string {
		seen := make(map[string]bool)
		var out []string
		for _, t := range list {
			trimmed := strings.TrimSpace(t)
			if trimmed != "" && !seen[trimmed] {
				seen[trimmed] = true
				out = append(out, trimmed)
			}
		}
		sort.Strings(out)
		return out
	}
	switch mode {
	case "replace":
		return clean(tags)
	case "add":
		return clean(append(append([]string(nil), existing...), tags...))
	case "remove":
		rm := make(map[string]bool, len(tags))
		for _, t := range tags {
			rm[strings.TrimSpace(t)] = true
		}
		var kept []string
		for _, t := range existing {
			if !rm[t] {
				kept = append(kept, t)
			}
		}
		return clean(kept)
	default:
		return clean(existing)
	}
}

func (s *Service) RestoreTemplate(ctx context.Context, tenantID, actorID, actorRole, clientIP, commandID, templateID string) (Template, error) {
	if tenantID == "" || templateID == "" {
		return Template{}, fmt.Errorf("%w: tenant_id and template_id are required", ErrInvalid)
	}
	tmpl, err := s.store.GetTemplateIncludingDeleted(ctx, tenantID, templateID)
	if err != nil {
		return Template{}, err
	}
	now := s.now().UTC()
	actor := nonEmpty(actorID, "system")
	tmpl.IsDeleted = false
	tmpl.DeletedAt = time.Time{}
	tmpl.DeletedBy = ""
	tmpl.DeleteReason = ""
	tmpl.UpdatedAt = now
	tmpl.UpdatedBy = actor
	if err := s.store.UpdateTemplate(ctx, *tmpl); err != nil {
		return Template{}, err
	}
	_ = s.store.AppendAudit(ctx, AuditRecord{
		TenantID:     tenantID,
		ID:           s.newID(),
		ActorRef:     actorUUIDOrZero(actor),
		ActorDisplay: actor,
		ActorRole:    actorRole,
		ClientIP:     clientIP,
		Action:       "TEMPLATE_RESTORED",
		ResourceType: "TEMPLATE",
		ResourceRef:  "template:" + tmpl.ID,
		Severity:     "INFO",
		CommandID:    commandID,
		OccurredAt:   now,
	})
	return *tmpl, nil
}

func (s *Service) ListTemplateTags(ctx context.Context, tenantID string) ([]string, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ErrInvalid)
	}
	return s.store.ListTemplateTags(ctx, tenantID)
}

func (s *Service) ListTemplateSummaries(ctx context.Context, filter TemplateListFilter) ([]Template, string, error) {
	if filter.TenantID == "" {
		return nil, "", fmt.Errorf("%w: tenant_id is required", ErrInvalid)
	}
	all, _, err := s.store.ListTemplates(ctx, filter.TenantID, filter.ApplicationID, 1000, "")
	if err != nil {
		return nil, "", err
	}
	now := s.now().UTC()
	since24h := now.Add(-24 * time.Hour)
	q := strings.ToLower(strings.TrimSpace(filter.Query))
	wantChan := strings.ToUpper(strings.TrimSpace(filter.Channel))
	wantTag := strings.TrimSpace(filter.Tag)
	wantStatus := strings.ToUpper(strings.TrimSpace(filter.Status))

	var matched []Template
	for _, t := range all {
		if wantStatus != "" && !strings.EqualFold(t.Status, wantStatus) {
			continue
		}
		if wantTag != "" {
			hasTag := false
			for _, tg := range t.Tags {
				if strings.EqualFold(tg, wantTag) {
					hasTag = true
					break
				}
			}
			if !hasTag {
				continue
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(t.TemplateKey), q) && !strings.Contains(strings.ToLower(t.Name), q) {
			continue
		}

		// Enrich channels & active_release & usage_24h
		versions, _ := s.store.ListTemplateVersions(ctx, filter.TenantID, t.ID)
		chanSet := make(map[string]bool)
		for _, v := range versions {
			for _, vr := range v.Variants {
				chanSet[vr.Channel] = true
			}
		}
		channels := make([]string, 0, len(chanSet))
		for ch := range chanSet {
			channels = append(channels, ch)
		}
		sort.Strings(channels)
		t.Channels = channels

		if wantChan != "" && !chanSet[wantChan] {
			continue
		}

		releases, _ := s.store.ListTemplateReleases(ctx, filter.TenantID, t.ID)
		for _, r := range releases {
			if r.Status == "ACTIVE" && r.Ordinal > t.ActiveRelease {
				t.ActiveRelease = r.Ordinal
			}
		}
		t.Usage24h, _ = s.store.CountTemplateDeliveriesSince(ctx, filter.TenantID, t.ID, since24h)
		if t.Tags == nil {
			t.Tags = []string{}
		}
		matched = append(matched, t)
	}

	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	offset := 0
	if filter.PageToken != "" {
		if parsed, err := strconv.Atoi(filter.PageToken); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	if offset >= len(matched) {
		return []Template{}, "", nil
	}
	end := offset + pageSize
	next := ""
	if end < len(matched) {
		next = strconv.Itoa(end)
	} else {
		end = len(matched)
	}
	return matched[offset:end], next, nil
}

func (s *Service) ListTemplateVersionsEnriched(ctx context.Context, tenantID, templateID string) ([]TemplateVersion, error) {
	versions, err := s.store.ListTemplateVersions(ctx, tenantID, templateID)
	if err != nil {
		return nil, err
	}
	releases, _ := s.store.ListTemplateReleases(ctx, tenantID, templateID)
	relByVer := make(map[string]uint64)
	for _, r := range releases {
		if r.Ordinal > relByVer[r.TemplateVersionID] {
			relByVer[r.TemplateVersionID] = r.Ordinal
		}
	}
	for i := range versions {
		versions[i].ReleaseOrdinal = relByVer[versions[i].ID]
	}
	return versions, nil
}

// --- Dry-Run / PlanNotification (ZERO DB writes, ZERO queue enqueue) ---

type NotificationPlan struct {
	TemplateVersionID    string                      `json:"template_version_id"`
	ReleaseOrdinal       uint64                      `json:"release_ordinal"`
	VariantID            string                      `json:"variant_id"`
	Channel              string                      `json:"channel"`
	Locale               string                      `json:"locale"`
	SchemaErrors         []string                    `json:"schema_errors"`
	QuotaRemainingMinute int                         `json:"quota_remaining_minute"`
	QuotaRemainingDay    int                         `json:"quota_remaining_day"`
	RoutingCandidates    []provider.PlannedCandidate `json:"routing_candidates"`
	SelectedProvider     string                      `json:"selected_provider"`
	FailoverReason       string                      `json:"failover_reason,omitempty"`
	Warnings             []string                    `json:"warnings"`
}

func (s *Service) PlanNotification(ctx context.Context, in SubmitInput) (NotificationPlan, error) {
	if in.TenantID == "" || in.TemplateKey == "" || in.SchemaMajor == 0 {
		return NotificationPlan{}, fmt.Errorf("%w: tenant_id, template_key, and schema_major are required", ErrInvalid)
	}
	tmpl, err := s.store.GetTemplateByKey(ctx, in.TenantID, in.TemplateKey)
	if err != nil {
		return NotificationPlan{}, err
	}
	var app *Application
	if in.ApplicationKey != "" {
		app, err = s.store.GetApplicationByKey(ctx, in.TenantID, in.ApplicationKey)
	} else {
		app, err = s.store.GetApplication(ctx, in.TenantID, tmpl.ApplicationID)
	}
	if err != nil {
		return NotificationPlan{}, err
	}
	if !MatchNamespace(app.AllowedNamespaces, tmpl.TemplateKey) {
		return NotificationPlan{}, fmt.Errorf("%w: application %q cannot send template %q", ErrForbidden, app.AppKey, tmpl.TemplateKey)
	}
	var cat *Category
	if in.CategoryKey != "" {
		cat, err = s.store.GetCategoryByKey(ctx, in.TenantID, in.CategoryKey)
	} else {
		cat, err = s.store.GetCategory(ctx, in.TenantID, tmpl.CategoryID)
	}
	if err != nil {
		return NotificationPlan{}, err
	}

	now := s.now().UTC()
	var tv *TemplateVersion
	var releaseOrdinal uint64
	if in.PinnedVersion > 0 {
		versions, err := s.store.ListTemplateVersions(ctx, in.TenantID, tmpl.ID)
		if err != nil {
			return NotificationPlan{}, err
		}
		for i := range versions {
			if versions[i].Version == in.PinnedVersion && versions[i].SchemaMajor == in.SchemaMajor {
				cp := versions[i]
				tv = &cp
				break
			}
		}
		if tv == nil {
			return NotificationPlan{}, ErrNotFound
		}
	} else {
		rel, err := s.store.GetActiveTemplateRelease(ctx, in.TenantID, tmpl.ID, in.SchemaMajor, now)
		if err != nil {
			return NotificationPlan{}, fmt.Errorf("%w: no active release for %s@%d", ErrNotFound, tmpl.TemplateKey, in.SchemaMajor)
		}
		releaseOrdinal = rel.Ordinal
		tv, err = s.store.GetTemplateVersion(ctx, in.TenantID, rel.TemplateVersionID)
		if err != nil {
			return NotificationPlan{}, err
		}
	}

	plan := NotificationPlan{
		TemplateVersionID:    tv.ID,
		ReleaseOrdinal:       releaseOrdinal,
		SchemaErrors:         []string{},
		QuotaRemainingMinute: -1,
		QuotaRemainingDay:    -1,
		RoutingCandidates:    []provider.PlannedCandidate{},
		Warnings:             []string{},
	}

	// Quota inspection (read-only)
	if app.QuotaConfig.MaxPerMinute > 0 {
		cnt, _ := s.store.CountApplicationRequestsSince(ctx, in.TenantID, app.ID, now.Add(-time.Minute))
		rem := app.QuotaConfig.MaxPerMinute - cnt
		if rem < 0 {
			rem = 0
		}
		plan.QuotaRemainingMinute = rem
		if rem == 0 {
			plan.Warnings = append(plan.Warnings, "application per-minute quota is exhausted")
		}
	}
	if app.QuotaConfig.MaxPerDay > 0 {
		cnt, _ := s.store.CountApplicationRequestsSince(ctx, in.TenantID, app.ID, now.Add(-24*time.Hour))
		rem := app.QuotaConfig.MaxPerDay - cnt
		if rem < 0 {
			rem = 0
		}
		plan.QuotaRemainingDay = rem
		if rem == 0 {
			plan.Warnings = append(plan.Warnings, "application daily quota is exhausted")
		}
	}

	// Schema validation (collect errors without failing hard)
	compiledSchema, err := schema.Compile(tv.VariableSchemaJSON)
	if err != nil {
		plan.SchemaErrors = append(plan.SchemaErrors, err.Error())
	} else if _, err := compiledSchema.ValidatePayload(in.DataJSON); err != nil {
		plan.SchemaErrors = append(plan.SchemaErrors, err.Error())
	}

	// Resolve target channel & locale
	ch := "EMAIL"
	preferredLocale := nonEmpty(in.Locale, tmpl.DefaultLocale)
	if len(in.Recipients) > 0 {
		if c := strings.ToUpper(strings.TrimSpace(in.Recipients[0].Channel)); c != "" {
			ch = c
		}
		if loc := strings.TrimSpace(in.Recipients[0].Locale); loc != "" {
			preferredLocale = loc
		}
	} else if len(tv.Variants) > 0 {
		ch = tv.Variants[0].Channel
	}

	variant, vErr := pickVariant(tv.Variants, ch, preferredLocale, tmpl.DefaultLocale)
	if vErr != nil {
		plan.Warnings = append(plan.Warnings, vErr.Error())
		plan.Channel = ch
		plan.Locale = preferredLocale
	} else {
		plan.VariantID = variant.ID
		plan.Channel = variant.Channel
		plan.Locale = variant.Locale
	}

	// Routing & breaker inspection (strictly read-only!)
	tiers, _ := s.resolveRouteTiers(ctx, in.TenantID, app.ID, cat.ID, plan.Channel)
	if len(tiers) == 0 {
		plan.FailoverReason = "no active provider route configured for channel " + plan.Channel
		plan.Warnings = append(plan.Warnings, plan.FailoverReason)
	} else {
		cands, selected, reason := s.router.PlanRoute(in.TenantID, tiers, now)
		if cands != nil {
			plan.RoutingCandidates = cands
		}
		plan.SelectedProvider = selected
		plan.FailoverReason = reason
	}

	return plan, nil
}

// --- Circuit Breaker Transitions, Probe & Simulate Failure ---

func (s *Service) ListBreakerTransitions(ctx context.Context, tenantID, providerID string, limit int) ([]BreakerTransition, error) {
	if tenantID == "" || providerID == "" {
		return nil, fmt.Errorf("%w: tenant_id and provider_id are required", ErrInvalid)
	}
	if _, err := s.store.GetProviderProfile(ctx, tenantID, providerID); err != nil {
		return nil, err
	}
	return s.store.ListBreakerTransitions(ctx, tenantID, providerID, limit)
}

func (s *Service) ProbeBreaker(ctx context.Context, tenantID, actorID, actorRole, clientIP, commandID, providerID, reason string) (ProviderHealthView, error) {
	if tenantID == "" || providerID == "" {
		return ProviderHealthView{}, fmt.Errorf("%w: tenant_id and provider_id are required", ErrInvalid)
	}
	prof, err := s.store.GetProviderProfile(ctx, tenantID, providerID)
	if err != nil {
		return ProviderHealthView{}, err
	}
	now := s.now().UTC()
	actor := nonEmpty(actorID, "system")
	snap, _ := s.router.Breakers().Probe(tenantID, providerID, nonEmpty(reason, "manual probe"), now)
	_ = s.store.AppendAudit(ctx, AuditRecord{
		TenantID:     tenantID,
		ID:           s.newID(),
		ActorRef:     actorUUIDOrZero(actor),
		ActorDisplay: actor,
		ActorRole:    actorRole,
		ClientIP:     clientIP,
		Action:       "BREAKER_PROBED",
		ResourceType: "PROVIDER",
		ResourceRef:  "provider:" + providerID,
		Severity:     "WARN",
		CommandID:    commandID,
		Details:      map[string]any{"reason": reason, "new_state": string(snap.BreakerState)},
		OccurredAt:   now,
	})
	sent24h, _ := s.store.CountProviderDeliveriesSince(ctx, tenantID, providerID, now.Add(-24*time.Hour))
	return ProviderHealthView{Profile: *prof, Breaker: snap, Sent24h: sent24h}, nil
}

type SimulateFailureInput struct {
	TenantID   string
	ActorID    string
	ActorRole  string
	ClientIP   string
	CommandID  string
	ProviderID string
	Count      int
	ErrorCode  string
}

func (s *Service) SimulateProviderFailure(ctx context.Context, in SimulateFailureInput) (ProviderHealthView, error) {
	if s.environment == "production" || s.environment == "prod" {
		return ProviderHealthView{}, fmt.Errorf("%w: simulate-failure is disabled in production", ErrForbidden)
	}
	if in.TenantID == "" || in.ProviderID == "" {
		return ProviderHealthView{}, fmt.Errorf("%w: tenant_id and provider_id are required", ErrInvalid)
	}
	prof, err := s.store.GetProviderProfile(ctx, in.TenantID, in.ProviderID)
	if err != nil {
		return ProviderHealthView{}, err
	}
	if !strings.EqualFold(prof.DriverKind, "mock") {
		return ProviderHealthView{}, fmt.Errorf("%w: simulate-failure is only permitted on mock driver providers (got %s)", ErrForbidden, prof.DriverKind)
	}
	count := in.Count
	if count <= 0 {
		count = 5
	}
	if count > 50 {
		count = 50
	}
	errCode := nonEmpty(in.ErrorCode, "SIMULATED_SPIKE")
	now := s.now().UTC()
	var snap provider.BreakerSnapshot
	for i := 0; i < count; i++ {
		snap, _ = s.router.Breakers().RecordOutcomeWithLatency(in.TenantID, in.ProviderID, provider.OutcomeRetryable, errCode, 100*time.Millisecond, now)
	}
	actor := nonEmpty(in.ActorID, "system")
	_ = s.store.AppendAudit(ctx, AuditRecord{
		TenantID:     in.TenantID,
		ID:           s.newID(),
		ActorRef:     actorUUIDOrZero(actor),
		ActorDisplay: actor,
		ActorRole:    in.ActorRole,
		ClientIP:     in.ClientIP,
		Action:       "BREAKER_FAILURE_SIMULATED",
		ResourceType: "PROVIDER",
		ResourceRef:  "provider:" + in.ProviderID,
		Severity:     "WARN",
		CommandID:    in.CommandID,
		Details:      map[string]any{"count": count, "error_code": errCode, "breaker_state": string(snap.BreakerState)},
		OccurredAt:   now,
	})
	sent24h, _ := s.store.CountProviderDeliveriesSince(ctx, in.TenantID, in.ProviderID, now.Add(-24*time.Hour))
	return ProviderHealthView{Profile: *prof, Breaker: snap, Sent24h: sent24h}, nil
}

// --- Delivery Detail with Trace ---

type DeliveryDetail struct {
	Delivery Delivery            `json:"delivery"`
	Attempts []DeliveryAttempt   `json:"attempts"`
	Events   []DeliveryEvent     `json:"events"`
	Trace    []DeliveryTraceStep `json:"trace"`
}

func (s *Service) GetDeliveryDetail(ctx context.Context, tenantID, deliveryID string) (DeliveryDetail, error) {
	del, _, err := s.store.GetDelivery(ctx, tenantID, deliveryID)
	if err != nil {
		return DeliveryDetail{}, err
	}
	events, _ := s.store.ListDeliveryEvents(ctx, tenantID, deliveryID)
	attempts, _ := s.store.ListDeliveryAttempts(ctx, tenantID, deliveryID)

	trace := make([]DeliveryTraceStep, 0, len(events)+len(attempts))
	for _, ev := range events {
		st := "OK"
		if ev.ToStatus == "FAILED" || ev.ToStatus == "REJECTED" || ev.ToStatus == "EXPIRED" {
			st = "ERROR"
		} else if ev.ToStatus == "DEFERRED" || ev.ToStatus == "SUPPRESSED" {
			st = "WARN"
		}
		trace = append(trace, DeliveryTraceStep{
			Step:       ev.EventType,
			Status:     st,
			LatencyMS:  0,
			Detail:     fmt.Sprintf("%s -> %s", ev.FromStatus, ev.ToStatus),
			OccurredAt: ev.OccurredAt,
		})
	}
	for _, a := range attempts {
		st := "OK"
		if a.OutcomeClass == "PERMANENT" || a.OutcomeClass == "AUTH_FAILED" {
			st = "ERROR"
		} else if a.OutcomeClass != "OK" {
			st = "WARN"
		}
		detail := fmt.Sprintf("provider=%s outcome=%s", a.ProviderID, a.OutcomeClass)
		if a.ErrorCode != "" {
			detail += " error=" + a.ErrorCode
		}
		trace = append(trace, DeliveryTraceStep{
			Step:       fmt.Sprintf("PROVIDER_ATTEMPT_%d", a.AttemptNo),
			Status:     st,
			LatencyMS:  a.LatencyMS,
			Detail:     detail,
			OccurredAt: a.FinishedAt,
		})
	}
	sort.Slice(trace, func(i, j int) bool {
		return trace[i].OccurredAt.Before(trace[j].OccurredAt)
	})
	if events == nil {
		events = []DeliveryEvent{}
	}
	if attempts == nil {
		attempts = []DeliveryAttempt{}
	}
	return DeliveryDetail{
		Delivery: *del,
		Attempts: attempts,
		Events:   events,
		Trace:    trace,
	}, nil
}

// --- Trigger Rules & Variable Mapping ---

type CreateTriggerRuleInput struct {
	TenantID     string
	ActorID      string
	RuleKey      string
	Name         string
	WorkflowType string
	NodeID       string
	TriggerKind  string
	TemplateID   string
	SchemaMajor  uint32
}

func (s *Service) CreateTriggerRule(ctx context.Context, in CreateTriggerRuleInput) (TriggerRule, error) {
	if in.TenantID == "" || in.RuleKey == "" || in.WorkflowType == "" || in.TemplateID == "" || in.SchemaMajor == 0 {
		return TriggerRule{}, fmt.Errorf("%w: tenant_id, rule_key, workflow_type, template_id, and schema_major are required", ErrInvalid)
	}
	if _, err := s.store.GetTemplate(ctx, in.TenantID, in.TemplateID); err != nil {
		return TriggerRule{}, err
	}
	now := s.now().UTC()
	actor := nonEmpty(in.ActorID, "system")
	rule := TriggerRule{
		TenantID:     in.TenantID,
		ID:           s.newID(),
		RuleKey:      in.RuleKey,
		Name:         nonEmpty(in.Name, in.RuleKey),
		WorkflowType: in.WorkflowType,
		NodeID:       in.NodeID,
		TriggerKind:  nonEmpty(in.TriggerKind, "STEP_COMPLETED"),
		TemplateID:   in.TemplateID,
		SchemaMajor:  in.SchemaMajor,
		Status:       "ACTIVE",
		CreatedAt:    now,
		UpdatedAt:    now,
		CreatedBy:    actor,
	}
	if err := s.store.SaveTriggerRule(ctx, rule); err != nil {
		return TriggerRule{}, err
	}
	return rule, nil
}

type SaveTriggerRuleRevisionInput struct {
	TenantID           string
	ActorID            string
	RuleID             string
	VersionSelector    string
	ConditionCEL       string
	RecipientSpecJSON  []byte
	DataMappingCEL     map[string]string
	ReferencedVars     []string
	DelaySeconds       int
	DedupWindowSeconds int
}

func (s *Service) SaveTriggerRuleRevision(ctx context.Context, in SaveTriggerRuleRevisionInput) (TriggerRuleRevision, error) {
	if in.TenantID == "" || in.RuleID == "" {
		return TriggerRuleRevision{}, fmt.Errorf("%w: tenant_id and rule_id are required", ErrInvalid)
	}
	if _, err := s.store.GetTriggerRule(ctx, in.TenantID, in.RuleID); err != nil {
		return TriggerRuleRevision{}, err
	}
	revs, err := s.store.ListTriggerRuleRevisions(ctx, in.TenantID, in.RuleID)
	if err != nil {
		return TriggerRuleRevision{}, err
	}
	var nextRev uint64 = 1
	for _, r := range revs {
		if r.Revision >= nextRev {
			nextRev = r.Revision + 1
		}
	}
	now := s.now().UTC()
	actor := nonEmpty(in.ActorID, "system")
	recSpec := in.RecipientSpecJSON
	if len(recSpec) == 0 {
		recSpec = []byte(`{}`)
	}
	mapping := in.DataMappingCEL
	if mapping == nil {
		mapping = map[string]string{}
	}
	refVars := in.ReferencedVars
	if len(refVars) == 0 {
		for _, expr := range mapping {
			if strings.HasPrefix(expr, "vars.") {
				refVars = append(refVars, strings.TrimPrefix(expr, "vars."))
			}
		}
		sort.Strings(refVars)
	}
	rev := TriggerRuleRevision{
		TenantID:           in.TenantID,
		ID:                 s.newID(),
		RuleID:             in.RuleID,
		Revision:           nextRev,
		VersionSelector:    nonEmpty(in.VersionSelector, "LATEST"),
		ConditionCEL:       in.ConditionCEL,
		RecipientSpecJSON:  recSpec,
		DataMappingCEL:     mapping,
		ReferencedVars:     refVars,
		DelaySeconds:       in.DelaySeconds,
		DedupWindowSeconds: in.DedupWindowSeconds,
		Digest:             hashJSON(mapping),
		Status:             "DRAFT",
		CreatedAt:          now,
		UpdatedAt:          now,
		CreatedBy:          actor,
	}
	if err := s.store.SaveTriggerRuleRevision(ctx, rev); err != nil {
		return TriggerRuleRevision{}, err
	}
	return rev, nil
}

func (s *Service) ActivateTriggerRuleRevision(ctx context.Context, tenantID, actorID, ruleID string, revision uint64) (TriggerRule, error) {
	rule, err := s.store.GetTriggerRule(ctx, tenantID, ruleID)
	if err != nil {
		return TriggerRule{}, err
	}
	revs, err := s.store.ListTriggerRuleRevisions(ctx, tenantID, ruleID)
	if err != nil {
		return TriggerRule{}, err
	}
	var target *TriggerRuleRevision
	for i := range revs {
		if revs[i].Revision == revision {
			cp := revs[i]
			target = &cp
			break
		}
	}
	if target == nil {
		return TriggerRule{}, ErrNotFound
	}
	now := s.now().UTC()
	target.Status = "PUBLISHED"
	target.PublishedAt = now
	target.UpdatedAt = now
	if err := s.store.SaveTriggerRuleRevision(ctx, *target); err != nil {
		return TriggerRule{}, err
	}
	rule.ActiveOrdinal++
	rule.ActiveRevision = target
	rule.UpdatedAt = now
	if err := s.store.SaveTriggerRule(ctx, *rule); err != nil {
		return TriggerRule{}, err
	}
	_ = s.store.AppendAudit(ctx, AuditRecord{
		TenantID:     tenantID,
		ID:           s.newID(),
		ActorRef:     actorUUIDOrZero(nonEmpty(actorID, "system")),
		ActorDisplay: nonEmpty(actorID, "system"),
		Action:       "TRIGGER_RULE_ACTIVATED",
		ResourceType: "TRIGGER_RULE",
		ResourceRef:  fmt.Sprintf("trigger_rule:%s#rev%d", rule.ID, revision),
		Severity:     "INFO",
		OccurredAt:   now,
	})
	return *rule, nil
}

type SimulateTriggerRuleResult struct {
	Matched    bool           `json:"matched"`
	MappedData map[string]any `json:"mapped_data"`
	Warnings   []string       `json:"warnings"`
}

func (s *Service) SimulateTriggerRule(ctx context.Context, tenantID, ruleID string, sampleCtxJSON []byte) (SimulateTriggerRuleResult, error) {
	rule, err := s.store.GetTriggerRule(ctx, tenantID, ruleID)
	if err != nil {
		return SimulateTriggerRuleResult{}, err
	}
	var sampleCtx map[string]any
	if len(strings.TrimSpace(string(sampleCtxJSON))) > 0 {
		if err := json.Unmarshal(sampleCtxJSON, &sampleCtx); err != nil {
			return SimulateTriggerRuleResult{}, fmt.Errorf("%w: invalid sample context JSON: %v", ErrInvalid, err)
		}
	}
	if sampleCtx == nil {
		sampleCtx = map[string]any{}
	}
	var rev *TriggerRuleRevision
	if rule.ActiveRevision != nil {
		rev = rule.ActiveRevision
	} else {
		revs, _ := s.store.ListTriggerRuleRevisions(ctx, tenantID, ruleID)
		if len(revs) > 0 {
			cp := revs[len(revs)-1]
			rev = &cp
		}
	}
	mapped := map[string]any{}
	var warnings []string
	if rev == nil {
		warnings = append(warnings, "trigger rule has no revisions yet")
		return SimulateTriggerRuleResult{Matched: false, MappedData: mapped, Warnings: warnings}, nil
	}
	for dstKey, expr := range rev.DataMappingCEL {
		srcKey := strings.TrimPrefix(strings.TrimSpace(expr), "vars.")
		if val, ok := sampleCtx[srcKey]; ok {
			mapped[dstKey] = val
		} else {
			warnings = append(warnings, fmt.Sprintf("context field %q missing for template variable %q", srcKey, dstKey))
		}
	}
	if warnings == nil {
		warnings = []string{}
	}
	return SimulateTriggerRuleResult{Matched: true, MappedData: mapped, Warnings: warnings}, nil
}

type VariableMappingLink struct {
	ProcessKey  string `json:"process_key"`
	TriggerKind string `json:"trigger_kind"`
	TemplateKey string `json:"template_key"`
	SchemaMajor uint32 `json:"schema_major"`
	VariableKey string `json:"variable_key"`
	Severity    string `json:"severity"` // OK | WARN | CRITICAL
	Reason      string `json:"reason"`   // OK | MISSING_IN_CONTEXT | TYPE_MISMATCH | UNDECLARED_IN_TEMPLATE | UNUSED_EXPORT | UNBOUND_PROCESS
}

type VariableMappingResult struct {
	Links     []VariableMappingLink `json:"links"`
	Processes []ProcessContextSpec  `json:"processes"`
}

func (s *Service) QueryVariableMapping(ctx context.Context, tenantID, filterProcess, filterTemplate string) (VariableMappingResult, error) {
	if tenantID == "" {
		return VariableMappingResult{}, fmt.Errorf("%w: tenant_id is required", ErrInvalid)
	}
	procs, err := s.processCatalog.ListProcesses(ctx, tenantID)
	if err != nil {
		return VariableMappingResult{}, err
	}
	procMap := make(map[string]ProcessContextSpec, len(procs))
	procFieldTypes := make(map[string]map[string]string, len(procs))
	for _, p := range procs {
		procMap[p.ProcessKey] = p
		ft := make(map[string]string, len(p.Fields))
		for _, f := range p.Fields {
			ft[f.Name] = f.Type
		}
		procFieldTypes[p.ProcessKey] = ft
	}

	rules, err := s.store.ListTriggerRules(ctx, tenantID, filterProcess)
	if err != nil {
		return VariableMappingResult{}, err
	}
	usedProcessFields := make(map[string]map[string]bool)
	boundProcesses := make(map[string]bool)
	var links []VariableMappingLink

	for _, rule := range rules {
		tmpl, err := s.store.GetTemplate(ctx, tenantID, rule.TemplateID)
		if err != nil {
			continue
		}
		if filterTemplate != "" && tmpl.TemplateKey != filterTemplate {
			continue
		}
		boundProcesses[rule.WorkflowType] = true
		if usedProcessFields[rule.WorkflowType] == nil {
			usedProcessFields[rule.WorkflowType] = make(map[string]bool)
		}

		// Resolve template schema (active release or latest version)
		var tv *TemplateVersion
		if rel, rerr := s.store.GetActiveTemplateRelease(ctx, tenantID, tmpl.ID, rule.SchemaMajor, s.now().UTC()); rerr == nil {
			tv, _ = s.store.GetTemplateVersion(ctx, tenantID, rel.TemplateVersionID)
		}
		if tv == nil {
			if versions, verr := s.store.ListTemplateVersions(ctx, tenantID, tmpl.ID); verr == nil && len(versions) > 0 {
				cp := versions[len(versions)-1]
				tv = &cp
			}
		}
		tmplFields := map[string]renderer.FieldSpec{}
		if tv != nil {
			if cs, cerr := schema.Compile(tv.VariableSchemaJSON); cerr == nil {
				tmplFields = cs.FieldSpecs()
			}
		}

		// Resolve active or latest rule revision
		rev := rule.ActiveRevision
		if rev == nil {
			if revs, rerr := s.store.ListTriggerRuleRevisions(ctx, tenantID, rule.ID); rerr == nil && len(revs) > 0 {
				cp := revs[len(revs)-1]
				rev = &cp
			}
		}

		ctxFields, procExists := procFieldTypes[rule.WorkflowType]
		if !procExists {
			links = append(links, VariableMappingLink{
				ProcessKey:  rule.WorkflowType,
				TriggerKind: rule.TriggerKind,
				TemplateKey: tmpl.TemplateKey,
				SchemaMajor: rule.SchemaMajor,
				VariableKey: "*",
				Severity:    "CRITICAL",
				Reason:      "UNBOUND_PROCESS",
			})
			continue
		}

		mappedVars := map[string]string{}
		if rev != nil {
			mappedVars = rev.DataMappingCEL
		}
		// Check every mapped variable in the rule
		for dstVar, expr := range mappedVars {
			srcField := strings.TrimPrefix(strings.TrimSpace(expr), "vars.")
			usedProcessFields[rule.WorkflowType][srcField] = true

			tmplSpec, declaredInTmpl := tmplFields[dstVar]
			ctxType, existsInCtx := ctxFields[srcField]
			switch {
			case !existsInCtx:
				links = append(links, VariableMappingLink{
					ProcessKey:  rule.WorkflowType,
					TriggerKind: rule.TriggerKind,
					TemplateKey: tmpl.TemplateKey,
					SchemaMajor: rule.SchemaMajor,
					VariableKey: dstVar,
					Severity:    "CRITICAL",
					Reason:      "MISSING_IN_CONTEXT",
				})
			case !declaredInTmpl:
				links = append(links, VariableMappingLink{
					ProcessKey:  rule.WorkflowType,
					TriggerKind: rule.TriggerKind,
					TemplateKey: tmpl.TemplateKey,
					SchemaMajor: rule.SchemaMajor,
					VariableKey: dstVar,
					Severity:    "WARN",
					Reason:      "UNDECLARED_IN_TEMPLATE",
				})
			case !typesCompatible(ctxType, tmplSpec.Type):
				links = append(links, VariableMappingLink{
					ProcessKey:  rule.WorkflowType,
					TriggerKind: rule.TriggerKind,
					TemplateKey: tmpl.TemplateKey,
					SchemaMajor: rule.SchemaMajor,
					VariableKey: dstVar,
					Severity:    "CRITICAL",
					Reason:      "TYPE_MISMATCH",
				})
			default:
				links = append(links, VariableMappingLink{
					ProcessKey:  rule.WorkflowType,
					TriggerKind: rule.TriggerKind,
					TemplateKey: tmpl.TemplateKey,
					SchemaMajor: rule.SchemaMajor,
					VariableKey: dstVar,
					Severity:    "OK",
					Reason:      "OK",
				})
			}
		}

		// Also check template variables not mapped by rule
		for tmplVar := range tmplFields {
			if _, mapped := mappedVars[tmplVar]; !mapped {
				links = append(links, VariableMappingLink{
					ProcessKey:  rule.WorkflowType,
					TriggerKind: rule.TriggerKind,
					TemplateKey: tmpl.TemplateKey,
					SchemaMajor: rule.SchemaMajor,
					VariableKey: tmplVar,
					Severity:    "CRITICAL",
					Reason:      "MISSING_IN_CONTEXT",
				})
			}
		}
	}

	// Report UNUSED_EXPORT and UNBOUND_PROCESS for processes in catalog
	if filterTemplate == "" {
		for _, p := range procs {
			if filterProcess != "" && p.ProcessKey != filterProcess {
				continue
			}
			if !boundProcesses[p.ProcessKey] {
				links = append(links, VariableMappingLink{
					ProcessKey: p.ProcessKey,
					Severity:   "WARN",
					Reason:     "UNBOUND_PROCESS",
				})
				continue
			}
			used := usedProcessFields[p.ProcessKey]
			for _, f := range p.Fields {
				if !used[f.Name] {
					links = append(links, VariableMappingLink{
						ProcessKey:  p.ProcessKey,
						VariableKey: f.Name,
						Severity:    "WARN",
						Reason:      "UNUSED_EXPORT",
					})
				}
			}
		}
	}

	if links == nil {
		links = []VariableMappingLink{}
	}
	return VariableMappingResult{Links: links, Processes: procs}, nil
}

func typesCompatible(ctxType, tmplType string) bool {
	if strings.EqualFold(ctxType, tmplType) {
		return true
	}
	if (ctxType == "number" && tmplType == "decimal") || (ctxType == "currency" && tmplType == "money") {
		return true
	}
	return false
}

func (s *Service) ListAuditFiltered(ctx context.Context, filter AuditFilter) ([]AuditRecord, error) {
	if filter.TenantID == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ErrInvalid)
	}
	return s.store.ListAuditFiltered(ctx, filter)
}

var _ = errors.New
