package application

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/crypto"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/queue"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/renderer"
)

func newTestService(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	key := bytes.Repeat([]byte{0x42}, 32)
	ring, err := crypto.NewKeyRing("vault://transit/test-v1", map[string][]byte{"vault://transit/test-v1": key}, key)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(
		NewMemoryStore(),
		queue.NewMemoryQueue(),
		ring,
		renderer.NewEngine(renderer.DefaultRenderLimits()),
		nil,
		Config{TestAllowedDomains: []string{"qa.bpmp.local"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetClock(func() time.Time { return now })
	return svc, &now
}

func TestVerticalSliceLifecyclePreviewSecretsAndResend(t *testing.T) {
	ctx := context.Background()
	svc, clock := newTestService(t)
	tenantID := "00000000-0000-0000-0000-000000000001"
	actorID := "00000000-0000-0000-0000-000000000099"

	// 1. Categories & Applications (namespace + quota).
	secCat, err := svc.CreateCategory(ctx, Category{
		TenantID: tenantID, CategoryKey: "security-otp", Name: "Security OTP",
		DefaultLane: "TRANSACTIONAL", UserUnsubscribable: false, AmbiguousOutcomePolicy: "RESEND_ON_FAILOVER",
	})
	if err != nil {
		t.Fatal(err)
	}
	caseCat, err := svc.CreateCategory(ctx, Category{
		TenantID: tenantID, CategoryKey: "case-updates", Name: "Case Updates",
		DefaultLane: "NORMAL", UserUnsubscribable: true, AmbiguousOutcomePolicy: "RECONCILE_BEFORE_RETRY",
	})
	if err != nil {
		t.Fatal(err)
	}
	caseApp, err := svc.CreateApplication(ctx, Application{
		TenantID: tenantID, AppKey: "case-service", Name: "Case Service",
		AllowedNamespaces: []string{"case.*", "auth.*"},
		QuotaConfig:       QuotaConfig{MaxPerMinute: 5},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Register mock EMAIL + IN_APP providers.
	if _, err := svc.RegisterProvider(ctx, ProviderProfile{
		TenantID: tenantID, ProviderKey: "mock-email", Channel: "EMAIL", DriverKind: "mock",
	}, []byte(`{"preset":"default"}`), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterProvider(ctx, ProviderProfile{
		TenantID: tenantID, ProviderKey: "ods-inapp", Channel: "IN_APP", DriverKind: "inapp",
	}, nil, ""); err != nil {
		t.Fatal(err)
	}

	// Namespace violation must be rejected.
	if _, _, err := svc.CreateTemplate(ctx, CreateTemplateInput{
		TenantID: tenantID, ActorID: actorID, CommandID: "cmd-bad-ns", IdempotencyKey: "idem-bad-ns",
		TemplateKey: "hr.payroll", ApplicationID: caseApp.ID, CategoryID: caseCat.ID, Name: "Forbidden",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for disallowed namespace, got %v", err)
	}

	// 2. Template v1 -> Publish -> Release -> v2 Compatible -> v3 Incompatible rejected -> Monotonic Rollback!
	tmpl, _, err := svc.CreateTemplate(ctx, CreateTemplateInput{
		TenantID: tenantID, ActorID: actorID, CommandID: "cmd-t1", IdempotencyKey: "idem-t1",
		TemplateKey: "case.submitted", ApplicationID: caseApp.ID, CategoryID: caseCat.ID,
		Name: "Case Submitted", DefaultLocale: "vi-VN",
	})
	if err != nil {
		t.Fatal(err)
	}
	v1Schema := []byte(`{
		"type":"object",
		"required":["caseCode"],
		"properties":{"caseCode":{"type":"string","example":"CS-2026-001"}}
	}`)
	v1Draft, err := svc.SaveTemplateVersionDraft(ctx, SaveDraftInput{
		TenantID: tenantID, ActorID: actorID, TemplateID: tmpl.ID, SchemaMajor: 1,
		VariableSchemaJSON: v1Schema,
		Variants: []TemplateVariant{
			{Channel: "EMAIL", Locale: "vi-VN", SubjectTmpl: "Hồ sơ {{ .data.caseCode }}", HTMLTmpl: "<p>Đã tiếp nhận {{ .data.caseCode }}</p>", TextTmpl: "Da tiep nhan {{ .data.caseCode }}"},
			{Channel: "IN_APP", Locale: "vi-VN", SubjectTmpl: "Hồ sơ {{ .data.caseCode }}", HTMLTmpl: "<p>Hồ sơ mới {{ .data.caseCode }}</p>"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	v1Pub, _, err := svc.PublishTemplateVersion(ctx, PublishVersionInput{
		TenantID: tenantID, ActorID: actorID, CommandID: "cmd-pub-1", IdempotencyKey: "idem-pub-1",
		TemplateID: tmpl.ID, VersionID: v1Draft.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Mutating a PUBLISHED version must fail with ErrImmutable!
	if _, err := svc.SaveTemplateVersionDraft(ctx, SaveDraftInput{
		TenantID: tenantID, ActorID: actorID, TemplateID: tmpl.ID, VersionID: v1Pub.ID,
		SchemaMajor: 1, VariableSchemaJSON: v1Schema,
	}); !errors.Is(err, ErrImmutable) {
		t.Fatalf("expected ErrImmutable when editing PUBLISHED version, got %v", err)
	}

	rel1, _, err := svc.ActivateTemplateRelease(ctx, ActivateReleaseInput{
		TenantID: tenantID, ActorID: actorID, CommandID: "cmd-rel-1", IdempotencyKey: "idem-rel-1",
		TemplateID: tmpl.ID, SchemaMajor: 1, TemplateVersionID: v1Pub.ID,
	})
	if err != nil || rel1.Ordinal != 1 {
		t.Fatalf("expected ordinal 1, got %+v err=%v", rel1, err)
	}

	// Breaking v2 (removing caseCode) in same schema_major=1 must fail Publish!
	v2BadDraft, err := svc.SaveTemplateVersionDraft(ctx, SaveDraftInput{
		TenantID: tenantID, ActorID: actorID, TemplateID: tmpl.ID, SchemaMajor: 1,
		VariableSchemaJSON: []byte(`{"type":"object","properties":{"other":{"type":"string","example":"x"}}}`),
		Variants: []TemplateVariant{
			{Channel: "EMAIL", Locale: "vi-VN", SubjectTmpl: "Hi", HTMLTmpl: "<p>{{ .data.other }}</p>"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.PublishTemplateVersion(ctx, PublishVersionInput{
		TenantID: tenantID, ActorID: actorID, TemplateID: tmpl.ID, VersionID: v2BadDraft.ID,
	}); !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("expected ErrIncompatibleSchema, got %v", err)
	}

	// Compatible v3 (adds optional reviewerName) publishes cleanly; release ordinal=2; rollback to v1 creates ordinal=3!
	v3Draft, err := svc.SaveTemplateVersionDraft(ctx, SaveDraftInput{
		TenantID: tenantID, ActorID: actorID, TemplateID: tmpl.ID, SchemaMajor: 1,
		VariableSchemaJSON: []byte(`{
			"type":"object",
			"required":["caseCode"],
			"properties":{
				"caseCode":{"type":"string","example":"CS-2026-001"},
				"reviewerName":{"type":"string","example":"Minh"}
			}
		}`),
		Variants: []TemplateVariant{
			{Channel: "EMAIL", Locale: "vi-VN", SubjectTmpl: "Hồ sơ {{ .data.caseCode }}", HTMLTmpl: "<p>{{ .data.caseCode }} - {{ default \"Hệ thống\" .data.reviewerName }}</p>"},
			{Channel: "IN_APP", Locale: "vi-VN", SubjectTmpl: "Hồ sơ {{ .data.caseCode }}", HTMLTmpl: "<p>{{ .data.caseCode }}</p>"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	v3Pub, _, err := svc.PublishTemplateVersion(ctx, PublishVersionInput{
		TenantID: tenantID, ActorID: actorID, TemplateID: tmpl.ID, VersionID: v3Draft.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	rel2, _, err := svc.ActivateTemplateRelease(ctx, ActivateReleaseInput{
		TenantID: tenantID, ActorID: actorID, TemplateID: tmpl.ID, SchemaMajor: 1, TemplateVersionID: v3Pub.ID,
	})
	if err != nil || rel2.Ordinal != 2 {
		t.Fatalf("expected ordinal 2, got %+v err=%v", rel2, err)
	}
	rollbackRel, _, err := svc.ActivateTemplateRelease(ctx, ActivateReleaseInput{
		TenantID: tenantID, ActorID: actorID, TemplateID: tmpl.ID, SchemaMajor: 1, TemplateVersionID: v1Pub.ID,
	})
	if err != nil || rollbackRel.Ordinal != 3 || rollbackRel.PreviousOrdinal != 2 {
		t.Fatalf("rollback must monotonically increment ordinal to 3, got %+v err=%v", rollbackRel, err)
	}

	// 3. Test Send allowlist enforcement!
	if _, _, err := svc.SendTestNotification(ctx, TestSendInput{
		TenantID: tenantID, ActorID: actorID, TemplateVersionID: v1Pub.ID,
		Channel: "EMAIL", VerifiedRecipientAddress: "stranger@external.com",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected unauthorized test recipient to fail with ErrForbidden, got %v", err)
	}
	_, testDel, err := svc.SendTestNotification(ctx, TestSendInput{
		TenantID: tenantID, ActorID: actorID, TemplateVersionID: v1Pub.ID,
		Channel: "EMAIL", VerifiedRecipientAddress: "dev@qa.bpmp.local",
	})
	if err != nil || testDel.ID == "" {
		t.Fatalf("allowed test domain should succeed, err=%v", err)
	}

	// 4. SubmitNotification idempotency + conflict + dispatch + In-App inbox + Delivery Replay Preview audit + Resend!
	recipientUser := "00000000-0000-0000-0000-000000000077"
	subIn := SubmitInput{
		TenantID: tenantID, ActorID: actorID, IdempotencyKey: "case-submit-1",
		CorrelationID: "corr-case-1", TemplateKey: "case.submitted", SchemaMajor: 1,
		DataJSON: []byte(`{"caseCode":"CS-9001"}`),
		Recipients: []RecipientInput{
			{SubjectRef: recipientUser, Channel: "EMAIL", ExternalAddress: "customer@example.com"},
			{SubjectRef: recipientUser, Channel: "IN_APP"},
		},
	}
	firstSub, err := svc.SubmitNotification(ctx, subIn)
	if err != nil || firstSub.Replayed || len(firstSub.Deliveries) != 2 {
		t.Fatalf("submit failed: %+v err=%v", firstSub, err)
	}
	// Exact retry returns replayed=true!
	replaySub, err := svc.SubmitNotification(ctx, subIn)
	if err != nil || !replaySub.Replayed || replaySub.Request.ID != firstSub.Request.ID {
		t.Fatalf("expected idempotent replay, got %+v err=%v", replaySub, err)
	}
	// Same key + mutated data returns ErrConflict!
	mutated := subIn
	mutated.DataJSON = []byte(`{"caseCode":"CS-9999"}`)
	if _, err := svc.SubmitNotification(ctx, mutated); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on mismatched digest, got %v", err)
	}

	// Dispatch normal lane -> delivers EMAIL + IN_APP!
	processed, err := svc.ProcessQueueBatch(ctx, queue.LaneNormal, 10, 30*time.Second)
	if err != nil || processed != 3 { // 1 test-send + 2 deliveries
		t.Fatalf("expected 3 processed jobs, got %d err=%v", processed, err)
	}

	// Verify In-App message is visible ONLY to recipientUser!
	msgs, _, unread, err := svc.ListInAppMessages(ctx, tenantID, recipientUser, true, 10, "")
	if err != nil || len(msgs) != 1 || unread != 1 || !strings.Contains(msgs[0].Title, "CS-9001") {
		t.Fatalf("unexpected inapp inbox: msgs=%+v unread=%d err=%v", msgs, unread, err)
	}
	otherMsgs, _, _, _ := svc.ListInAppMessages(ctx, tenantID, actorID, false, 10, "")
	if len(otherMsgs) != 0 {
		t.Fatal("inapp messages leaked to another user")
	}

	// Delivery Replay Preview: requires inspect_payload capability & records DELIVERY_PAYLOAD_INSPECTED audit!
	emailDelID := firstSub.Deliveries[0].ID
	if _, err := svc.RenderPreview(ctx, PreviewInput{
		TenantID: tenantID, ActorID: actorID, ActorCapabilities: []string{"notification.read"},
		Mode: "DELIVERY_REPLAY", DeliveryID: emailDelID,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden without inspect_payload cap, got %v", err)
	}
	replayPreview, err := svc.RenderPreview(ctx, PreviewInput{
		TenantID: tenantID, ActorID: actorID, ActorCapabilities: []string{"notification.delivery.inspect_payload"},
		Mode: "DELIVERY_REPLAY", DeliveryID: emailDelID,
	})
	if err != nil || replayPreview.RenderedWithSampleData || !strings.Contains(replayPreview.HTML, "CS-9001") {
		t.Fatalf("unexpected delivery replay preview: %+v err=%v", replayPreview, err)
	}
	audits, _ := svc.Store().ListAudit(ctx, tenantID, 20)
	foundInspectAudit := false
	for _, a := range audits {
		if a.Action == "DELIVERY_PAYLOAD_INSPECTED" && a.ResourceRef == "delivery:"+emailDelID {
			foundInspectAudit = true
			break
		}
	}
	if !foundInspectAudit {
		t.Fatal("mandatory DELIVERY_PAYLOAD_INSPECTED audit evidence was not recorded")
	}

	// Resend creates a new delivery with resend_of_delivery_id!
	resent, _, err := svc.ResendDelivery(ctx, ResendInput{
		TenantID: tenantID, ActorID: actorID, CommandID: "cmd-resend-1", IdempotencyKey: "idem-resend-1",
		DeliveryID: emailDelID,
	})
	if err != nil || resent.ResendOfDeliveryID != emailDelID || resent.ID == emailDelID {
		t.Fatalf("unexpected resent delivery: %+v err=%v", resent, err)
	}

	// Advance clock past 31-day payload retention -> Delivery Replay falls back to sample data!
	*clock = clock.Add(31 * 24 * time.Hour)
	expiredPreview, err := svc.RenderPreview(ctx, PreviewInput{
		TenantID: tenantID, ActorID: actorID, ActorCapabilities: []string{"notification.delivery.inspect_payload"},
		Mode: "DELIVERY_REPLAY", DeliveryID: emailDelID,
	})
	if err != nil || !expiredPreview.RenderedWithSampleData || !strings.Contains(expiredPreview.HTML, "CS-2026-001") {
		t.Fatalf("expected fallback sample render on expired payload, got %+v err=%v", expiredPreview, err)
	}

	// 5. Secret OTP template: erased immediately on SENT and blocked from Resend; mandatory category cannot be unsubscribed!
	if _, err := svc.UpdateMyPreferences(ctx, tenantID, recipientUser, []Preference{
		{CategoryKey: secCat.CategoryKey, Channel: "EMAIL", Enabled: false},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("disabling mandatory security category must be rejected, got %v", err)
	}

	otpTmpl, _, err := svc.CreateTemplate(ctx, CreateTemplateInput{
		TenantID: tenantID, ActorID: actorID, TemplateKey: "auth.otp",
		ApplicationID: caseApp.ID, CategoryID: secCat.ID, Name: "OTP", DefaultLocale: "vi-VN",
	})
	if err != nil {
		t.Fatal(err)
	}
	otpDraft, err := svc.SaveTemplateVersionDraft(ctx, SaveDraftInput{
		TenantID: tenantID, ActorID: actorID, TemplateID: otpTmpl.ID, SchemaMajor: 1,
		VariableSchemaJSON: []byte(`{
			"type":"object",
			"required":["otp"],
			"properties":{"otp":{"type":"string","secret":true,"example":"000000"}}
		}`),
		Variants: []TemplateVariant{
			{Channel: "EMAIL", Locale: "vi-VN", SubjectTmpl: "OTP", HTMLTmpl: "<p>Code: {{ .data.otp }}</p>"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	otpPub, _, err := svc.PublishTemplateVersion(ctx, PublishVersionInput{
		TenantID: tenantID, ActorID: actorID, TemplateID: otpTmpl.ID, VersionID: otpDraft.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ActivateTemplateRelease(ctx, ActivateReleaseInput{
		TenantID: tenantID, ActorID: actorID, TemplateID: otpTmpl.ID, SchemaMajor: 1, TemplateVersionID: otpPub.ID,
	}); err != nil {
		t.Fatal(err)
	}

	otpSub, err := svc.SubmitNotification(ctx, SubmitInput{
		TenantID: tenantID, ActorID: actorID, IdempotencyKey: "otp-1",
		TemplateKey: "auth.otp", SchemaMajor: 1,
		DataJSON:    []byte(`{"otp":"839201"}`),
		ExpiresAt:   clock.Add(5 * time.Minute),
		Recipients:  []RecipientInput{{Channel: "EMAIL", ExternalAddress: "user@example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	otpDelID := otpSub.Deliveries[0].ID
	if _, err := svc.ProcessQueueBatch(ctx, queue.LaneTransactional, 10, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	otpDel, otpPayload, err := svc.Store().GetDelivery(ctx, tenantID, otpDelID)
	if err != nil || otpDel.Status != "SENT" {
		t.Fatalf("expected OTP delivery SENT, got %+v err=%v", otpDel, err)
	}
	if len(otpPayload.EncryptedSecrets) != 0 || otpPayload.SecretsErasedAt.IsZero() {
		t.Fatalf("expected OTP encrypted_secrets to be wiped immediately upon SENT, got %+v", otpPayload)
	}
	if _, _, err := svc.ResendDelivery(ctx, ResendInput{
		TenantID: tenantID, ActorID: actorID, DeliveryID: otpDelID,
	}); !errors.Is(err, ErrSecretResendForbidden) {
		t.Fatalf("expected ErrSecretResendForbidden on OTP resend, got %v", err)
	}
}
