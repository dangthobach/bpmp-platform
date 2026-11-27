package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/application"
	"github.com/dangthobach/bpmp-platform/go/platform/actorcontext"
)

func primaryRole(id actorcontext.Identity) string {
	if len(id.Roles) > 0 {
		return id.Roles[0]
	}
	return ""
}

func clientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return xff
	}
	return r.RemoteAddr
}

func formatProviderHealthDTO(v application.ProviderHealthView) map[string]any {
	healthStatus := "HEALTHY"
	switch v.Breaker.BreakerState {
	case "OPEN":
		healthStatus = "DOWN"
	case "HALF_OPEN":
		healthStatus = "DEGRADED"
	}
	return map[string]any{
		"profile":               v.Profile,
		"breaker":               v.Breaker,
		"health_status":         healthStatus,
		"avg_latency_ms":        v.Breaker.AvgLatencyMS,
		"success_rate_bps":      v.Breaker.SuccessRateBps,
		"sent_24h":              v.Sent24h,
		"buffered_calls":        v.Breaker.BufferedCalls,
		"failed_calls":          v.Breaker.FailedCalls,
		"slow_calls":            v.Breaker.SlowCalls,
		"cooldown_remaining_ms": v.Breaker.CooldownRemainingMS,
		"fallback_provider":     v.FallbackProvider,
	}
}

func (h *Handler) validateTemplateDraft(w http.ResponseWriter, r *http.Request) {
	_, _, _, ok := h.authenticate(w, r, false, "notification.template.validate", "notification.read", "notification.manage")
	if !ok {
		return
	}
	var body struct {
		Syntax         string          `json:"syntax"`
		VariableSchema json.RawMessage `json:"variable_schema"`
		SubjectTmpl    string          `json:"subject_tmpl"`
		HTMLTmpl       string          `json:"html_tmpl"`
		TextTmpl       string          `json:"text_tmpl"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	diags, valid, err := h.service.ValidateTemplateDraft(r.Context(), application.ValidateTemplateInput{
		Syntax:             body.Syntax,
		VariableSchemaJSON: body.VariableSchema,
		SubjectTmpl:        body.SubjectTmpl,
		HTMLTmpl:           body.HTMLTmpl,
		TextTmpl:           body.TextTmpl,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":       valid,
		"diagnostics": nonNilSlice(diags),
	})
}

func (h *Handler) batchTemplateActions(w http.ResponseWriter, r *http.Request) {
	id, cmdID, idempKey, ok := h.authenticate(w, r, true, "notification.template.batch", "notification.manage")
	if !ok {
		return
	}
	var body struct {
		Action  string   `json:"action"`
		IDs     []string `json:"ids"`
		Status  string   `json:"status"`
		TagMode string   `json:"tag_mode"`
		Tags    []string `json:"tags"`
		Reason  string   `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	results, err := h.service.BatchTemplateActions(r.Context(), application.BatchTemplateActionsInput{
		TenantID:       id.TenantID,
		ActorID:        id.ActorID,
		ActorRole:      primaryRole(id),
		ClientIP:       clientIP(r),
		CommandID:      cmdID,
		IdempotencyKey: idempKey,
		Action:         body.Action,
		IDs:            body.IDs,
		Status:         body.Status,
		TagMode:        body.TagMode,
		Tags:           body.Tags,
		Reason:         body.Reason,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": nonNilSlice(results)})
}

func (h *Handler) restoreTemplate(w http.ResponseWriter, r *http.Request) {
	id, cmdID, _, ok := h.authenticate(w, r, true, "notification.manage")
	if !ok {
		return
	}
	tmpl, err := h.service.RestoreTemplate(r.Context(), id.TenantID, id.ActorID, primaryRole(id), clientIP(r), cmdID, r.PathValue("templateID"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"template": tmpl})
}

func (h *Handler) listTemplateTags(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	tags, err := h.service.ListTemplateTags(r.Context(), id.TenantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": nonNilSlice(tags)})
}

func (h *Handler) listBreakerTransitions(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.read", "notification.manage")
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.ListBreakerTransitions(r.Context(), id.TenantID, r.PathValue("providerID"), limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transitions": nonNilSlice(items)})
}

func (h *Handler) probeBreaker(w http.ResponseWriter, r *http.Request) {
	id, cmdID, _, ok := h.authenticate(w, r, true, "notification.breaker.probe", "notification.manage")
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &body) {
			return
		}
	}
	view, err := h.service.ProbeBreaker(r.Context(), id.TenantID, id.ActorID, primaryRole(id), clientIP(r), cmdID, r.PathValue("providerID"), body.Reason)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, formatProviderHealthDTO(view))
}

func (h *Handler) simulateProviderFailure(w http.ResponseWriter, r *http.Request) {
	id, cmdID, _, ok := h.authenticate(w, r, true, "notification.manage")
	if !ok {
		return
	}
	var body struct {
		Count     int    `json:"count"`
		ErrorCode string `json:"error_code"`
	}
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &body) {
			return
		}
	}
	view, err := h.service.SimulateProviderFailure(r.Context(), application.SimulateFailureInput{
		TenantID:   id.TenantID,
		ActorID:    id.ActorID,
		ActorRole:  primaryRole(id),
		ClientIP:   clientIP(r),
		CommandID:  cmdID,
		ProviderID: r.PathValue("providerID"),
		Count:      body.Count,
		ErrorCode:  body.ErrorCode,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, formatProviderHealthDTO(view))
}

func (h *Handler) listTriggerRules(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.rule.read", "notification.read", "notification.manage")
	if !ok {
		return
	}
	rules, err := h.service.Store().ListTriggerRules(r.Context(), id.TenantID, r.URL.Query().Get("workflow_type"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trigger_rules": nonNilSlice(rules)})
}

func (h *Handler) createTriggerRule(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, true, "notification.rule.write", "notification.manage")
	if !ok {
		return
	}
	var body struct {
		RuleKey      string `json:"rule_key"`
		Name         string `json:"name"`
		WorkflowType string `json:"workflow_type"`
		NodeID       string `json:"node_id"`
		TriggerKind  string `json:"trigger_kind"`
		TemplateID   string `json:"template_id"`
		SchemaMajor  uint32 `json:"schema_major"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	rule, err := h.service.CreateTriggerRule(r.Context(), application.CreateTriggerRuleInput{
		TenantID:     id.TenantID,
		ActorID:      id.ActorID,
		RuleKey:      body.RuleKey,
		Name:         body.Name,
		WorkflowType: body.WorkflowType,
		NodeID:       body.NodeID,
		TriggerKind:  body.TriggerKind,
		TemplateID:   body.TemplateID,
		SchemaMajor:  body.SchemaMajor,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"trigger_rule": rule})
}

func (h *Handler) saveTriggerRuleRevision(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, true, "notification.rule.write", "notification.manage")
	if !ok {
		return
	}
	var body struct {
		VersionSelector    string            `json:"version_selector"`
		ConditionCEL       string            `json:"condition_cel"`
		RecipientSpec      json.RawMessage   `json:"recipient_spec"`
		DataMappingCEL     map[string]string `json:"data_mapping_cel"`
		ReferencedVars     []string          `json:"referenced_vars"`
		DelaySeconds       int               `json:"delay_seconds"`
		DedupWindowSeconds int               `json:"dedup_window_seconds"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	rev, err := h.service.SaveTriggerRuleRevision(r.Context(), application.SaveTriggerRuleRevisionInput{
		TenantID:           id.TenantID,
		ActorID:            id.ActorID,
		RuleID:             r.PathValue("ruleID"),
		VersionSelector:    body.VersionSelector,
		ConditionCEL:       body.ConditionCEL,
		RecipientSpecJSON:  body.RecipientSpec,
		DataMappingCEL:     body.DataMappingCEL,
		ReferencedVars:     body.ReferencedVars,
		DelaySeconds:       body.DelaySeconds,
		DedupWindowSeconds: body.DedupWindowSeconds,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": rev})
}

func (h *Handler) activateTriggerRuleRevision(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, true, "notification.rule.activate", "notification.manage")
	if !ok {
		return
	}
	revNum, err := strconv.ParseUint(r.PathValue("revision"), 10, 64)
	if err != nil || revNum == 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_revision")
		return
	}
	rule, err := h.service.ActivateTriggerRuleRevision(r.Context(), id.TenantID, id.ActorID, r.PathValue("ruleID"), revNum)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trigger_rule": rule})
}

func (h *Handler) simulateTriggerRule(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.rule.read", "notification.read", "notification.manage")
	if !ok {
		return
	}
	var body struct {
		SampleContext json.RawMessage `json:"sample_context"`
	}
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &body) {
			return
		}
	}
	res, err := h.service.SimulateTriggerRule(r.Context(), id.TenantID, r.PathValue("ruleID"), body.SampleContext)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) queryVariableMapping(w http.ResponseWriter, r *http.Request) {
	id, _, _, ok := h.authenticate(w, r, false, "notification.variable_mapping.read", "notification.read", "notification.manage")
	if !ok {
		return
	}
	q := r.URL.Query()
	res, err := h.service.QueryVariableMapping(r.Context(), id.TenantID, q.Get("process_key"), q.Get("template_key"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
