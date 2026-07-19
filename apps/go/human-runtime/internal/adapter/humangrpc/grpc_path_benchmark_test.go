package humangrpc_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/actorverifier"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/enginegrpc"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/adapter/humangrpc"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const grpcBufferSize = 1024 * 1024

type benchmarkStore struct{}

func (benchmarkStore) GetWorkItem(_ context.Context, _, id string) (domain.WorkItem, error) {
	return domain.WorkItem{TenantID: "tenant-a", ID: id, InstanceID: "instance-1", WorkflowType: "approval", WorkflowVersion: "1", NodeID: "review", Status: domain.WorkItemActive, Assignment: domain.Assignment{AssigneeID: "alice"}, Version: 1}, nil
}
func (benchmarkStore) ProjectActivation(context.Context, domain.Activation) (domain.WorkItem, bool, error) {
	return domain.WorkItem{}, false, nil
}
func (benchmarkStore) RequestCompletion(context.Context, domain.WorkItem, string, string, string) error {
	return nil
}
func (benchmarkStore) CommitCompletion(context.Context, application.CommittedCompletion) error {
	return nil
}
func (benchmarkStore) CommitCancellation(context.Context, application.CommittedCancellation) error {
	return nil
}
func (benchmarkStore) Delegate(context.Context, domain.WorkItem, string, string, string) error {
	return nil
}
func (benchmarkStore) ProjectCase(context.Context, application.CommittedCase) (bool, error) {
	return false, nil
}
func (benchmarkStore) CommitCaseTransition(context.Context, application.CommittedCaseTransition) error {
	return nil
}
func (benchmarkStore) TransitionCaseStage(context.Context, string, string, string, domain.PlanItemStatus, string, time.Time) error {
	return nil
}
func (benchmarkStore) AchieveCaseMilestone(context.Context, string, string, string, string, time.Time) error {
	return nil
}
func (benchmarkStore) ListWorkItems(context.Context, string, string, []string, int, *application.PageCursor) ([]domain.WorkItem, *application.PageCursor, error) {
	return nil, nil, nil
}
func (benchmarkStore) GetCase(context.Context, string, string) (application.CaseView, error) {
	return application.CaseView{}, nil
}
func (benchmarkStore) ListAuditRecords(context.Context, string, string, string, int, *application.AuditCursor) ([]application.AuditRecord, *application.AuditCursor, error) {
	return nil, nil, nil
}

type benchmarkSecurity struct{}

func (benchmarkSecurity) ForTenant(context.Context, string, string) (enginegrpc.SecuritySnapshot, error) {
	return enginegrpc.SecuritySnapshot{EncryptionKeyScope: "tenant-a/operational", WorkloadProof: []byte("workload-proof")}, nil
}

type receiptEngine struct {
	enginev1.UnimplementedEngineCommandServiceServer
}

func (receiptEngine) HandleCommand(_ context.Context, command *enginev1.CommandEnvelope) (*enginev1.CommandReceipt, error) {
	return &enginev1.CommandReceipt{CommandId: command.GetCommandId(), CommittedSequence: 2}, nil
}

type grpcHarness struct {
	client humanv1.HumanRuntimeServiceClient
	proof  *authv1.ActorProof
	now    time.Time
}

func newGRPCHarness(tb testing.TB) grpcHarness {
	tb.Helper()
	now := time.Now().UTC()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	jwks := []byte(fmt.Sprintf(`{"keys":[{"kty":"OKP","kid":"jwt-key","alg":"EdDSA","crv":"Ed25519","x":"%s"}]}`, base64.RawURLEncoding.EncodeToString(publicKey)))
	verifier, err := actorverifier.New(actorverifier.Config{
		Issuers: map[string]struct{}{"https://identity.example": {}}, Audiences: map[string]struct{}{"bpmp": {}},
		AllowedJWTMethods: map[string]struct{}{jwt.SigningMethodEdDSA.Alg(): {}}, WorkloadID: "human-runtime",
		MaxProofBytes: 8192, MaxJWKSKeys: 8, MaxRoles: 16, MaxCapabilities: 32, ClockSkew: time.Second,
	}, jwks, map[string]ed25519.PublicKey{"internal-key": publicKey}, actorverifier.NewMemoryRevokeEpochs())
	if err != nil {
		tb.Fatal(err)
	}
	claims := jwt.MapClaims{"iss": "https://identity.example", "sub": "alice", "aud": []string{"bpmp"}, "tenant_id": "tenant-a", "roles": []string{"reviewers"}, "capabilities": []string{"task.complete"}, "revoke_epoch": 1, "iat": now.Add(-time.Second).Unix(), "exp": now.Add(time.Hour).Unix()}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "jwt-key"
	signed, err := token.SignedString(privateKey)
	if err != nil {
		tb.Fatal(err)
	}

	engineListener := bufconn.Listen(grpcBufferSize)
	engineServer := grpc.NewServer()
	enginev1.RegisterEngineCommandServiceServer(engineServer, receiptEngine{})
	go func() { _ = engineServer.Serve(engineListener) }()
	tb.Cleanup(engineServer.Stop)
	engineConn := dialBuffer(tb, engineListener, "engine")
	engineClient, err := enginegrpc.New(enginev1.NewEngineCommandServiceClient(engineConn), benchmarkSecurity{})
	if err != nil {
		tb.Fatal(err)
	}
	store := benchmarkStore{}
	service, err := application.NewService(store, engineClient)
	if err != nil {
		tb.Fatal(err)
	}
	humanServerAdapter, err := humangrpc.New(service, store, verifier, func() time.Time { return now })
	if err != nil {
		tb.Fatal(err)
	}
	humanListener := bufconn.Listen(grpcBufferSize)
	humanServer := grpc.NewServer()
	humanv1.RegisterHumanRuntimeServiceServer(humanServer, humanServerAdapter)
	go func() { _ = humanServer.Serve(humanListener) }()
	tb.Cleanup(humanServer.Stop)
	humanConn := dialBuffer(tb, humanListener, "human")
	return grpcHarness{client: humanv1.NewHumanRuntimeServiceClient(humanConn), proof: &authv1.ActorProof{Type: authv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT, SignedProof: []byte(signed)}, now: now}
}

func dialBuffer(tb testing.TB, listener *bufconn.Listener, name string) *grpc.ClientConn {
	tb.Helper()
	connection, err := grpc.NewClient("passthrough:///"+name, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = connection.Close() })
	return connection
}

func (h grpcHarness) request(id int) *humanv1.CompleteWorkItemRequest {
	value := fmt.Sprintf("%d", id)
	return &humanv1.CompleteWorkItemRequest{TenantId: "tenant-a", WorkItemId: "work-" + value, CommandId: "command-" + value, CorrelationId: "correlation-" + value, Decision: "approved", ExpectedVersion: 1, ActorProof: h.proof}
}

func TestFullGrpcPathP95(t *testing.T) {
	harness := newGRPCHarness(t)
	const workers, operations = 8, 200
	latencies := make(chan time.Duration, operations)
	errorsSeen := make(chan error, operations)
	var wait sync.WaitGroup
	for worker := range workers {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for index := worker; index < operations; index += workers {
				started := time.Now()
				_, err := harness.client.CompleteWorkItem(context.Background(), harness.request(index))
				latencies <- time.Since(started)
				if err != nil {
					errorsSeen <- err
				}
			}
		}(worker)
	}
	wait.Wait()
	close(latencies)
	close(errorsSeen)
	if err, ok := <-errorsSeen; ok {
		t.Fatal(err)
	}
	values := make([]time.Duration, 0, operations)
	for latency := range latencies {
		values = append(values, latency)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	p95 := values[(len(values)*95+99)/100-1]
	t.Logf("full-gRPC operations=%d concurrency=%d p95=%s", operations, workers, p95)
	if p95 > 500*time.Millisecond {
		t.Fatalf("full gRPC P95 %s exceeds 500ms", p95)
	}
}

func BenchmarkFullGrpcCompletionPath(b *testing.B) {
	harness := newGRPCHarness(b)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := harness.client.CompleteWorkItem(context.Background(), harness.request(index)); err != nil {
			b.Fatal(err)
		}
	}
}
