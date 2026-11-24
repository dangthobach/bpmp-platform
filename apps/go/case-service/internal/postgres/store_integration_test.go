package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/kafkapublisher"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func TestCreateCaseIdempotencyAndTransactionalOutbox(t *testing.T) {
	dsn := os.Getenv("CASE_SERVICE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CASE_SERVICE_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "case_it_" + uuid.NewString()[:8]
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve migration path")
	}
	for _, name := range []string{"001_case_foundation.sql", "002_outbox_delivery.sql", "003_case_search.sql", "004_engine_event_inbox.sql"} {
		migration := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../../../db/case-service/migrations", name))
		contents, readErr := os.ReadFile(migration)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(contents)); err != nil {
			t.Fatal(err)
		}
	}
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(store, time.Now, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	tenant := uuid.New()
	request := application.CreateRequest{TenantID: tenant, ActorID: "alice", IdempotencyKey: "create-1", CorrelationID: "corr-1", CustomerRef: "customer-1", WorkflowType: "approval", WorkflowVersion: "1"}
	const callers = 8
	results := make([]application.CreateResult, callers)
	errorsByCaller := make([]error, callers)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errorsByCaller[i] = service.Create(ctx, request) }(i)
	}
	wg.Wait()
	var firstID uuid.UUID
	fresh := 0
	for i, result := range results {
		if errorsByCaller[i] != nil {
			t.Fatalf("caller %d: %v", i, errorsByCaller[i])
		}
		if firstID == uuid.Nil {
			firstID = result.Case.ID
		}
		if result.Case.ID != firstID || result.Case.OrchestrationStatus != "START_PENDING" || result.Case.Version != 1 {
			t.Fatalf("different Case or invalid initial state: %+v", result)
		}
		if !result.Duplicate {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("expected one fresh Case and seven replays; got %d", fresh)
	}
	var caseCount, receiptCount, eventCount, jobCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM customer_cases WHERE tenant_id=$1`, tenant).Scan(&caseCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM case_create_receipts WHERE tenant_id=$1`, tenant).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM case_outbox WHERE tenant_id=$1 AND event_type='CaseCreated' AND published_at IS NULL`, tenant).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM workflow_start_jobs WHERE tenant_id=$1 AND case_id=$2 AND command_id=$2 AND status='PENDING'`, tenant, firstID).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if caseCount != 1 || receiptCount != 1 || eventCount != 1 || jobCount != 1 {
		t.Fatalf("transactional effect counts: cases=%d receipts=%d events=%d jobs=%d", caseCount, receiptCount, eventCount, jobCount)
	}
	changed := request
	changed.CustomerRef = "customer-2"
	if _, err = service.Create(ctx, changed); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("changed intent reused key: %v", err)
	}
	if _, err = service.Get(ctx, uuid.New(), firstID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-tenant read succeeded: %v", err)
	}
	caseView, err := service.Get(ctx, tenant, firstID)
	if err != nil || caseView.CustomerRef != request.CustomerRef {
		t.Fatalf("tenant read failed: case=%+v err=%v", caseView, err)
	}
	var persistedDigestBytes int
	if err = pool.QueryRow(ctx, `SELECT octet_length(request_digest) FROM case_create_receipts WHERE tenant_id=$1`, tenant).Scan(&persistedDigestBytes); err != nil {
		t.Fatal(err)
	}
	if persistedDigestBytes != 32 {
		t.Fatal(fmt.Errorf("wrong receipt digest length: %d", persistedDigestBytes))
	}
	claimAt := time.Now().UTC().Add(time.Second)
	firstLease, found, err := store.ClaimStart(ctx, claimAt, 30*time.Second, uuid.New())
	if err != nil || !found || firstLease.CommandID != firstID || firstLease.Attempts != 1 {
		t.Fatalf("first workflow start lease: job=%+v found=%v err=%v", firstLease, found, err)
	}
	if _, found, err = store.ClaimStart(ctx, claimAt, 30*time.Second, uuid.New()); err != nil || found {
		t.Fatalf("leased job was claimed twice: found=%v err=%v", found, err)
	}
	retryAt := claimAt.Add(2 * time.Second)
	if err = store.RetryStart(ctx, firstLease, claimAt.Add(time.Second), retryAt, "ENGINE_OUTCOME_UNKNOWN"); err != nil {
		t.Fatal(err)
	}
	caseView, err = service.Get(ctx, tenant, firstID)
	if err != nil || caseView.OrchestrationStatus != "RECONCILING" || caseView.Version != 2 {
		t.Fatalf("ambiguous Engine outcome was not visible: case=%+v err=%v", caseView, err)
	}
	secondLease, found, err := store.ClaimStart(ctx, retryAt.Add(time.Second), 30*time.Second, uuid.New())
	if err != nil || !found || secondLease.CommandID != firstLease.CommandID || secondLease.CaseID != firstLease.CaseID || secondLease.Attempts != 2 {
		t.Fatalf("retry changed Engine command identity: job=%+v found=%v err=%v", secondLease, found, err)
	}
	if err = store.CompleteStart(ctx, firstLease, retryAt.Add(2*time.Second), uuid.New()); !errors.Is(err, application.ErrLeaseLost) {
		t.Fatalf("stale worker completed workflow: %v", err)
	}
	if err = store.CompleteStart(ctx, secondLease, retryAt.Add(2*time.Second), uuid.New()); err != nil {
		t.Fatal(err)
	}
	caseView, err = service.Get(ctx, tenant, firstID)
	if err != nil || caseView.OrchestrationStatus != "ACTIVE" || caseView.WorkflowInstanceID != firstID.String() || caseView.Version != 3 {
		t.Fatalf("workflow start did not reconcile: case=%+v err=%v", caseView, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM case_outbox WHERE tenant_id=$1`, tenant).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 3 {
		t.Fatalf("expected CaseCreated, CaseWorkflowReconciling and CaseWorkflowStarted; got %d", eventCount)
	}
	engineEvents, err := application.NewEngineEventService(store)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	startedEvent := application.EngineEvent{TenantID: tenant, InstanceID: firstID.String(), EventID: "engine-start-1",
		WorkflowType: request.WorkflowType, WorkflowVersion: request.WorkflowVersion, Sequence: 1,
		EventType: "workflow_started", OccurredAt: startedAt, PayloadDigest: sha256.Sum256([]byte("started"))}
	if applied, applyErr := engineEvents.Apply(ctx, startedEvent); applyErr != nil || !applied {
		t.Fatalf("apply Engine start event: applied=%v err=%v", applied, applyErr)
	}
	if applied, applyErr := engineEvents.Apply(ctx, startedEvent); applyErr != nil || applied {
		t.Fatalf("duplicate Engine event was not deduplicated: applied=%v err=%v", applied, applyErr)
	}
	conflictingDuplicate := startedEvent
	conflictingDuplicate.PayloadDigest = sha256.Sum256([]byte("changed payload"))
	if _, applyErr := engineEvents.Apply(ctx, conflictingDuplicate); !errors.Is(applyErr, ErrEngineEventConflict) {
		t.Fatalf("same event identity with changed payload was accepted: %v", applyErr)
	}
	conflictingSequence := startedEvent
	conflictingSequence.EventID = "engine-other-at-1"
	conflictingSequence.PayloadDigest = sha256.Sum256([]byte("second event at sequence one"))
	if _, applyErr := engineEvents.Apply(ctx, conflictingSequence); !errors.Is(applyErr, ErrEngineEventConflict) {
		t.Fatalf("two Engine event IDs claimed one stream sequence: %v", applyErr)
	}
	terminalEvent := application.EngineEvent{TenantID: tenant, InstanceID: firstID.String(), EventID: "engine-complete-3",
		WorkflowType: request.WorkflowType, WorkflowVersion: request.WorkflowVersion, Sequence: 3,
		EventType: "workflow_completed", OccurredAt: startedAt.Add(time.Minute), PayloadDigest: sha256.Sum256([]byte("completed"))}
	if applied, applyErr := engineEvents.Apply(ctx, terminalEvent); applyErr != nil || !applied {
		t.Fatalf("apply Engine completion: applied=%v err=%v", applied, applyErr)
	}
	lateEvent := application.EngineEvent{TenantID: tenant, InstanceID: firstID.String(), EventID: "engine-late-2",
		WorkflowType: request.WorkflowType, WorkflowVersion: request.WorkflowVersion, Sequence: 2,
		EventType: "user_task_activated", OccurredAt: startedAt.Add(30 * time.Second), PayloadDigest: sha256.Sum256([]byte("late"))}
	if applied, applyErr := engineEvents.Apply(ctx, lateEvent); applyErr != nil || !applied {
		t.Fatalf("store reordered Engine event: applied=%v err=%v", applied, applyErr)
	}
	engineView, err := service.GetView(ctx, tenant, firstID)
	if err != nil || engineView.Case.OrchestrationStatus != "CLOSED" || engineView.EngineSequence != 3 {
		t.Fatalf("out-of-order Engine event regressed Case process state: view=%+v err=%v", engineView, err)
	}
	if applied, applyErr := engineEvents.Apply(ctx, application.EngineEvent{TenantID: tenant, InstanceID: firstID.String(),
		EventID: "invalid-reopen", WorkflowType: request.WorkflowType, WorkflowVersion: request.WorkflowVersion,
		Sequence: 4, EventType: "workflow_started", OccurredAt: startedAt.Add(2 * time.Minute), PayloadDigest: sha256.Sum256([]byte("reopen"))}); applyErr != nil || !applied {
		t.Fatalf("apply later Engine start record: applied=%v err=%v", applied, applyErr)
	}
	engineView, err = service.GetView(ctx, tenant, firstID)
	if err != nil || engineView.Case.OrchestrationStatus != "CLOSED" || engineView.EngineSequence != 4 {
		t.Fatalf("terminal Case was reopened by a later start event: view=%+v err=%v", engineView, err)
	}
	processPage, err := service.ProcessEvents(ctx, tenant, firstID, 0, 2)
	if err != nil || len(processPage.Events) != 2 || processPage.Events[0].Sequence != 1 ||
		processPage.Events[1].Sequence != 2 || processPage.NextAfterSequence != 2 {
		t.Fatalf("Engine process timeline did not paginate in sequence order: page=%+v err=%v", processPage, err)
	}
	processPage, err = service.ProcessEvents(ctx, tenant, firstID, processPage.NextAfterSequence, 2)
	if err != nil || len(processPage.Events) != 2 || processPage.Events[0].Sequence != 3 || processPage.Events[1].Sequence != 4 {
		t.Fatalf("Engine process timeline continuation failed: page=%+v err=%v", processPage, err)
	}
	if _, err = service.ProcessEvents(ctx, uuid.New(), firstID, 0, 10); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-tenant process timeline succeeded: %v", err)
	}
	if applied, applyErr := engineEvents.Apply(ctx, application.EngineEvent{TenantID: uuid.New(), InstanceID: firstID.String(),
		EventID: "other-tenant-event", WorkflowType: request.WorkflowType, WorkflowVersion: request.WorkflowVersion,
		Sequence: 1, EventType: "workflow_started", OccurredAt: startedAt, PayloadDigest: sha256.Sum256([]byte("foreign"))}); applyErr != nil || applied {
		t.Fatalf("foreign tenant event changed a Case: applied=%v err=%v", applied, applyErr)
	}
	if applied, applyErr := engineEvents.Apply(ctx, application.EngineEvent{TenantID: tenant, InstanceID: "non-case-instance",
		EventID: "other-workflow-event", WorkflowType: "other-workflow", WorkflowVersion: "1",
		Sequence: 1, EventType: "workflow_started", OccurredAt: startedAt, PayloadDigest: sha256.Sum256([]byte("other workflow"))}); applyErr != nil || applied {
		t.Fatalf("non-Case Engine instance was not ignored: applied=%v err=%v", applied, applyErr)
	}
	if _, applyErr := engineEvents.Apply(ctx, application.EngineEvent{TenantID: tenant, InstanceID: firstID.String(),
		EventID: "wrong-definition", WorkflowType: "other-workflow", WorkflowVersion: request.WorkflowVersion,
		Sequence: 4, EventType: "workflow_completed", OccurredAt: startedAt, PayloadDigest: sha256.Sum256([]byte("wrong"))}); !errors.Is(applyErr, ErrEngineEventConflict) {
		t.Fatalf("wrong workflow definition was accepted: %v", applyErr)
	}
	// A leased event blocks later versions for the same Case, even if another worker is ready.
	eventNow := time.Now().UTC().Add(2 * time.Second)
	firstEvent, found, err := store.ClaimEvent(ctx, eventNow, 30*time.Second, uuid.New())
	if err != nil || !found || firstEvent.AggregateVersion != 1 || firstEvent.EventType != "CaseCreated" {
		t.Fatalf("claim first event: event=%+v found=%v err=%v", firstEvent, found, err)
	}
	if _, found, err = store.ClaimEvent(ctx, eventNow, 30*time.Second, uuid.New()); err != nil || found {
		t.Fatalf("later version bypassed leased event: found=%v err=%v", found, err)
	}
	if err = store.RetryEvent(ctx, firstEvent, eventNow.Add(time.Second), eventNow.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, found, err = store.ClaimEvent(ctx, eventNow.Add(2*time.Second), 30*time.Second, uuid.New()); err != nil || found {
		t.Fatalf("later version bypassed retry delay: found=%v err=%v", found, err)
	}
	retryEvent, found, err := store.ClaimEvent(ctx, eventNow.Add(4*time.Second), 30*time.Second, uuid.New())
	if err != nil || !found || retryEvent.EventID != firstEvent.EventID || retryEvent.Attempts != 2 {
		t.Fatalf("retry changed event identity: event=%+v found=%v err=%v", retryEvent, found, err)
	}
	if err = store.MarkPublished(ctx, firstEvent, eventNow.Add(5*time.Second)); !errors.Is(err, application.ErrLeaseLost) {
		t.Fatalf("stale event worker acknowledged: %v", err)
	}
	if err = store.MarkPublished(ctx, retryEvent, eventNow.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	secondEvent, found, err := store.ClaimEvent(ctx, eventNow.Add(6*time.Second), 30*time.Second, uuid.New())
	if err != nil || !found || secondEvent.AggregateVersion != 2 {
		t.Fatalf("second event unavailable: event=%+v found=%v err=%v", secondEvent, found, err)
	}
	// Exhaustion must be visible from the Case aggregate, not only the private job row.
	failingRequest := request
	failingRequest.IdempotencyKey = "create-failing"
	failing, err := service.Create(ctx, failingRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE workflow_start_jobs SET attempts=7 WHERE tenant_id=$1 AND case_id=$2`, tenant, failing.Case.ID); err != nil {
		t.Fatal(err)
	}
	failedJob, found, err := store.ClaimStart(ctx, eventNow.Add(7*time.Second), 30*time.Second, uuid.New())
	if err != nil || !found || failedJob.CaseID != failing.Case.ID || failedJob.Attempts != 8 {
		t.Fatalf("claim last start attempt: job=%+v found=%v err=%v", failedJob, found, err)
	}
	if err = store.RetryStart(ctx, failedJob, eventNow.Add(8*time.Second), eventNow.Add(9*time.Second), "ENGINE_OUTCOME_UNKNOWN"); err != nil {
		t.Fatal(err)
	}
	failingView, err := service.Get(ctx, tenant, failing.Case.ID)
	if err != nil || failingView.OrchestrationStatus != "START_FAILED" || failingView.Version != 2 {
		t.Fatalf("manual review state missing: case=%+v err=%v", failingView, err)
	}
	var terminalEventCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM case_outbox WHERE tenant_id=$1 AND aggregate_id=$2
		AND event_type='CaseWorkflowStartFailed' AND aggregate_version=2`, tenant, failing.Case.ID).Scan(&terminalEventCount); err != nil || terminalEventCount != 1 {
		t.Fatalf("terminal event missing: count=%d err=%v", terminalEventCount, err)
	}
	preSubmitRequest := request
	preSubmitRequest.IdempotencyKey = "create-authz-unavailable"
	preSubmit, err := service.Create(ctx, preSubmitRequest)
	if err != nil {
		t.Fatal(err)
	}
	preSubmitJob, found, err := store.ClaimStart(ctx, eventNow.Add(10*time.Second), 30*time.Second, uuid.New())
	if err != nil || !found || preSubmitJob.CaseID != preSubmit.Case.ID {
		t.Fatalf("claim pre-submit job: job=%+v found=%v err=%v", preSubmitJob, found, err)
	}
	if err = store.RetryStart(ctx, preSubmitJob, eventNow.Add(11*time.Second), eventNow.Add(12*time.Second), "START_INPUT_UNAVAILABLE"); err != nil {
		t.Fatal(err)
	}
	preSubmitView, err := service.Get(ctx, tenant, preSubmit.Case.ID)
	if err != nil || preSubmitView.OrchestrationStatus != "START_PENDING" || preSubmitView.Version != 1 {
		t.Fatalf("pre-submit failure falsely reconciled Engine: case=%+v err=%v", preSubmitView, err)
	}
	view, err := service.GetView(ctx, tenant, firstID)
	if err != nil || view.Case.Version != 3 || view.StartStatus != "STARTED" || view.PublishedVersion != 1 || view.AsOf.IsZero() {
		t.Fatalf("Case view lost start or delivery watermark: view=%+v err=%v", view, err)
	}
	if _, err = service.GetView(ctx, uuid.New(), firstID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-tenant Case view succeeded: %v", err)
	}
	timeline, err := service.Timeline(ctx, tenant, firstID, 0, 10)
	if err != nil || len(timeline) != 3 || timeline[0].AggregateVersion != 1 ||
		timeline[1].AggregateVersion != 2 || timeline[2].AggregateVersion != 3 || timeline[0].PublishedAt == nil {
		t.Fatalf("Case timeline is incomplete: entries=%+v err=%v", timeline, err)
	}
	if _, err = service.Timeline(ctx, uuid.New(), firstID, 0, 10); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-tenant timeline succeeded: %v", err)
	}
	seen := map[uuid.UUID]bool{}
	var beforeAt time.Time
	var beforeID uuid.UUID
	for len(seen) < 3 {
		page, searchErr := service.Search(ctx, tenant, request.CustomerRef, beforeAt, beforeID, 1)
		if searchErr != nil || len(page.Cases) != 1 || seen[page.Cases[0].ID] {
			t.Fatalf("Case keyset search failed: page=%+v err=%v", page, searchErr)
		}
		seen[page.Cases[0].ID] = true
		if page.NextCreatedAt == nil {
			break
		}
		beforeAt, beforeID = *page.NextCreatedAt, page.NextID
	}
	if len(seen) != 3 {
		t.Fatalf("Case search lost a result: %v", seen)
	}
	otherTenantPage, err := service.Search(ctx, uuid.New(), request.CustomerRef, time.Time{}, uuid.Nil, 10)
	if err != nil || len(otherTenantPage.Cases) != 0 {
		t.Fatalf("cross-tenant search leaked cases: page=%+v err=%v", otherTenantPage, err)
	}
	if broker := os.Getenv("CASE_SERVICE_KAFKA_BROKER"); broker != "" {
		const topic = "bpmp.case.events.v1.test"
		producerConfig := kafkaconfig.Producer{
			Bootstrap: kafkaconfig.Bootstrap{Brokers: []string{broker}, ClientID: "bpmp-case-outbox-pg-it",
				SecurityProtocol: kafkaconfig.ProtocolPlaintext, DialTimeoutMS: 2000, RequestTimeoutMS: 2000},
			Topic: topic, MessageTimeoutMS: 5000, MaxInflight: 1, MaxMessageBytes: 4096,
			RequiredAcks: "ALL", EnableIdempotence: true,
		}
		client, clientErr := kafkaconfig.NewProducerClient(producerConfig)
		if clientErr != nil {
			t.Fatal(clientErr)
		}
		defer client.Close()
		create := kmsg.NewPtrCreateTopicsRequest()
		create.Topics = []kmsg.CreateTopicsRequestTopic{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}}
		response, requestErr := client.Request(ctx, create)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		created := response.(*kmsg.CreateTopicsResponse)
		if len(created.Topics) != 1 {
			t.Fatalf("unexpected topic response: %+v", created.Topics)
		}
		if topicErr := kerr.ErrorForCode(created.Topics[0].ErrorCode); topicErr != nil && topicErr != kerr.TopicAlreadyExists {
			t.Fatal(topicErr)
		}
		publisher, publisherErr := kafkapublisher.New(client, producerConfig)
		if publisherErr != nil {
			t.Fatal(publisherErr)
		}
		runner, runnerErr := application.NewOutboxRunner(store, publisher, time.Now, uuid.New)
		if runnerErr != nil {
			t.Fatal(runnerErr)
		}
		processed, runErr := runner.RunOnce(ctx)
		if runErr != nil || !processed {
			t.Fatalf("broker-backed outbox run: processed=%v err=%v", processed, runErr)
		}
		var publishedCount int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM case_outbox WHERE tenant_id=$1 AND aggregate_id=$2
			AND aggregate_version=1 AND published_at IS NOT NULL`, tenant, failing.Case.ID).Scan(&publishedCount); err != nil || publishedCount != 1 {
			t.Fatalf("broker ACK did not mark event published: count=%d err=%v", publishedCount, err)
		}
	}
}
