package postgres

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/grpcapi"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/adapter/queryauth"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	authorizationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	projectionv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/projection/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/jwtauth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type integrationPolicy struct{ value application.RuntimePolicy }

func (p *integrationPolicy) Policy(string) (application.RuntimePolicy, error) { return p.value, nil }

func TestPostgresAuthorizedQueryLifecycle(t *testing.T) {
	dsn := os.Getenv("BPMP_PROJECTION_TEST_DSN")
	if dsn == "" {
		t.Skip("BPMP_PROJECTION_TEST_DSN is required for real PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("projection_query_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, dropErr := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); dropErr != nil {
			t.Error(dropErr)
		}
	}()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, file := range []string{"001_projection.sql", "002_completed_process_query.sql", "004_query_text_transforms.sql"} {
		data, readErr := os.ReadFile(filepath.Join("../../../../../../db/projection-service/migrations", file))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(data)); err != nil {
			t.Fatal(err)
		}
	}
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	jwks, err := json.Marshal(map[string]any{"keys": []map[string]string{{"kty": "OKP", "kid": "k1", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public)}}})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := jwtauth.NewFromJWKS(jwtauth.Config{Issuers: []string{"test"}, Audiences: []string{"projection"}, Algorithms: []string{"EdDSA"}, MaxTokenBytes: 4096, MaxJWKSKeys: 2}, jwks)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := queryauth.New(queryauth.Config{MaxCapabilities: 8}, verifier)
	if err != nil {
		t.Fatal(err)
	}
	policy := &integrationPolicy{value: application.RuntimePolicy{
		ConfigVersion: "c1", PolicyVersion: "p1", QueryDefaultPageSize: 2, QueryMaxPageSize: 20, QueryMaxFilterDepth: 8, QueryMaxPredicates: 20, QueryMaxSortFields: 4, QueryMaxRelationJoins: 4, QueryMaxInValues: 20, QueryTimeout: time.Second, QueryMaxStringBytes: 512, QueryCursorSigningKeyID: "k1", QueryAllCapability: "query.all", QueryOwnCapability: "query.own", QueryMaxTokenAge: time.Hour,
	}}
	service, err := application.NewService(store, policy, application.WithQueryAuthorizer(authorizer))
	if err != nil {
		t.Fatal(err)
	}
	codec, err := grpcapi.NewCursorCodec([]grpcapi.CursorKey{{ID: "k1", Secret: []byte(strings.Repeat("x", 32))}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := grpcapi.New(service, codec)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(actor, tenant, capability string) *authorizationv1.ActorProof {
		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{"iss": "test", "sub": actor, "aud": []string{"projection"}, "tenant_id": tenant, "capabilities": []string{capability}, "revoke_epoch": 0, "iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix()})
		token.Header["kid"] = "k1"
		raw, signErr := token.SignedString(private)
		if signErr != nil {
			t.Fatal(signErr)
		}
		return &authorizationv1.ActorProof{Type: authorizationv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT, SignedProof: []byte(raw)}
	}
	own := sign("alice", "tenant-a", "query.own")
	all := sign("admin", "tenant-a", "query.all")
	occurred := time.Now().UTC().Truncate(time.Millisecond)
	for index, actor := range []string{"alice", "bob", "alice", "alice"} {
		id := fmt.Sprintf("i%d", index)
		event := application.InstanceEvent{TenantID: "tenant-a", InstanceID: id, WorkflowType: "approval", WorkflowVersion: "1", EventID: id + "-start", Sequence: 1, OccurredAt: occurred, ConfigVersion: "c1", PolicyVersion: "p1", ActorID: actor, EventType: "workflow_started", Kind: application.EventStarted}
		pos := application.KafkaPosition{ConsumerName: "test", Topic: "events", Partition: 0, Offset: int64(index * 2)}
		if _, err = service.Apply(ctx, pos, event); err != nil {
			t.Fatal(err)
		}
		event.EventID = id + "-done"
		event.Sequence = 2
		event.Kind = application.EventCompleted
		event.EventType = "workflow_completed"
		event.OccurredAt = occurred.Add(time.Second)
		pos.Offset++
		if _, err = service.Apply(ctx, pos, event); err != nil {
			t.Fatal(err)
		}
		if duplicate, applyErr := service.Apply(ctx, pos, event); applyErr != nil || !duplicate {
			t.Fatalf("replay dedup failed: %v %v", duplicate, applyErr)
		}
	}
	req := &projectionv1.ListWorkflowInstancesRequest{TenantId: "tenant-a", ActorProof: own, PageSize: 2}
	page, err := server.ListWorkflowInstances(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Instances) != 2 || page.NextPageToken == "" {
		t.Fatalf("missing bounded page: %+v", page)
	}
	for _, instance := range page.Instances {
		if instance.InitiatorActorId != "alice" {
			t.Fatal("ownership filter leaked another actor")
		}
	}
	req.PageToken = page.NextPageToken
	next, err := server.ListWorkflowInstances(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Instances) != 1 || next.Instances[0].InstanceId == page.Instances[0].InstanceId || next.Instances[0].InstanceId == page.Instances[1].InstanceId {
		t.Fatal("keyset page omitted or duplicated data")
	}
	req.ActorProof = all
	if _, err = server.ListWorkflowInstances(ctx, req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("cross-actor cursor accepted: %v", err)
	}
	if _, err = server.GetWorkflowInstance(ctx, &projectionv1.GetWorkflowInstanceRequest{TenantId: "tenant-a", InstanceId: "i1", ActorProof: own}); status.Code(err) != codes.NotFound {
		t.Fatalf("unauthorized Get leaked row: %v", err)
	}
	if _, err = server.GetWorkflowInstance(ctx, &projectionv1.GetWorkflowInstanceRequest{TenantId: "tenant-a", InstanceId: "i0"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("missing actor proof accepted: %v", err)
	}
	completed, err := server.QueryCompletedProcesses(ctx, &projectionv1.QueryCompletedProcessesRequest{TenantId: "tenant-a", ActorProof: all, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Processes) != 4 {
		t.Fatalf("completed query returned %d", len(completed.Processes))
	}
	for _, process := range completed.Processes {
		if process.EventCount != 2 || process.DurationMs != 1000 {
			t.Fatalf("bad persisted aggregate: %+v", process)
		}
	}
	for _, test := range []struct {
		name       string
		quantifier application.RelationQuantifier
		eventType  string
		count      int
	}{
		{"exists", application.RelationExists, "workflow_completed", 3},
		{"not exists", application.RelationNotExists, "workflow_cancelled", 3},
		{"not exists excludes", application.RelationNotExists, "workflow_completed", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows, _, _, queryErr := service.QueryCompleted(ctx, application.ProcessQuery{
				TenantID: "tenant-a", Limit: 20,
				Credential: application.QueryCredential{Type: application.ActorProofOriginalJWT, SignedProof: own.SignedProof},
				Filter: &application.ProcessExpression{EventRelation: &application.EventRelation{
					Quantifier: test.quantifier,
					Expression: application.EventExpression{Predicate: &application.EventPredicate{
						Field: application.EventType, Operator: application.CompareEqual,
						Values: []application.QueryValue{{Kind: application.ValueString, String: test.eventType}},
					}},
				}},
			})
			if queryErr != nil || len(rows) != test.count {
				t.Fatalf("relation result: rows=%d err=%v", len(rows), queryErr)
			}
		})
	}
	rows, _, _, err := service.QueryCompleted(ctx, application.ProcessQuery{
		TenantID: "tenant-a", Limit: 20,
		Credential: application.QueryCredential{Type: application.ActorProofOriginalJWT, SignedProof: own.SignedProof},
		Filter: &application.ProcessExpression{Logical: application.LogicalOr, Children: []application.ProcessExpression{
			{Predicate: &application.ProcessPredicate{Field: application.ProcessInstanceID, Operator: application.CompareEqual, Values: []application.QueryValue{{Kind: application.ValueString, String: "i1"}}}},
			{Predicate: &application.ProcessPredicate{Field: application.ProcessWorkflowType, Operator: application.CompareEqual, Values: []application.QueryValue{{Kind: application.ValueString, String: "approval"}}}},
		}},
	})
	if err != nil || len(rows) != 3 {
		t.Fatalf("client OR bypassed ownership: rows=%d err=%v", len(rows), err)
	}
	for _, row := range rows {
		if row.Instance.InitiatorActorID != "alice" {
			t.Fatal("client OR leaked another actor")
		}
	}
	policy.value.QueryActorRevokeEpochs = map[string]uint64{"alice": 1}
	policy.value.PolicyVersion = "p2"
	if _, err = server.ListWorkflowInstances(ctx, &projectionv1.ListWorkflowInstancesRequest{TenantId: "tenant-a", ActorProof: own}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("revoked actor token accepted: %v", err)
	}
	if _, err = server.QueryCompletedProcesses(ctx, &projectionv1.QueryCompletedProcessesRequest{TenantId: "tenant-b", ActorProof: all}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-tenant actor accepted: %v", err)
	}
}
