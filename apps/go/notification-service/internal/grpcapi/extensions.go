package grpcapi

import (
	"context"
	"encoding/json"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/application"
	notificationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/notification/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) PlanNotification(ctx context.Context, req *notificationv1.PlanNotificationRequest) (*notificationv1.PlanNotificationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), "", req.GetActorProof(), "notification.operations.plan", "notification.send", "notification.manage", "notification.read")
	if err != nil {
		return nil, err
	}
	recs := make([]application.RecipientInput, 0, len(req.GetRecipients()))
	for _, r := range req.GetRecipients() {
		recs = append(recs, application.RecipientInput{
			SubjectRef:      r.GetSubjectRef(),
			Channel:         fromProtoChannel(r.GetChannel()),
			ExternalAddress: r.GetExternalAddress(),
			DisplayName:     r.GetDisplayName(),
			Locale:          r.GetLocale(),
			Timezone:        r.GetTimezone(),
		})
	}
	plan, err := s.service.PlanNotification(ctx, application.SubmitInput{
		TenantID:      req.GetTenantId(),
		ActorID:       identity.ActorID,
		TemplateKey:   req.GetTemplateKey(),
		SchemaMajor:   req.GetSchemaMajor(),
		PinnedVersion: req.GetPinnedVersion(),
		CategoryKey:   req.GetCategoryKey(),
		LaneOverride:  fromProtoLane(req.GetLaneOverride()),
		Priority:      int(req.GetPriority()),
		Locale:        req.GetLocale(),
		DataJSON:      req.GetDataJson(),
		Recipients:    recs,
	})
	if err != nil {
		return nil, mapError(err)
	}
	cands := make([]*notificationv1.RoutingCandidatePlan, 0, len(plan.RoutingCandidates))
	for _, c := range plan.RoutingCandidates {
		cands = append(cands, &notificationv1.RoutingCandidatePlan{
			ProviderId:   c.ProviderID,
			ProviderKey:  c.ProviderKey,
			BreakerState: c.BreakerState,
			Healthy:      c.Healthy,
		})
	}
	return &notificationv1.PlanNotificationResponse{
		TemplateVersionId:    plan.TemplateVersionID,
		ReleaseOrdinal:       plan.ReleaseOrdinal,
		VariantId:            plan.VariantID,
		Channel:              toProtoChannel(plan.Channel),
		Locale:               plan.Locale,
		SchemaErrors:         plan.SchemaErrors,
		QuotaRemainingMinute: int32(plan.QuotaRemainingMinute),
		QuotaRemainingDay:    int32(plan.QuotaRemainingDay),
		RoutingCandidates:    cands,
		SelectedProviderId:   plan.SelectedProvider,
		FailoverReason:       plan.FailoverReason,
		Warnings:             plan.Warnings,
	}, nil
}

func (s *Server) ValidateTemplate(ctx context.Context, req *notificationv1.ValidateTemplateRequest) (*notificationv1.ValidateTemplateResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := s.verifyActor(req.GetTenantId(), "", req.GetActorProof(), "notification.template.validate", "notification.read", "notification.manage"); err != nil {
		return nil, err
	}
	diags, valid, err := s.service.ValidateTemplateDraft(ctx, application.ValidateTemplateInput{
		Syntax:             req.GetSyntax(),
		VariableSchemaJSON: req.GetVariableSchemaJson(),
		SubjectTmpl:        req.GetSubjectTmpl(),
		HTMLTmpl:           req.GetHtmlTmpl(),
		TextTmpl:           req.GetTextTmpl(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*notificationv1.TemplateDiagnostic, 0, len(diags))
	for _, d := range diags {
		td := &notificationv1.TemplateDiagnostic{
			Code:     d.Code,
			Severity: d.Severity,
			Target:   d.Target,
			Line:     uint32(d.Line),
			Column:   uint32(d.Column),
			Message:  d.Message,
		}
		if d.QuickFix != nil {
			td.QuickFixLabel = d.QuickFix.Label
			td.QuickFixStart = uint32(d.QuickFix.ReplaceStart)
			td.QuickFixEnd = uint32(d.QuickFix.ReplaceEnd)
			td.QuickFixReplacement = d.QuickFix.Replacement
		}
		out = append(out, td)
	}
	return &notificationv1.ValidateTemplateResponse{Valid: valid, Diagnostics: out}, nil
}

func (s *Server) BatchTemplateActions(ctx context.Context, req *notificationv1.BatchTemplateActionsRequest) (*notificationv1.BatchTemplateActionsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.template.batch", "notification.manage")
	if err != nil {
		return nil, err
	}
	res, err := s.service.BatchTemplateActions(ctx, application.BatchTemplateActionsInput{
		TenantID:       req.GetTenantId(),
		ActorID:        identity.ActorID,
		CommandID:      req.GetCommandId(),
		IdempotencyKey: req.GetIdempotencyKey(),
		Action:         req.GetAction(),
		IDs:            req.GetIds(),
		Status:         req.GetStatus(),
		TagMode:        req.GetTagMode(),
		Tags:           req.GetTags(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*notificationv1.BatchTemplateActionResult, 0, len(res))
	for _, r := range res {
		out = append(out, &notificationv1.BatchTemplateActionResult{Id: r.ID, Ok: r.OK, Error: r.Error})
	}
	return &notificationv1.BatchTemplateActionsResponse{Results: out}, nil
}

func (s *Server) ListBreakerTransitions(ctx context.Context, req *notificationv1.ListBreakerTransitionsRequest) (*notificationv1.ListBreakerTransitionsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := s.verifyActor(req.GetTenantId(), "", req.GetActorProof(), "notification.read", "notification.manage"); err != nil {
		return nil, err
	}
	items, err := s.service.ListBreakerTransitions(ctx, req.GetTenantId(), req.GetProviderId(), int(req.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*notificationv1.BreakerTransitionRecord, 0, len(items))
	for _, tr := range items {
		out = append(out, &notificationv1.BreakerTransitionRecord{
			Id:               tr.ID,
			ProviderId:       tr.ProviderID,
			FromState:        tr.FromState,
			ToState:          tr.ToState,
			TriggerKind:      tr.TriggerKind,
			Reason:           tr.Reason,
			ActorRef:         tr.ActorRef,
			ErrorRateBps:     tr.ErrorRateBps,
			SlowCallRateBps:  tr.SlowCallRateBps,
			OccurredAtEpochMs: tr.OccurredAt.UnixMilli(),
		})
	}
	return &notificationv1.ListBreakerTransitionsResponse{Transitions: out}, nil
}

func (s *Server) ProbeBreaker(ctx context.Context, req *notificationv1.ProbeBreakerRequest) (*notificationv1.ProbeBreakerResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.breaker.probe", "notification.manage")
	if err != nil {
		return nil, err
	}
	view, err := s.service.ProbeBreaker(ctx, req.GetTenantId(), identity.ActorID, "", "", req.GetCommandId(), req.GetProviderId(), req.GetReason())
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.ProbeBreakerResponse{Provider: toProtoProviderHealth(view)}, nil
}

func (s *Server) QueryVariableMapping(ctx context.Context, req *notificationv1.QueryVariableMappingRequest) (*notificationv1.QueryVariableMappingResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := s.verifyActor(req.GetTenantId(), "", req.GetActorProof(), "notification.variable_mapping.read", "notification.read", "notification.manage"); err != nil {
		return nil, err
	}
	res, err := s.service.QueryVariableMapping(ctx, req.GetTenantId(), req.GetProcessKey(), req.GetTemplateKey())
	if err != nil {
		return nil, mapError(err)
	}
	links := make([]*notificationv1.VariableMappingLink, 0, len(res.Links))
	for _, l := range res.Links {
		links = append(links, &notificationv1.VariableMappingLink{
			ProcessKey:  l.ProcessKey,
			TriggerKind: l.TriggerKind,
			TemplateKey: l.TemplateKey,
			SchemaMajor: l.SchemaMajor,
			VariableKey: l.VariableKey,
			Severity:    l.Severity,
			Reason:      l.Reason,
		})
	}
	procs := make([]string, 0, len(res.Processes))
	for _, p := range res.Processes {
		procs = append(procs, p.ProcessKey)
	}
	return &notificationv1.QueryVariableMappingResponse{Links: links, Processes: procs}, nil
}

func (s *Server) ListTriggerRules(ctx context.Context, req *notificationv1.ListTriggerRulesRequest) (*notificationv1.ListTriggerRulesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := s.verifyActor(req.GetTenantId(), "", req.GetActorProof(), "notification.rule.read", "notification.read", "notification.manage"); err != nil {
		return nil, err
	}
	rules, err := s.service.Store().ListTriggerRules(ctx, req.GetTenantId(), req.GetWorkflowType())
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*notificationv1.TriggerRuleRecord, 0, len(rules))
	for _, r := range rules {
		out = append(out, toProtoTriggerRule(r))
	}
	return &notificationv1.ListTriggerRulesResponse{Rules: out}, nil
}

func (s *Server) CreateTriggerRule(ctx context.Context, req *notificationv1.CreateTriggerRuleRequest) (*notificationv1.CreateTriggerRuleResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.rule.write", "notification.manage")
	if err != nil {
		return nil, err
	}
	rule, err := s.service.CreateTriggerRule(ctx, application.CreateTriggerRuleInput{
		TenantID:     req.GetTenantId(),
		ActorID:      identity.ActorID,
		RuleKey:      req.GetRuleKey(),
		Name:         req.GetName(),
		WorkflowType: req.GetWorkflowType(),
		NodeID:       req.GetNodeId(),
		TriggerKind:  req.GetTriggerKind(),
		TemplateID:   req.GetTemplateId(),
		SchemaMajor:  req.GetSchemaMajor(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.CreateTriggerRuleResponse{Rule: toProtoTriggerRule(rule)}, nil
}

func (s *Server) SaveTriggerRuleRevision(ctx context.Context, req *notificationv1.SaveTriggerRuleRevisionRequest) (*notificationv1.SaveTriggerRuleRevisionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.rule.write", "notification.manage")
	if err != nil {
		return nil, err
	}
	var mapping map[string]string
	if len(req.GetDataMappingCelJson()) > 0 {
		_ = json.Unmarshal(req.GetDataMappingCelJson(), &mapping)
	}
	rev, err := s.service.SaveTriggerRuleRevision(ctx, application.SaveTriggerRuleRevisionInput{
		TenantID:           req.GetTenantId(),
		ActorID:            identity.ActorID,
		RuleID:             req.GetRuleId(),
		VersionSelector:    req.GetVersionSelector(),
		ConditionCEL:       req.GetConditionCel(),
		RecipientSpecJSON:  req.GetRecipientSpecJson(),
		DataMappingCEL:     mapping,
		ReferencedVars:     req.GetReferencedVars(),
		DelaySeconds:       int(req.GetDelaySeconds()),
		DedupWindowSeconds: int(req.GetDedupWindowSeconds()),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.SaveTriggerRuleRevisionResponse{Revision: toProtoTriggerRuleRevision(rev)}, nil
}

func (s *Server) ActivateTriggerRuleRevision(ctx context.Context, req *notificationv1.ActivateTriggerRuleRevisionRequest) (*notificationv1.ActivateTriggerRuleRevisionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.rule.activate", "notification.manage")
	if err != nil {
		return nil, err
	}
	rule, err := s.service.ActivateTriggerRuleRevision(ctx, req.GetTenantId(), identity.ActorID, req.GetRuleId(), req.GetRevision())
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.ActivateTriggerRuleRevisionResponse{Rule: toProtoTriggerRule(rule)}, nil
}

func (s *Server) SimulateTriggerRule(ctx context.Context, req *notificationv1.SimulateTriggerRuleRequest) (*notificationv1.SimulateTriggerRuleResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := s.verifyActor(req.GetTenantId(), "", req.GetActorProof(), "notification.rule.read", "notification.read", "notification.manage"); err != nil {
		return nil, err
	}
	res, err := s.service.SimulateTriggerRule(ctx, req.GetTenantId(), req.GetRuleId(), req.GetSampleContextJson())
	if err != nil {
		return nil, mapError(err)
	}
	mappedBytes, _ := json.Marshal(res.MappedData)
	return &notificationv1.SimulateTriggerRuleResponse{
		Matched:        res.Matched,
		MappedDataJson: mappedBytes,
		Warnings:       res.Warnings,
	}, nil
}

func toProtoTriggerRule(r application.TriggerRule) *notificationv1.TriggerRuleRecord {
	out := &notificationv1.TriggerRuleRecord{
		Id:            r.ID,
		TenantId:      r.TenantID,
		RuleKey:       r.RuleKey,
		Name:          r.Name,
		WorkflowType:  r.WorkflowType,
		NodeId:        r.NodeID,
		TriggerKind:   r.TriggerKind,
		TemplateId:    r.TemplateID,
		SchemaMajor:   r.SchemaMajor,
		ActiveOrdinal: r.ActiveOrdinal,
		Status:        r.Status,
	}
	if r.ActiveRevision != nil {
		out.ActiveRevision = toProtoTriggerRuleRevision(*r.ActiveRevision)
	}
	return out
}

func toProtoTriggerRuleRevision(rev application.TriggerRuleRevision) *notificationv1.TriggerRuleRevisionRecord {
	mapBytes, _ := json.Marshal(rev.DataMappingCEL)
	var pubMS int64
	if !rev.PublishedAt.IsZero() {
		pubMS = rev.PublishedAt.UnixMilli()
	}
	return &notificationv1.TriggerRuleRevisionRecord{
		Id:                 rev.ID,
		RuleId:             rev.RuleID,
		Revision:           rev.Revision,
		VersionSelector:    rev.VersionSelector,
		ConditionCel:       rev.ConditionCEL,
		RecipientSpecJson:  rev.RecipientSpecJSON,
		DataMappingCelJson: mapBytes,
		ReferencedVars:     rev.ReferencedVars,
		DelaySeconds:       uint32(rev.DelaySeconds),
		DedupWindowSeconds: uint32(rev.DedupWindowSeconds),
		Status:             rev.Status,
		PublishedAtEpochMs: pubMS,
	}
}
