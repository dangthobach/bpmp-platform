package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/crypto"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/queue"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/renderer"
)

func newExtTestService(t *testing.T, env string) (*Service, *MemoryStore, *queue.MemoryQueue) {
	t.Helper()
	store := NewMemoryStore()
	q := queue.NewMemoryQueue()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	ring, err := crypto.NewKeyRing("test-key", map[string][]byte{"test-key": key}, key)
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	svc, err := NewService(store, q, ring, renderer.NewEngine(renderer.DefaultRenderLimits()), nil, Config{
		Environment: env,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, store, q
}

func TestDryRunPlanZeroWritesAndPriorityOrdering(t *testing.T) {
	ctx := context.Background()
	svc, store, q := newExtTestService(t, "development")
	tenantID := "00000000-0000-7000-8000-000000000001"

	cat, err := svc.CreateCategory(ctx, Category{TenantID: tenantID, CategoryKey: "transactional", Name: "Transactional", DefaultLane: "TRANSACTIONAL"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := svc.CreateApplication(ctx, Application{
		TenantID: tenantID, AppKey: "loan-app", Name: "Loan App",
		AllowedNamespaces: []string{"loan.*"}, QuotaConfig: QuotaConfig{MaxPerMinute: 10, MaxPerDay: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	prov, err := svc.RegisterProvider(ctx, ProviderProfile{
		TenantID: tenantID, ProviderKey: "mock-email", Channel: "EMAIL", DriverKind: "mock",
	}, []byte(`{}`), "")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, _, err := svc.CreateTemplate(ctx, CreateTemplateInput{
		TenantID: tenantID, TemplateKey: "loan.approved", ApplicationID: app.ID, CategoryID: cat.ID, Name: "Loan Approved", DefaultLocale: "vi-VN",
	})
	if err != nil {
		t.Fatal(err)
	}
	schemaJSON := []byte(`{"type":"object","required":["customerName"],"properties":{"customerName":{"type":"string","example":"Bach"}}}`)
	tv, err := svc.SaveTemplateVersionDraft(ctx, SaveDraftInput{
		TenantID: tenantID, TemplateID: tmpl.ID, SchemaMajor: 1, Syntax: renderer.SyntaxHandlebarsV1,
		VariableSchemaJSON: schemaJSON, ChangeNote: "initial hbs",
		Variants: []TemplateVariant{{Channel: "EMAIL", Locale: "vi-VN", VariantKind: "INLINE", SubjectTmpl: "Hi {{customerName}}", HTMLTmpl: "<p>Hello {{customerName}}</p>"}},
	})
	if err != nil {
		t.Fatalf("save handlebars draft: %v", err)
	}
	pub, _, err := svc.PublishTemplateVersion(ctx, PublishVersionInput{TenantID: tenantID, TemplateID: tmpl.ID, VersionID: tv.ID})
	if err != nil {
		t.Fatalf("publish handlebars version: %v", err)
	}
	rel, _, err := svc.ActivateTemplateRelease(ctx, ActivateReleaseInput{
		TenantID: tenantID, TemplateID: tmpl.ID, SchemaMajor: 1, TemplateVersionID: pub.ID,
	})
	if err != nil {
		t.Fatalf("activate release: %v", err)
	}

	// Dry-run PlanNotification must perform ZERO DB writes and ZERO queue enqueue
	plan, err := svc.PlanNotification(ctx, SubmitInput{
		TenantID: tenantID, TemplateKey: "loan.approved", SchemaMajor: 1,
		DataJSON:   []byte(`{"customerName":"Alice"}`),
		Recipients: []RecipientInput{{Channel: "EMAIL", ExternalAddress: "alice@example.com"}},
	})
	if err != nil {
		t.Fatalf("plan notification: %v", err)
	}
	if plan.TemplateVersionID != pub.ID || plan.ReleaseOrdinal != rel.Ordinal || plan.SelectedProvider != prov.ID {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if len(plan.SchemaErrors) != 0 || plan.QuotaRemainingMinute != 10 {
		t.Fatalf("unexpected schema/quota in plan: %+v", plan)
	}
	dels, _, _, _ := store.QueryDeliveries(ctx, DeliveryQuery{TenantID: tenantID})
	if len(dels) != 0 {
		t.Fatalf("dry-run wrote deliveries to store: %d", len(dels))
	}
	claimed, _ := q.ClaimBatch(ctx, queue.LaneTransactional, 10, time.Minute, time.Now().UTC())
	if len(claimed) != 0 {
		t.Fatalf("dry-run enqueued jobs: %d", len(claimed))
	}

	// Submit low priority (3) first, then urgent priority (1) -> queue must claim priority 1 first!
	resLow, err := svc.SubmitNotification(ctx, SubmitInput{
		TenantID: tenantID, IdempotencyKey: "k-low", TemplateKey: "loan.approved", SchemaMajor: 1, Priority: 3,
		DataJSON: []byte(`{"customerName":"Low"}`), Recipients: []RecipientInput{{Channel: "EMAIL", ExternalAddress: "low@example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resUrgent, err := svc.SubmitNotification(ctx, SubmitInput{
		TenantID: tenantID, IdempotencyKey: "k-urgent", TemplateKey: "loan.approved", SchemaMajor: 1, Priority: 1,
		DataJSON: []byte(`{"customerName":"Urgent"}`), Recipients: []RecipientInput{{Channel: "EMAIL", ExternalAddress: "urgent@example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := q.ClaimBatch(ctx, queue.LaneTransactional, 2, time.Minute, time.Now().UTC().Add(time.Second))
	if err != nil || len(jobs) != 2 {
		t.Fatalf("claim batch: %v (len=%d)", err, len(jobs))
	}
	if jobs[0].DeliveryID.String() != resUrgent.Deliveries[0].ID || jobs[0].Priority != 1 {
		t.Fatalf("expected priority 1 job first, got %+v", jobs[0])
	}
	if jobs[1].DeliveryID.String() != resLow.Deliveries[0].ID || jobs[1].Priority != 3 {
		t.Fatalf("expected priority 3 job second, got %+v", jobs[1])
	}
}

func TestBatchActionsSoftDeleteRestoreAndVariableMapping(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newExtTestService(t, "development")
	tenantID := "00000000-0000-7000-8000-000000000002"

	cat, _ := svc.CreateCategory(ctx, Category{TenantID: tenantID, CategoryKey: "general", Name: "General"})
	app, _ := svc.CreateApplication(ctx, Application{TenantID: tenantID, AppKey: "app1", Name: "App 1", AllowedNamespaces: []string{"*"}})
	t1, _, _ := svc.CreateTemplate(ctx, CreateTemplateInput{TenantID: tenantID, TemplateKey: "loan.ok", ApplicationID: app.ID, CategoryID: cat.ID, Name: "Loan OK"})
	t2, _, _ := svc.CreateTemplate(ctx, CreateTemplateInput{TenantID: tenantID, TemplateKey: "loan.draft", ApplicationID: app.ID, CategoryID: cat.ID, Name: "Loan Draft"})

	// Publish & activate t1 so DELETE must refuse t1 while allowing t2
	tv1, err := svc.SaveTemplateVersionDraft(ctx, SaveDraftInput{
		TenantID: tenantID, TemplateID: t1.ID, SchemaMajor: 1, Syntax: renderer.SyntaxHandlebarsV1,
		VariableSchemaJSON: []byte(`{"type":"object","properties":{"customerName":{"type":"string"},"amount":{"type":"boolean"},"missingVar":{"type":"string"}}}`),
		Variants:           []TemplateVariant{{Channel: "EMAIL", Locale: "vi-VN", VariantKind: "INLINE", SubjectTmpl: "Hi {{customerName}}", HTMLTmpl: "<p>{{customerName}}</p>"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pub1, _, err := svc.PublishTemplateVersion(ctx, PublishVersionInput{TenantID: tenantID, TemplateID: t1.ID, VersionID: tv1.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ActivateTemplateRelease(ctx, ActivateReleaseInput{TenantID: tenantID, TemplateID: t1.ID, SchemaMajor: 1, TemplateVersionID: pub1.ID}); err != nil {
		t.Fatal(err)
	}

	// Tag batch action
	tagRes, err := svc.BatchTemplateActions(ctx, BatchTemplateActionsInput{
		TenantID: tenantID, Action: "TAGS", IDs: []string{t1.ID, t2.ID}, TagMode: "add", Tags: []string{"finance", "vip"},
	})
	if err != nil || !tagRes[0].OK || !tagRes[1].OK {
		t.Fatalf("batch tags failed: %v %+v", err, tagRes)
	}
	tags, _ := svc.ListTemplateTags(ctx, tenantID)
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %v", tags)
	}

	// Delete batch action: t1 refused (active release), t2 soft-deleted
	delRes, err := svc.BatchTemplateActions(ctx, BatchTemplateActionsInput{
		TenantID: tenantID, Action: "DELETE", IDs: []string{t1.ID, t2.ID}, Reason: "cleanup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if delRes[0].OK || !delRes[1].OK {
		t.Fatalf("expected t1 refused and t2 deleted, got %+v", delRes)
	}
	if _, err := svc.Store().GetTemplate(ctx, tenantID, t2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected soft-deleted t2 hidden from GetTemplate, got %v", err)
	}
	if _, err := svc.RestoreTemplate(ctx, tenantID, "admin", "ADMIN", "127.0.0.1", "cmd-1", t2.ID); err != nil {
		t.Fatalf("restore template: %v", err)
	}
	if _, err := svc.Store().GetTemplate(ctx, tenantID, t2.ID); err != nil {
		t.Fatalf("expected restored t2 visible, got %v", err)
	}

	// Trigger rule & Variable Mapping
	rule, err := svc.CreateTriggerRule(ctx, CreateTriggerRuleInput{
		TenantID: tenantID, RuleKey: "r-loan", WorkflowType: "loan.approval", TriggerKind: "STEP_COMPLETED", TemplateID: t1.ID, SchemaMajor: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	rev, err := svc.SaveTriggerRuleRevision(ctx, SaveTriggerRuleRevisionInput{
		TenantID: tenantID, RuleID: rule.ID,
		DataMappingCEL: map[string]string{
			"customerName": "vars.customerName", // OK
			"amount":       "vars.amount",       // TYPE_MISMATCH (money vs boolean)
			"extraVar":     "vars.branchName",   // UNDECLARED_IN_TEMPLATE
			"ghostVar":     "vars.nonExistent",  // MISSING_IN_CONTEXT
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ActivateTriggerRuleRevision(ctx, tenantID, "admin", rule.ID, rev.Revision); err != nil {
		t.Fatal(err)
	}
	vm, err := svc.QueryVariableMapping(ctx, tenantID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	reasons := make(map[string]bool)
	for _, l := range vm.Links {
		reasons[l.Reason] = true
	}
	for _, want := range []string{"OK", "TYPE_MISMATCH", "UNDECLARED_IN_TEMPLATE", "MISSING_IN_CONTEXT", "UNUSED_EXPORT", "UNBOUND_PROCESS"} {
		if !reasons[want] {
			t.Fatalf("expected variable mapping reason %s in %+v", want, vm.Links)
		}
	}
}

func TestBreakerTransitionsProbeAndSimulateFailureGuards(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newExtTestService(t, "development")
	tenantID := "00000000-0000-7000-8000-000000000003"

	mockProv, _ := svc.RegisterProvider(ctx, ProviderProfile{TenantID: tenantID, ProviderKey: "mock-1", Channel: "EMAIL", DriverKind: "mock"}, []byte(`{}`), "")
	smtpProv, _ := svc.RegisterProvider(ctx, ProviderProfile{TenantID: tenantID, ProviderKey: "smtp-1", Channel: "EMAIL", DriverKind: "smtp"}, []byte(`{}`), "")

	// Simulate failure must reject non-mock driver
	if _, err := svc.SimulateProviderFailure(ctx, SimulateFailureInput{TenantID: tenantID, ProviderID: smtpProv.ID, Count: 5}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for non-mock simulate-failure, got %v", err)
	}

	// Simulate failure on mock driver trips breaker to OPEN and records transition
	view, err := svc.SimulateProviderFailure(ctx, SimulateFailureInput{TenantID: tenantID, ProviderID: mockProv.ID, Count: 5})
	if err != nil {
		t.Fatalf("simulate failure on mock: %v", err)
	}
	if view.Breaker.BreakerState != "OPEN" {
		t.Fatalf("expected OPEN breaker after spike, got %s", view.Breaker.BreakerState)
	}

	// Probe transitions to HALF_OPEN
	probed, err := svc.ProbeBreaker(ctx, tenantID, "ops", "OPS", "10.0.0.1", "cmd-p", mockProv.ID, "testing recovery")
	if err != nil || probed.Breaker.BreakerState != "HALF_OPEN" {
		t.Fatalf("expected HALF_OPEN after probe, got %v %+v", err, probed)
	}

	transitions, err := svc.ListBreakerTransitions(ctx, tenantID, mockProv.ID, 10)
	if err != nil || len(transitions) < 2 {
		t.Fatalf("expected >= 2 breaker transitions, got %d (%v)", len(transitions), err)
	}

	// Production guard rejects simulate-failure even on mock provider
	svc.SetEnvironment("production")
	if _, err := svc.SimulateProviderFailure(ctx, SimulateFailureInput{TenantID: tenantID, ProviderID: mockProv.ID, Count: 5}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden in production, got %v", err)
	}
}
