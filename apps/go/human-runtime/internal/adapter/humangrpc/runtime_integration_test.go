package humangrpc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/postgres"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type runtimeHistoryReader struct {
	page *enginev1.ReadHistoryPageResponse
}

func (r runtimeHistoryReader) ReadHistoryPage(_ context.Context, tenantID, instanceID string, after uint64, pageSize uint32) (*enginev1.ReadHistoryPageResponse, error) {
	if tenantID != "tenant-a" || instanceID != "instance-1" || after != 0 || pageSize == 0 {
		return nil, fmt.Errorf("unexpected history scope or page: tenant=%s instance=%s after=%d size=%d", tenantID, instanceID, after, pageSize)
	}
	return r.page, nil
}

func TestBulkMutationGRPCPostgresRuntimeFlow(t *testing.T) {
	dsn := os.Getenv("HUMAN_RUNTIME_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("HUMAN_RUNTIME_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("human_rpc_it_%d", time.Now().UTC().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../../../../"))
	migrations, err := filepath.Glob(filepath.Join(repoRoot, "db/human-runtime/migrations/*.sql"))
	if err != nil || len(migrations) == 0 {
		t.Fatalf("find Human Runtime migrations: %v", err)
	}
	sort.Strings(migrations)
	for _, migration := range migrations {
		contents, readErr := os.ReadFile(migration)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(migration), err)
		}
	}

	store, err := postgres.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err = pool.Exec(ctx, `INSERT INTO assignment_policies(tenant_id,policy_ref,workflow_type,workflow_version,node_id,candidate_group,sla_duration_ms,config_version,created_at,created_by,updated_at,updated_by) VALUES('tenant-a','reviewers','approval','1','review','finance',0,'cfg-1',$1,'test',$1,'test')`, now); err != nil {
		t.Fatal(err)
	}
	item, duplicate, err := store.ProjectActivation(ctx, domain.Activation{
		TenantID: "tenant-a", EventID: "work-item-1", Sequence: 1, InstanceID: "instance-1",
		WorkflowType: "approval", WorkflowVersion: "1", NodeID: "review", TaskType: "review",
		AssignmentPolicyRef: "reviewers", OccurredAt: now,
	})
	if err != nil || duplicate || item.Version != 1 {
		t.Fatalf("project activation: item=%+v duplicate=%v err=%v", item, duplicate, err)
	}
	service, err := application.NewService(store, &testEngine{}, testPolicy)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &testVerifier{actor: application.ActorIdentity{
		ActorID: "alice", Groups: map[string]struct{}{"finance": {}},
		Capabilities: map[string]struct{}{"human.task.bulk.delegate": {}},
	}}
	server, err := New(service, store, verifier, testPolicy, func() time.Time { return now.Add(time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	engineEvent, err := proto.Marshal(&enginev1.EventEnvelope{
		Metadata: &enginev1.EventMetadata{EventId: "activation-1", TenantId: "tenant-a", InstanceId: "instance-1", Sequence: 1, OccurredAtEpochMs: uint64(now.UnixMilli()), ActorId: "engine"},
		Event:    &enginev1.EventEnvelope_UserTaskActivated{UserTaskActivated: &enginev1.UserTaskActivated{NodeId: "review", TaskType: "review"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.SetHistoryReader(runtimeHistoryReader{page: &enginev1.ReadHistoryPageResponse{
		Events: []*enginev1.StoredWorkflowEvent{{Sequence: 1, EventEnvelope: engineEvent}}, Watermark: 1,
	}})
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	humanv1.RegisterHumanRuntimeServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()
	conn, err := grpc.NewClient("passthrough:///human-runtime-integration",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := humanv1.NewHumanRuntimeServiceClient(conn)
	request := &humanv1.BulkMutateWorkItemsRequest{
		TenantId: "tenant-a", OperationId: "bulk-op-1", IdempotencyKey: "bulk-key-1", CorrelationId: "corr-1",
		Mode:       humanv1.WorkItemBulkMode_WORK_ITEM_BULK_MODE_PARTIAL,
		Action:     humanv1.WorkItemBulkAction_WORK_ITEM_BULK_ACTION_DELEGATE,
		ActorProof: &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT, SignedProof: []byte("signed-actor-context")},
		Items:      []*humanv1.BulkWorkItemMutation{{WorkItemId: item.ID, ExpectedVersion: 1, AssigneeId: "bob", Reason: "coverage"}},
	}
	first, err := client.BulkMutateWorkItems(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.GetStatus() != "SUCCEEDED" || first.GetDuplicate() || len(first.GetReceipts()) != 1 || first.GetReceipts()[0].GetStatus() != "SUCCEEDED" {
		t.Fatalf("unexpected first mutation result: %+v", first)
	}
	replay, err := client.BulkMutateWorkItems(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.GetDuplicate() || replay.GetOperationId() != first.GetOperationId() || len(replay.GetReceipts()) != 1 || replay.GetReceipts()[0].GetCommandId() != first.GetReceipts()[0].GetCommandId() {
		t.Fatalf("idempotent replay changed receipt identity: first=%+v replay=%+v", first, replay)
	}
	updated, err := store.GetWorkItem(ctx, "tenant-a", item.ID)
	if err != nil || updated.Version != 2 || updated.Assignment.AssigneeID != "bob" {
		t.Fatalf("mutation was not committed to the projection: item=%+v err=%v", updated, err)
	}
	var mutationAudits, acceptanceAudits, receiptAudits int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM human_audit_log WHERE tenant_id='tenant-a' AND command_id=$1 AND action='DELEGATED'`, first.GetReceipts()[0].GetCommandId()).Scan(&mutationAudits); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM human_audit_log WHERE tenant_id='tenant-a' AND audit_id='bulk:bulk-op-1' AND action='BULK_ACCEPTED'`).Scan(&acceptanceAudits); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM human_audit_log WHERE tenant_id='tenant-a' AND command_id=$1 AND action='BULK_ITEM_SUCCEEDED'`, first.GetReceipts()[0].GetCommandId()).Scan(&receiptAudits); err != nil {
		t.Fatal(err)
	}
	if mutationAudits != 1 || acceptanceAudits != 1 || receiptAudits != 1 {
		t.Fatalf("expected exactly one mutation/acceptance/receipt audit, got %d/%d/%d", mutationAudits, acceptanceAudits, receiptAudits)
	}

	conflict := proto.Clone(request).(*humanv1.BulkMutateWorkItemsRequest)
	conflict.Items = []*humanv1.BulkWorkItemMutation{{WorkItemId: item.ID, ExpectedVersion: 1, AssigneeId: "carol", Reason: "different intent"}}
	if _, err = client.BulkMutateWorkItems(ctx, conflict); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("changed payload with the same key must conflict, got %v", err)
	}
	stale := proto.Clone(request).(*humanv1.BulkMutateWorkItemsRequest)
	stale.OperationId, stale.IdempotencyKey = "bulk-op-stale", "bulk-key-stale"
	stale.Items = []*humanv1.BulkWorkItemMutation{{WorkItemId: item.ID, ExpectedVersion: 1, AssigneeId: "carol", Reason: "coverage"}}
	staleResult, err := client.BulkMutateWorkItems(ctx, stale)
	if err != nil || staleResult.GetStatus() != "FAILED" || staleResult.GetReceipts()[0].GetErrorCode() != "VERSION_CONFLICT" {
		t.Fatalf("stale item must return a terminal per-item conflict: response=%+v err=%v", staleResult, err)
	}
	updated, err = store.GetWorkItem(ctx, "tenant-a", item.ID)
	if err != nil || updated.Version != 2 || updated.Assignment.AssigneeID != "bob" {
		t.Fatalf("stale attempt changed committed state: item=%+v err=%v", updated, err)
	}
	verifier.actor = application.ActorIdentity{ActorID: "bob", Capabilities: map[string]struct{}{"audit.read": {}}}
	history, err := client.GetWorkItemHistory(ctx, &humanv1.GetWorkItemHistoryRequest{
		TenantId: "tenant-a", WorkItemId: item.ID, PageSize: 10,
		ActorProof: &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT, SignedProof: []byte("signed-actor-context")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var engineEntries, humanEntries int
	for _, entry := range history.GetEntries() {
		switch entry.GetSource() {
		case "ENGINE":
			engineEntries++
		case "HUMAN_RUNTIME":
			humanEntries++
		}
	}
	if history.GetEngineWatermark() != 1 || engineEntries != 1 || humanEntries < 3 {
		t.Fatalf("history did not compose Engine and durable Human Runtime evidence: watermark=%d engine=%d human=%d entries=%+v", history.GetEngineWatermark(), engineEntries, humanEntries, history.GetEntries())
	}
}
