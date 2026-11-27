package grpcapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"sort"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/crypto"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/queue"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/renderer"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	notificationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/notification/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/actorcontext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func signTestActorProof(t *testing.T, priv ed25519.PrivateKey, tenantID, actorID, commandID string, caps []string, now time.Time) *authv1.ActorProof {
	t.Helper()
	sortedCaps := append([]string(nil), caps...)
	sort.Strings(sortedCaps)
	msg := &authv1.SignedActorContext{
		SchemaVersion:      1,
		TenantId:           tenantID,
		ActorId:            actorID,
		AudienceWorkloadId: "notification-service",
		CommandId:          commandID,
		Roles:              []string{"operator"},
		Capabilities:       sortedCaps,
		IssuedAtEpochMs:    uint64(now.Add(-time.Second).UnixMilli()),
		ExpiresAtEpochMs:   uint64(now.Add(time.Minute).UnixMilli()),
		SigningKeyId:       "test-key",
	}
	unsigned, err := (proto.MarshalOptions{Deterministic: true}).Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(unsigned)
	msg.ContentHash = digest[:]
	msg.Signature = ed25519.Sign(priv, digest[:])
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return &authv1.ActorProof{
		Type:        authv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT,
		SignedProof: raw,
	}
}

func TestGRPCServicesEnforceActorProofAndDispatchEndToEnd(t *testing.T) {
	now := time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := actorcontext.New(actorcontext.Config{
		WorkloadID: "notification-service",
		MaxBytes:   1 << 20, MaxRoles: 32, MaxCaps: 64,
		ClockSkew: 30 * time.Second,
		Keys:      map[string]ed25519.PublicKey{"test-key": pub},
	})
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x55}, 32)
	ring, _ := crypto.NewKeyRing("k1", map[string][]byte{"k1": key}, key)
	svc, err := application.NewService(application.NewMemoryStore(), queue.NewMemoryQueue(), ring, renderer.NewEngine(renderer.DefaultRenderLimits()), nil, application.Config{})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetClock(func() time.Time { return now })

	tenantID := "00000000-0000-0000-0000-000000000010"
	cat, _ := svc.CreateCategory(context.Background(), application.Category{TenantID: tenantID, CategoryKey: "general", Name: "General"})
	app, _ := svc.CreateApplication(context.Background(), application.Application{TenantID: tenantID, AppKey: "portal", Name: "Portal", AllowedNamespaces: []string{"*"}})
	_, _ = svc.RegisterProvider(context.Background(), application.ProviderProfile{TenantID: tenantID, ProviderKey: "inapp", Channel: "IN_APP", DriverKind: "inapp"}, nil, "")

	srv, err := NewServer(svc, verifier)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetClock(func() time.Time { return now })

	// Unauthenticated call fails with Unauthenticated.
	if _, err := srv.ListTemplates(context.Background(), &notificationv1.ListTemplatesRequest{TenantId: tenantID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}

	// Caller with only notification.read cannot CreateTemplate.
	readProof := signTestActorProof(t, priv, tenantID, "actor-1", "cmd-1", []string{"notification.read"}, now)
	if _, err := srv.CreateTemplate(context.Background(), &notificationv1.CreateTemplateRequest{
		TenantId: tenantID, CommandId: "cmd-1", IdempotencyKey: "id-1",
		TemplateKey: "portal.welcome", ApplicationId: app.ID, CategoryId: cat.ID, Name: "Welcome",
		ActorProof: readProof,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}

	// Admin creates, saves draft, publishes, activates, and sends notification!
	adminCaps := []string{"notification.inbox", "notification.manage", "notification.read", "notification.send"}
	tRes, err := srv.CreateTemplate(context.Background(), &notificationv1.CreateTemplateRequest{
		TenantId: tenantID, CommandId: "cmd-create", IdempotencyKey: "id-create",
		TemplateKey: "portal.welcome", ApplicationId: app.ID, CategoryId: cat.ID, Name: "Welcome", DefaultLocale: "vi-VN",
		ActorProof: signTestActorProof(t, priv, tenantID, "actor-1", "cmd-create", adminCaps, now),
	})
	if err != nil {
		t.Fatal(err)
	}
	draftRes, err := srv.SaveTemplateVersionDraft(context.Background(), &notificationv1.SaveTemplateVersionDraftRequest{
		TenantId: tenantID, TemplateId: tRes.GetTemplate().GetId(), SchemaMajor: 1,
		VariableSchemaJson: []byte(`{"type":"object","properties":{"greeting":{"type":"string","example":"Xin chào"}}}`),
		Variants: []*notificationv1.TemplateVariantSpec{{
			Channel: notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_IN_APP, Locale: "vi-VN",
			VariantKind: notificationv1.VariantKind_VARIANT_KIND_INLINE,
			SubjectTmpl: "{{ .data.greeting }}", HtmlTmpl: "<p>{{ .data.greeting }}</p>",
		}},
		CommandId: "cmd-draft", IdempotencyKey: "id-draft",
		ActorProof: signTestActorProof(t, priv, tenantID, "actor-1", "cmd-draft", adminCaps, now),
	})
	if err != nil {
		t.Fatal(err)
	}
	pubRes, err := srv.PublishTemplateVersion(context.Background(), &notificationv1.PublishTemplateVersionRequest{
		TenantId: tenantID, TemplateId: tRes.GetTemplate().GetId(), VersionId: draftRes.GetVersion().GetId(),
		CommandId: "cmd-pub", IdempotencyKey: "id-pub",
		ActorProof: signTestActorProof(t, priv, tenantID, "actor-1", "cmd-pub", adminCaps, now),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.ActivateTemplateRelease(context.Background(), &notificationv1.ActivateTemplateReleaseRequest{
		TenantId: tenantID, TemplateId: tRes.GetTemplate().GetId(), SchemaMajor: 1, TemplateVersionId: pubRes.GetVersion().GetId(),
		CommandId: "cmd-rel", IdempotencyKey: "id-rel",
		ActorProof: signTestActorProof(t, priv, tenantID, "actor-1", "cmd-rel", adminCaps, now),
	}); err != nil {
		t.Fatal(err)
	}

	subRes, err := srv.SubmitNotification(context.Background(), &notificationv1.SubmitNotificationRequest{
		TenantId: tenantID, CommandId: "cmd-send", IdempotencyKey: "id-send",
		TemplateKey: "portal.welcome", SchemaMajor: 1,
		DataJson: []byte(`{"greeting":"Chào mừng bạn"}`),
		Recipients: []*notificationv1.RecipientTarget{{
			SubjectRef: "actor-1", Channel: notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_IN_APP,
		}},
		ActorProof: signTestActorProof(t, priv, tenantID, "actor-1", "cmd-send", adminCaps, now),
	})
	if err != nil || len(subRes.GetDeliveries()) != 1 {
		t.Fatalf("submit failed: %+v err=%v", subRes, err)
	}
	_, _ = svc.ProcessQueueBatch(context.Background(), queue.LaneNormal, 10, 30*time.Second)

	inboxRes, err := srv.ListInAppMessages(context.Background(), &notificationv1.ListInAppMessagesRequest{
		TenantId: tenantID, UnreadOnly: true,
		ActorProof: signTestActorProof(t, priv, tenantID, "actor-1", "corr-1", adminCaps, now),
	})
	if err != nil || inboxRes.GetUnreadCount() != 1 {
		t.Fatalf("expected 1 unread message, got %+v err=%v", inboxRes, err)
	}
}

func TestNewServerRejectsNilProofVerifier(t *testing.T) {
	key := bytes.Repeat([]byte{0x55}, 32)
	ring, err := crypto.NewKeyRing("k1", map[string][]byte{"k1": key}, key)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := application.NewService(application.NewMemoryStore(), queue.NewMemoryQueue(), ring, renderer.NewEngine(renderer.DefaultRenderLimits()), nil, application.Config{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewServer(svc, nil); err == nil {
		t.Fatal("expected NewServer to reject nil proof verifier (fail-closed)")
	}
}

func TestVerifyActorFailsClosedWithoutVerifier(t *testing.T) {
	srv := &Server{}
	_, err := srv.verifyActor("00000000-0000-0000-0000-000000000010", "cmd-1", nil, "notification.manage")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated when verifier is nil, got %v", err)
	}
}
