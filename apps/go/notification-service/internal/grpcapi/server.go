package grpcapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/provider"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	notificationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/notification/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/actorcontext"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	notificationv1.UnimplementedNotificationDispatchServiceServer
	notificationv1.UnimplementedNotificationInboxServiceServer
	notificationv1.UnimplementedNotificationAdminServiceServer

	service       *application.Service
	proofVerifier *actorcontext.Verifier
	now           func() time.Time
}

func NewServer(service *application.Service, proofVerifier *actorcontext.Verifier) (*Server, error) {
	if service == nil {
		return nil, errors.New("application service is required")
	}
	return &Server{
		service:       service,
		proofVerifier: proofVerifier,
		now:           time.Now,
	}, nil
}

func (s *Server) SetClock(fn func() time.Time) {
	if fn != nil {
		s.now = fn
	}
}

func (s *Server) Register(reg grpc.ServiceRegistrar) {
	notificationv1.RegisterNotificationDispatchServiceServer(reg, s)
	notificationv1.RegisterNotificationInboxServiceServer(reg, s)
	notificationv1.RegisterNotificationAdminServiceServer(reg, s)
}

func (s *Server) verifyActor(tenantID, commandID string, proof *authv1.ActorProof, requiredCaps ...string) (actorcontext.Identity, error) {
	if strings.TrimSpace(tenantID) == "" {
		return actorcontext.Identity{}, status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	if s.proofVerifier == nil {
		return actorcontext.Identity{TenantID: tenantID, ActorID: "workload", Capabilities: requiredCaps}, nil
	}
	if proof == nil || proof.GetType() != authv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT || len(proof.GetSignedProof()) == 0 {
		return actorcontext.Identity{}, status.Error(codes.Unauthenticated, "signed internal actor proof is required")
	}
	identity, err := s.proofVerifier.Verify(proof.GetSignedProof(), tenantID, commandID, s.now().UTC())
	if err != nil {
		return actorcontext.Identity{}, status.Error(codes.Unauthenticated, "actor proof verification failed")
	}
	if len(requiredCaps) > 0 {
		matched := false
		for _, want := range requiredCaps {
			for _, got := range identity.Capabilities {
				if got == want {
					matched = true
					break
				}
			}
		}
		if !matched {
			return actorcontext.Identity{}, status.Error(codes.PermissionDenied, "caller lacks required capability")
		}
	}
	return identity, nil
}

// --- NotificationDispatchService ---

func (s *Server) SubmitNotification(ctx context.Context, req *notificationv1.SubmitNotificationRequest) (*notificationv1.SubmitNotificationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.send", "notification.manage")
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
	var sendAt, expiresAt time.Time
	if req.GetSendAtEpochMs() > 0 {
		sendAt = time.UnixMilli(req.GetSendAtEpochMs()).UTC()
	}
	if req.GetExpiresAtEpochMs() > 0 {
		expiresAt = time.UnixMilli(req.GetExpiresAtEpochMs()).UTC()
	}
	res, err := s.service.SubmitNotification(ctx, application.SubmitInput{
		TenantID:       req.GetTenantId(),
		ActorID:        identity.ActorID,
		CommandID:      req.GetCommandId(),
		IdempotencyKey: req.GetIdempotencyKey(),
		CorrelationID:  req.GetCorrelationId(),
		TemplateKey:    req.GetTemplateKey(),
		SchemaMajor:    req.GetSchemaMajor(),
		PinnedVersion:  req.GetPinnedVersion(),
		CategoryKey:    req.GetCategoryKey(),
		LaneOverride:   fromProtoLane(req.GetLaneOverride()),
		Priority:       int(req.GetPriority()),
		Locale:         req.GetLocale(),
		DataJSON:       req.GetDataJson(),
		Recipients:     recs,
		SendAt:         sendAt,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		return nil, mapError(err)
	}
	receipts := make([]*notificationv1.DeliveryReceipt, 0, len(res.Deliveries))
	for _, d := range res.Deliveries {
		receipts = append(receipts, &notificationv1.DeliveryReceipt{
			DeliveryId:          d.ID,
			Channel:             toProtoChannel(d.Channel),
			RecipientSubjectRef: d.RecipientSubjectRef,
			MaskedAddress:       d.MaskedAddress,
			Status:              toProtoDeliveryStatus(d.Status),
		})
	}
	return &notificationv1.SubmitNotificationResponse{
		RequestId:  res.Request.ID,
		Replayed:   res.Replayed,
		Status:     res.Request.Status,
		Deliveries: receipts,
	}, nil
}

func (s *Server) GetNotificationRequest(ctx context.Context, req *notificationv1.GetNotificationRequestRequest) (*notificationv1.GetNotificationRequestResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := s.verifyActor(req.GetTenantId(), "", req.GetActorProof(), "notification.read", "notification.send", "notification.manage"); err != nil {
		return nil, err
	}
	rec, dels, err := s.service.Store().GetNotificationRequest(ctx, req.GetTenantId(), req.GetRequestId())
	if err != nil {
		return nil, mapError(err)
	}
	receipts := make([]*notificationv1.DeliveryReceipt, 0, len(dels))
	for _, d := range dels {
		receipts = append(receipts, &notificationv1.DeliveryReceipt{
			DeliveryId:          d.ID,
			Channel:             toProtoChannel(d.Channel),
			RecipientSubjectRef: d.RecipientSubjectRef,
			MaskedAddress:       d.MaskedAddress,
			Status:              toProtoDeliveryStatus(d.Status),
		})
	}
	return &notificationv1.GetNotificationRequestResponse{
		RequestId:        rec.ID,
		ApplicationId:    rec.ApplicationID,
		TemplateKey:      rec.TemplateKey,
		SchemaMajor:      rec.SchemaMajor,
		CategoryKey:      rec.CategoryKey,
		Lane:             toProtoLane(rec.Lane),
		CorrelationId:    rec.CorrelationID,
		Status:           rec.Status,
		CreatedAtEpochMs: rec.CreatedAt.UnixMilli(),
		Deliveries:       receipts,
	}, nil
}

// --- NotificationInboxService ---

func (s *Server) ListInAppMessages(ctx context.Context, req *notificationv1.ListInAppMessagesRequest) (*notificationv1.ListInAppMessagesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), "", req.GetActorProof(), "notification.inbox", "notification.read")
	if err != nil {
		return nil, err
	}
	items, nextToken, unread, err := s.service.ListInAppMessages(ctx, req.GetTenantId(), identity.ActorID, req.GetUnreadOnly(), int(req.GetPageSize()), req.GetPageToken())
	if err != nil {
		return nil, mapError(err)
	}
	msgs := make([]*notificationv1.InAppMessage, 0, len(items))
	for _, m := range items {
		var readAtMS int64
		if !m.ReadAt.IsZero() {
			readAtMS = m.ReadAt.UnixMilli()
		}
		msgs = append(msgs, &notificationv1.InAppMessage{
			Id:               m.ID,
			DeliveryId:       m.DeliveryID,
			SubjectRef:       m.SubjectRef,
			CategoryKey:      m.CategoryKey,
			Title:            m.Title,
			BodyHtml:         m.BodyHTML,
			ActionUrl:        m.ActionURL,
			ReadAtEpochMs:    readAtMS,
			CreatedAtEpochMs: m.CreatedAt.UnixMilli(),
		})
	}
	return &notificationv1.ListInAppMessagesResponse{
		Messages:      msgs,
		NextPageToken: nextToken,
		UnreadCount:   unread,
	}, nil
}

func (s *Server) MarkInAppMessagesRead(ctx context.Context, req *notificationv1.MarkInAppMessagesReadRequest) (*notificationv1.MarkInAppMessagesReadResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.inbox", "notification.read")
	if err != nil {
		return nil, err
	}
	updated, err := s.service.MarkInAppMessagesRead(ctx, req.GetTenantId(), identity.ActorID, req.GetMessageIds())
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.MarkInAppMessagesReadResponse{UpdatedCount: updated}, nil
}

func (s *Server) GetMyPreferences(ctx context.Context, req *notificationv1.GetMyPreferencesRequest) (*notificationv1.GetMyPreferencesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), "", req.GetActorProof(), "notification.inbox", "notification.read")
	if err != nil {
		return nil, err
	}
	prefs, err := s.service.GetMyPreferences(ctx, req.GetTenantId(), identity.ActorID)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*notificationv1.RecipientPreference, 0, len(prefs))
	for _, p := range prefs {
		out = append(out, &notificationv1.RecipientPreference{
			CategoryKey:        p.CategoryKey,
			Channel:            toProtoChannel(p.Channel),
			Enabled:            p.Enabled,
			UserUnsubscribable: p.UserUnsubscribable,
		})
	}
	return &notificationv1.GetMyPreferencesResponse{Preferences: out}, nil
}

func (s *Server) UpdateMyPreferences(ctx context.Context, req *notificationv1.UpdateMyPreferencesRequest) (*notificationv1.UpdateMyPreferencesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.inbox", "notification.read")
	if err != nil {
		return nil, err
	}
	updates := make([]application.Preference, 0, len(req.GetPreferences()))
	for _, p := range req.GetPreferences() {
		updates = append(updates, application.Preference{
			CategoryKey: p.GetCategoryKey(),
			Channel:     fromProtoChannel(p.GetChannel()),
			Enabled:     p.GetEnabled(),
		})
	}
	prefs, err := s.service.UpdateMyPreferences(ctx, req.GetTenantId(), identity.ActorID, updates)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*notificationv1.RecipientPreference, 0, len(prefs))
	for _, p := range prefs {
		out = append(out, &notificationv1.RecipientPreference{
			CategoryKey:        p.CategoryKey,
			Channel:            toProtoChannel(p.Channel),
			Enabled:            p.Enabled,
			UserUnsubscribable: p.UserUnsubscribable,
		})
	}
	return &notificationv1.UpdateMyPreferencesResponse{Preferences: out}, nil
}

// --- NotificationAdminService ---

func (s *Server) CreateTemplate(ctx context.Context, req *notificationv1.CreateTemplateRequest) (*notificationv1.CreateTemplateResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.manage")
	if err != nil {
		return nil, err
	}
	tmpl, replayed, err := s.service.CreateTemplate(ctx, application.CreateTemplateInput{
		TenantID:       req.GetTenantId(),
		ActorID:        identity.ActorID,
		CommandID:      req.GetCommandId(),
		IdempotencyKey: req.GetIdempotencyKey(),
		TemplateKey:    req.GetTemplateKey(),
		ApplicationID:  req.GetApplicationId(),
		CategoryID:     req.GetCategoryId(),
		Name:           req.GetName(),
		DefaultLocale:  req.GetDefaultLocale(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.CreateTemplateResponse{
		Template: toProtoTemplateSummary(tmpl),
		Replayed: replayed,
	}, nil
}

func (s *Server) ListTemplates(ctx context.Context, req *notificationv1.ListTemplatesRequest) (*notificationv1.ListTemplatesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := s.verifyActor(req.GetTenantId(), "", req.GetActorProof(), "notification.read", "notification.manage"); err != nil {
		return nil, err
	}
	items, nextToken, err := s.service.ListTemplateSummaries(ctx, application.TemplateListFilter{
		TenantID:      req.GetTenantId(),
		ApplicationID: req.GetApplicationId(),
		Channel:       fromProtoChannel(req.GetChannel()),
		Tag:           req.GetTag(),
		Status:        req.GetStatus(),
		Query:         req.GetQuery(),
		PageSize:      int(req.GetPageSize()),
		PageToken:     req.GetPageToken(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*notificationv1.TemplateSummary, 0, len(items))
	for _, t := range items {
		out = append(out, toProtoTemplateSummary(t))
	}
	return &notificationv1.ListTemplatesResponse{Templates: out, NextPageToken: nextToken}, nil
}

func (s *Server) SaveTemplateVersionDraft(ctx context.Context, req *notificationv1.SaveTemplateVersionDraftRequest) (*notificationv1.SaveTemplateVersionDraftResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.manage")
	if err != nil {
		return nil, err
	}
	variants := make([]application.TemplateVariant, 0, len(req.GetVariants()))
	for _, v := range req.GetVariants() {
		variants = append(variants, fromProtoVariantSpec(v))
	}
	tv, err := s.service.SaveTemplateVersionDraft(ctx, application.SaveDraftInput{
		TenantID:           req.GetTenantId(),
		ActorID:            identity.ActorID,
		CommandID:          req.GetCommandId(),
		IdempotencyKey:     req.GetIdempotencyKey(),
		TemplateID:         req.GetTemplateId(),
		VersionID:          req.GetVersionId(),
		SchemaMajor:        req.GetSchemaMajor(),
		Syntax:             req.GetSyntax(),
		LayoutVersionID:    req.GetLayoutVersionId(),
		VariableSchemaJSON: req.GetVariableSchemaJson(),
		Variants:           variants,
		SourceCommit:       req.GetSourceCommit(),
		ChangeNote:         req.GetChangeNote(),
		ExpectedRowVersion: req.GetExpectedRowVersion(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.SaveTemplateVersionDraftResponse{Version: toProtoVersionDetail(tv)}, nil
}

func (s *Server) PublishTemplateVersion(ctx context.Context, req *notificationv1.PublishTemplateVersionRequest) (*notificationv1.PublishTemplateVersionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.manage")
	if err != nil {
		return nil, err
	}
	tv, replayed, err := s.service.PublishTemplateVersion(ctx, application.PublishVersionInput{
		TenantID:       req.GetTenantId(),
		ActorID:        identity.ActorID,
		CommandID:      req.GetCommandId(),
		IdempotencyKey: req.GetIdempotencyKey(),
		TemplateID:     req.GetTemplateId(),
		VersionID:      req.GetVersionId(),
		ApprovalRef:    req.GetApprovalRef(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.PublishTemplateVersionResponse{
		Version:  toProtoVersionDetail(tv),
		Replayed: replayed,
	}, nil
}

func (s *Server) ActivateTemplateRelease(ctx context.Context, req *notificationv1.ActivateTemplateReleaseRequest) (*notificationv1.ActivateTemplateReleaseResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.manage")
	if err != nil {
		return nil, err
	}
	var actAt time.Time
	if req.GetActivateAtEpochMs() > 0 {
		actAt = time.UnixMilli(req.GetActivateAtEpochMs()).UTC()
	}
	rel, replayed, err := s.service.ActivateTemplateRelease(ctx, application.ActivateReleaseInput{
		TenantID:          req.GetTenantId(),
		ActorID:           identity.ActorID,
		CommandID:         req.GetCommandId(),
		IdempotencyKey:    req.GetIdempotencyKey(),
		TemplateID:        req.GetTemplateId(),
		SchemaMajor:       req.GetSchemaMajor(),
		TemplateVersionID: req.GetTemplateVersionId(),
		ActivateAt:        actAt,
	})
	if err != nil {
		return nil, mapError(err)
	}
	var activatedMS int64
	if !rel.ActivatedAt.IsZero() {
		activatedMS = rel.ActivatedAt.UnixMilli()
	}
	return &notificationv1.ActivateTemplateReleaseResponse{
		Release: &notificationv1.TemplateReleaseRecord{
			Id:                rel.ID,
			TemplateId:        rel.TemplateID,
			SchemaMajor:       rel.SchemaMajor,
			TemplateVersionId: rel.TemplateVersionID,
			Ordinal:           rel.Ordinal,
			PreviousOrdinal:   rel.PreviousOrdinal,
			ActivateAtEpochMs: rel.ActivateAt.UnixMilli(),
			ActivatedAtEpochMs: activatedMS,
			Status:            rel.Status,
		},
		Replayed: replayed,
	}, nil
}

func (s *Server) RenderPreview(ctx context.Context, req *notificationv1.RenderPreviewRequest) (*notificationv1.RenderPreviewResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), "", req.GetActorProof(), "notification.read", "notification.manage")
	if err != nil {
		return nil, err
	}
	var draftVar *application.TemplateVariant
	if req.GetDraftVariant() != nil {
		dv := fromProtoVariantSpec(req.GetDraftVariant())
		draftVar = &dv
	}
	res, err := s.service.RenderPreview(ctx, application.PreviewInput{
		TenantID:                req.GetTenantId(),
		ActorID:                 identity.ActorID,
		ActorCapabilities:       identity.Capabilities,
		Mode:                    fromProtoPreviewMode(req.GetMode()),
		TemplateVersionID:       req.GetTemplateVersionId(),
		DeliveryID:              req.GetDeliveryId(),
		Channel:                 fromProtoChannel(req.GetChannel()),
		Locale:                  req.GetLocale(),
		DraftVariableSchemaJSON: req.GetDraftVariableSchemaJson(),
		DraftVariant:            draftVar,
		SampleDataJSON:          req.GetSampleDataJson(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.RenderPreviewResponse{
		Channel:                toProtoChannel(res.Channel),
		Locale:                 res.Locale,
		Subject:                res.Subject,
		Html:                   res.HTML,
		Text:                   res.Text,
		SmsEncoding:            res.SMSEncoding,
		SmsCharCount:           uint32(res.SMSCharCount),
		SmsSegments:            uint32(res.SMSSegments),
		HtmlBytes:              uint32(res.HTMLBytes),
		RenderedWithSampleData: res.RenderedWithSampleData,
		Warnings:               res.Warnings,
	}, nil
}

func (s *Server) SendTestNotification(ctx context.Context, req *notificationv1.SendTestNotificationRequest) (*notificationv1.SendTestNotificationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.manage")
	if err != nil {
		return nil, err
	}
	reqRec, del, err := s.service.SendTestNotification(ctx, application.TestSendInput{
		TenantID:                 req.GetTenantId(),
		ActorID:                  identity.ActorID,
		CommandID:                req.GetCommandId(),
		IdempotencyKey:           req.GetIdempotencyKey(),
		TemplateVersionID:        req.GetTemplateVersionId(),
		Channel:                  fromProtoChannel(req.GetChannel()),
		Locale:                   req.GetLocale(),
		VerifiedRecipientAddress: req.GetVerifiedRecipientAddress(),
		SampleDataJSON:           req.GetSampleDataJson(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.SendTestNotificationResponse{
		RequestId:  reqRec.ID,
		DeliveryId: del.ID,
		Status:     toProtoDeliveryStatus(del.Status),
	}, nil
}

func (s *Server) ListProviderHealth(ctx context.Context, req *notificationv1.ListProviderHealthRequest) (*notificationv1.ListProviderHealthResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := s.verifyActor(req.GetTenantId(), "", req.GetActorProof(), "notification.read", "notification.manage"); err != nil {
		return nil, err
	}
	views, err := s.service.ListProviderHealth(ctx, req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*notificationv1.ProviderHealthRecord, 0, len(views))
	for _, v := range views {
		out = append(out, toProtoProviderHealth(v))
	}
	return &notificationv1.ListProviderHealthResponse{Providers: out}, nil
}

func (s *Server) SetBreakerOverride(ctx context.Context, req *notificationv1.SetBreakerOverrideRequest) (*notificationv1.SetBreakerOverrideResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.manage")
	if err != nil {
		return nil, err
	}
	view, err := s.service.SetBreakerOverride(ctx, application.SetOverrideInput{
		TenantID:       req.GetTenantId(),
		ActorID:        identity.ActorID,
		CommandID:      req.GetCommandId(),
		IdempotencyKey: req.GetIdempotencyKey(),
		ProviderID:     req.GetProviderId(),
		Mode:           fromProtoOverrideMode(req.GetOverrideMode()),
		Reason:         req.GetReason(),
		TTL:            time.Duration(req.GetTtlMs()) * time.Millisecond,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.SetBreakerOverrideResponse{Provider: toProtoProviderHealth(view)}, nil
}

func (s *Server) QueryDeliveries(ctx context.Context, req *notificationv1.QueryDeliveriesRequest) (*notificationv1.QueryDeliveriesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := s.verifyActor(req.GetTenantId(), "", req.GetActorProof(), "notification.read", "notification.manage"); err != nil {
		return nil, err
	}
	var beforeAt time.Time
	if req.GetBeforeCreatedAtEpochMs() > 0 {
		beforeAt = time.UnixMilli(req.GetBeforeCreatedAtEpochMs()).UTC()
	}
	items, nextBeforeAt, nextBeforeID, err := s.service.QueryDeliveries(ctx, application.DeliveryQuery{
		TenantID:            req.GetTenantId(),
		ApplicationID:       req.GetApplicationId(),
		TemplateID:          req.GetTemplateId(),
		Channel:             fromProtoChannel(req.GetChannel()),
		Status:              fromProtoDeliveryStatus(req.GetStatus()),
		CorrelationID:       req.GetCorrelationId(),
		RecipientSubjectRef: req.GetRecipientSubjectRef(),
		Limit:               int(req.GetLimit()),
		BeforeCreatedAt:     beforeAt,
		BeforeID:            req.GetBeforeId(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	recs := make([]*notificationv1.DeliveryMonitoringRecord, 0, len(items))
	for _, d := range items {
		recs = append(recs, toProtoMonitoringRecord(d))
	}
	var nextMS int64
	if !nextBeforeAt.IsZero() {
		nextMS = nextBeforeAt.UnixMilli()
	}
	return &notificationv1.QueryDeliveriesResponse{
		Deliveries:              recs,
		NextBeforeCreatedAtEpochMs: nextMS,
		NextBeforeId:            nextBeforeID,
	}, nil
}

func (s *Server) ResendDelivery(ctx context.Context, req *notificationv1.ResendDeliveryRequest) (*notificationv1.ResendDeliveryResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	identity, err := s.verifyActor(req.GetTenantId(), req.GetCommandId(), req.GetActorProof(), "notification.send", "notification.manage")
	if err != nil {
		return nil, err
	}
	del, replayed, err := s.service.ResendDelivery(ctx, application.ResendInput{
		TenantID:       req.GetTenantId(),
		ActorID:        identity.ActorID,
		CommandID:      req.GetCommandId(),
		IdempotencyKey: req.GetIdempotencyKey(),
		DeliveryID:     req.GetDeliveryId(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.ResendDeliveryResponse{
		Delivery: toProtoMonitoringRecord(del),
		Replayed: replayed,
	}, nil
}

// --- Enum & Message Mappers ---

func mapError(err error) error {
	switch {
	case errors.Is(err, application.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, application.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, application.ErrConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, application.ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, application.ErrQuotaExceeded):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, application.ErrImmutable),
		errors.Is(err, application.ErrIncompatibleSchema),
		errors.Is(err, application.ErrSecretResendForbidden):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func fromProtoChannel(ch notificationv1.NotificationChannel) string {
	switch ch {
	case notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_EMAIL:
		return "EMAIL"
	case notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_SMS:
		return "SMS"
	case notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_PUSH:
		return "PUSH"
	case notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_IN_APP:
		return "IN_APP"
	case notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_ZNS:
		return "ZNS"
	case notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_WEBHOOK:
		return "WEBHOOK"
	default:
		return ""
	}
}

func toProtoChannel(ch string) notificationv1.NotificationChannel {
	switch strings.ToUpper(ch) {
	case "EMAIL":
		return notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_EMAIL
	case "SMS":
		return notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_SMS
	case "PUSH":
		return notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_PUSH
	case "IN_APP":
		return notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_IN_APP
	case "ZNS":
		return notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_ZNS
	case "WEBHOOK":
		return notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_WEBHOOK
	default:
		return notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_UNSPECIFIED
	}
}

func fromProtoLane(l notificationv1.NotificationLane) string {
	switch l {
	case notificationv1.NotificationLane_NOTIFICATION_LANE_TRANSACTIONAL:
		return "transactional"
	case notificationv1.NotificationLane_NOTIFICATION_LANE_NORMAL:
		return "normal"
	case notificationv1.NotificationLane_NOTIFICATION_LANE_BULK:
		return "bulk"
	default:
		return ""
	}
}

func toProtoLane(l string) notificationv1.NotificationLane {
	switch strings.ToLower(l) {
	case "transactional":
		return notificationv1.NotificationLane_NOTIFICATION_LANE_TRANSACTIONAL
	case "normal":
		return notificationv1.NotificationLane_NOTIFICATION_LANE_NORMAL
	case "bulk":
		return notificationv1.NotificationLane_NOTIFICATION_LANE_BULK
	default:
		return notificationv1.NotificationLane_NOTIFICATION_LANE_UNSPECIFIED
	}
}

func toProtoDeliveryStatus(s string) notificationv1.DeliveryStatus {
	switch strings.ToUpper(s) {
	case "ACCEPTED":
		return notificationv1.DeliveryStatus_DELIVERY_STATUS_ACCEPTED
	case "SCHEDULED":
		return notificationv1.DeliveryStatus_DELIVERY_STATUS_SCHEDULED
	case "QUEUED":
		return notificationv1.DeliveryStatus_DELIVERY_STATUS_QUEUED
	case "SENDING":
		return notificationv1.DeliveryStatus_DELIVERY_STATUS_SENDING
	case "SENT":
		return notificationv1.DeliveryStatus_DELIVERY_STATUS_SENT
	case "DELIVERED":
		return notificationv1.DeliveryStatus_DELIVERY_STATUS_DELIVERED
	case "BOUNCED":
		return notificationv1.DeliveryStatus_DELIVERY_STATUS_BOUNCED
	case "FAILED":
		return notificationv1.DeliveryStatus_DELIVERY_STATUS_FAILED
	case "DEFERRED":
		return notificationv1.DeliveryStatus_DELIVERY_STATUS_DEFERRED
	case "EXPIRED":
		return notificationv1.DeliveryStatus_DELIVERY_STATUS_EXPIRED
	case "SUPPRESSED":
		return notificationv1.DeliveryStatus_DELIVERY_STATUS_SUPPRESSED
	case "REJECTED":
		return notificationv1.DeliveryStatus_DELIVERY_STATUS_REJECTED
	default:
		return notificationv1.DeliveryStatus_DELIVERY_STATUS_UNSPECIFIED
	}
}

func fromProtoDeliveryStatus(s notificationv1.DeliveryStatus) string {
	if s == notificationv1.DeliveryStatus_DELIVERY_STATUS_UNSPECIFIED {
		return ""
	}
	return strings.TrimPrefix(s.String(), "DELIVERY_STATUS_")
}

func fromProtoPreviewMode(m notificationv1.PreviewMode) string {
	switch m {
	case notificationv1.PreviewMode_PREVIEW_MODE_DRAFT:
		return "DRAFT"
	case notificationv1.PreviewMode_PREVIEW_MODE_PUBLISHED_VERSION:
		return "PUBLISHED_VERSION"
	case notificationv1.PreviewMode_PREVIEW_MODE_DELIVERY_REPLAY:
		return "DELIVERY_REPLAY"
	default:
		return ""
	}
}

func fromProtoOverrideMode(m notificationv1.BreakerOverrideMode) provider.OverrideMode {
	switch m {
	case notificationv1.BreakerOverrideMode_BREAKER_OVERRIDE_MODE_FORCE_OPEN:
		return provider.OverrideForceOpen
	case notificationv1.BreakerOverrideMode_BREAKER_OVERRIDE_MODE_FORCE_CLOSE:
		return provider.OverrideForceClose
	case notificationv1.BreakerOverrideMode_BREAKER_OVERRIDE_MODE_DRAINING:
		return provider.OverrideDraining
	default:
		return provider.OverrideNone
	}
}

func toProtoTemplateSummary(t application.Template) *notificationv1.TemplateSummary {
	chans := make([]notificationv1.NotificationChannel, 0, len(t.Channels))
	for _, ch := range t.Channels {
		chans = append(chans, toProtoChannel(ch))
	}
	return &notificationv1.TemplateSummary{
		Id:                   t.ID,
		TenantId:             t.TenantID,
		TemplateKey:          t.TemplateKey,
		ApplicationId:        t.ApplicationID,
		CategoryId:           t.CategoryID,
		Name:                 t.Name,
		DefaultLocale:        t.DefaultLocale,
		Status:               t.Status,
		RowVersion:           t.RowVersion,
		UpdatedAtEpochMs:     t.UpdatedAt.UnixMilli(),
		Tags:                 t.Tags,
		Channels:             chans,
		ActiveReleaseOrdinal: t.ActiveRelease,
		Usage_24H:            t.Usage24h,
	}
}

func fromProtoVariantSpec(v *notificationv1.TemplateVariantSpec) application.TemplateVariant {
	kind := "INLINE"
	if v.GetVariantKind() == notificationv1.VariantKind_VARIANT_KIND_PROVIDER_TEMPLATE {
		kind = "PROVIDER_TEMPLATE"
	}
	return application.TemplateVariant{
		ID:                 v.GetId(),
		Channel:            fromProtoChannel(v.GetChannel()),
		Locale:             v.GetLocale(),
		VariantKind:        kind,
		SubjectTmpl:        v.GetSubjectTmpl(),
		HTMLTmpl:           v.GetHtmlTmpl(),
		TextTmpl:           v.GetTextTmpl(),
		MJMLSource:         v.GetMjmlSource(),
		ProviderTemplateID: v.GetProviderTemplateId(),
		ParamMappingJSON:   v.GetParamMappingJson(),
	}
}

func toProtoVersionDetail(tv application.TemplateVersion) *notificationv1.TemplateVersionDetail {
	var st notificationv1.TemplateVersionStatus
	switch tv.Status {
	case "DRAFT":
		st = notificationv1.TemplateVersionStatus_TEMPLATE_VERSION_STATUS_DRAFT
	case "IN_REVIEW":
		st = notificationv1.TemplateVersionStatus_TEMPLATE_VERSION_STATUS_IN_REVIEW
	case "PUBLISHED":
		st = notificationv1.TemplateVersionStatus_TEMPLATE_VERSION_STATUS_PUBLISHED
	case "DEPRECATED":
		st = notificationv1.TemplateVersionStatus_TEMPLATE_VERSION_STATUS_DEPRECATED
	case "RETIRED":
		st = notificationv1.TemplateVersionStatus_TEMPLATE_VERSION_STATUS_RETIRED
	}
	var pubMS int64
	if !tv.PublishedAt.IsZero() {
		pubMS = tv.PublishedAt.UnixMilli()
	}
	vars := make([]*notificationv1.TemplateVariantSpec, 0, len(tv.Variants))
	for _, v := range tv.Variants {
		vk := notificationv1.VariantKind_VARIANT_KIND_INLINE
		if v.VariantKind == "PROVIDER_TEMPLATE" {
			vk = notificationv1.VariantKind_VARIANT_KIND_PROVIDER_TEMPLATE
		}
		vars = append(vars, &notificationv1.TemplateVariantSpec{
			Id:                 v.ID,
			Channel:            toProtoChannel(v.Channel),
			Locale:             v.Locale,
			VariantKind:        vk,
			SubjectTmpl:        v.SubjectTmpl,
			HtmlTmpl:           v.HTMLTmpl,
			TextTmpl:           v.TextTmpl,
			MjmlSource:         v.MJMLSource,
			ProviderTemplateId: v.ProviderTemplateID,
			ParamMappingJson:   v.ParamMappingJSON,
			VariantHash:        v.VariantHash,
		})
	}
	return &notificationv1.TemplateVersionDetail{
		Id:                 tv.ID,
		TemplateId:         tv.TemplateID,
		Version:            tv.Version,
		SchemaMajor:        tv.SchemaMajor,
		Syntax:             tv.Syntax,
		LayoutVersionId:    tv.LayoutVersionID,
		VariableSchemaJson: tv.VariableSchemaJSON,
		ContentHash:        tv.ContentHash,
		SourceCommit:       tv.SourceCommit,
		ChangeNote:         tv.ChangeNote,
		ReleaseOrdinal:     tv.ReleaseOrdinal,
		CreatedBy:          tv.CreatedBy,
		Status:             st,
		PublishedAtEpochMs: pubMS,
		Variants:           vars,
	}
}

func toProtoProviderHealth(v application.ProviderHealthView) *notificationv1.ProviderHealthRecord {
	var bs notificationv1.BreakerState
	switch v.Breaker.BreakerState {
	case provider.BreakerClosed:
		bs = notificationv1.BreakerState_BREAKER_STATE_CLOSED
	case provider.BreakerOpen:
		bs = notificationv1.BreakerState_BREAKER_STATE_OPEN
	case provider.BreakerHalfOpen:
		bs = notificationv1.BreakerState_BREAKER_STATE_HALF_OPEN
	}
	var ov notificationv1.BreakerOverrideMode
	switch v.Breaker.ActiveOverride {
	case provider.OverrideForceOpen:
		ov = notificationv1.BreakerOverrideMode_BREAKER_OVERRIDE_MODE_FORCE_OPEN
	case provider.OverrideForceClose:
		ov = notificationv1.BreakerOverrideMode_BREAKER_OVERRIDE_MODE_FORCE_CLOSE
	case provider.OverrideDraining:
		ov = notificationv1.BreakerOverrideMode_BREAKER_OVERRIDE_MODE_DRAINING
	}
	var expMS int64
	if !v.Breaker.OverrideExpiresAt.IsZero() {
		expMS = v.Breaker.OverrideExpiresAt.UnixMilli()
	}
	return &notificationv1.ProviderHealthRecord{
		ProviderId:               v.Profile.ID,
		ProviderKey:              v.Profile.ProviderKey,
		Channel:                  toProtoChannel(v.Profile.Channel),
		DriverKind:               v.Profile.DriverKind,
		OperationalState:         v.Profile.OperationalState,
		BreakerState:             bs,
		ErrorRateBps:             v.Breaker.ErrorRateBps,
		WindowRequests:           v.Breaker.WindowRequests,
		WindowFailures:           v.Breaker.WindowFailures,
		ActiveOverride:           ov,
		OverrideExpiresAtEpochMs: expMS,
		LastTransitionAtEpochMs:  v.Breaker.LastTransitionAt.UnixMilli(),
		AvgLatencyMs:             v.Breaker.AvgLatencyMS,
		SuccessRateBps:           v.Breaker.SuccessRateBps,
		Sent_24H:                 v.Sent24h,
		BufferedCalls:            v.Breaker.BufferedCalls,
		FailedCalls:              v.Breaker.FailedCalls,
		SlowCalls:                v.Breaker.SlowCalls,
		CooldownRemainingMs:      v.Breaker.CooldownRemainingMS,
		FallbackProvider:         v.FallbackProvider,
	}
}

func toProtoMonitoringRecord(d application.Delivery) *notificationv1.DeliveryMonitoringRecord {
	var compMS int64
	if !d.CompletedAt.IsZero() {
		compMS = d.CompletedAt.UnixMilli()
	}
	return &notificationv1.DeliveryMonitoringRecord{
		Id:                  d.ID,
		RequestId:           d.RequestID,
		TemplateVersionId:   d.TemplateVersionID,
		Channel:             toProtoChannel(d.Channel),
		Lane:                toProtoLane(d.Lane),
		Status:              toProtoDeliveryStatus(d.Status),
		RecipientSubjectRef: d.RecipientSubjectRef,
		MaskedAddress:       d.MaskedAddress,
		SelectedProviderId:  d.SelectedProviderID,
		ContentHash:         d.ContentHash,
		AttemptCount:        uint32(d.AttemptCount),
		ResendOfDeliveryId:  d.ResendOfDeliveryID,
		CreatedAtEpochMs:    d.CreatedAt.UnixMilli(),
		CompletedAtEpochMs:  compMS,
	}
}
