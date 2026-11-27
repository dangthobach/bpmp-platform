package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/postgres"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/actorcontext"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

func signCaseActorProof(t *testing.T, private ed25519.PrivateKey, tenantID, actorID, commandID string, caps []string) string {
	t.Helper()
	now := time.Now().UTC()
	sortedCaps := append([]string(nil), caps...)
	sort.Strings(sortedCaps)
	proof := &authv1.SignedActorContext{
		SchemaVersion:      1,
		TenantId:           tenantID,
		ActorId:            actorID,
		CommandId:          commandID,
		AudienceWorkloadId: "case-service",
		SigningKeyId:       "case-test-key",
		Capabilities:       sortedCaps,
		IssuedAtEpochMs:    uint64(now.Add(-time.Second).UnixMilli()),
		ExpiresAtEpochMs:   uint64(now.Add(time.Minute).UnixMilli()),
	}
	unsigned, err := (proto.MarshalOptions{Deterministic: true}).Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(unsigned)
	proof.ContentHash = digest[:]
	proof.Signature = ed25519.Sign(private, digest[:])
	signed, err := (proto.MarshalOptions{Deterministic: true}).Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(signed)
}

func TestAuthenticatedCaseCreateReplayNoDuplicateWorkflow(t *testing.T) {
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
	schema := "case_http_it_" + uuid.NewString()[:8]
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()

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

	store, err := postgres.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(store, time.Now, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := actorcontext.New(actorcontext.Config{
		WorkloadID: "case-service", MaxBytes: 1 << 20, MaxRoles: 32, MaxCaps: 64,
		ClockSkew: 30 * time.Second, Keys: map[string]ed25519.PublicKey{"case-test-key": public},
	})
	if err != nil {
		t.Fatal(err)
	}

	h := &handler{service: service, proof: verifier}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/customer-cases", h.create)
	mux.HandleFunc("GET /v1/customer-cases/{caseID}", h.get)
	mux.HandleFunc("GET /v1/customer-cases/{caseID}/timeline", h.timeline)
	mux.HandleFunc("GET /v1/customer-cases/{caseID}/process-events", h.processEvents)

	tenantA := uuid.New()
	tenantB := uuid.New()
	actorID := uuid.NewString()
	idemKey := "bff-create-replay-key-1"
	cmdID := uuid.NewString()
	payload := `{"customer_ref":"CUST-REPLAY-001","workflow_type":"loan-approval","workflow_version":"1"}`

	sendCreate := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/customer-cases", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-BPMP-Tenant-ID", tenantA.String())
		req.Header.Set("X-BPMP-Signed-Actor-Context", signCaseActorProof(t, private, tenantA.String(), actorID, cmdID, []string{"case.create", "case.read"}))
		req.Header.Set("Idempotency-Key", idemKey)
		req.Header.Set("X-Command-ID", cmdID)
		req.Header.Set("X-Correlation-ID", "corr-replay-1")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	// 1. First authenticated create
	firstRec := sendCreate(payload)
	if firstRec.Code != http.StatusAccepted {
		t.Fatalf("first create expected 202, got %d: %s", firstRec.Code, firstRec.Body.String())
	}
	var firstRes application.CreateResult
	if err = json.Unmarshal(firstRec.Body.Bytes(), &firstRes); err != nil {
		t.Fatal(err)
	}
	if firstRes.Duplicate || firstRes.Case.ID == uuid.Nil {
		t.Fatalf("expected fresh case on first create, got %+v", firstRes)
	}

	// 2. Replay exact same authenticated request -> must return same Case with duplicate=true and NO duplicate workflow start job.
	replayRec := sendCreate(payload)
	if replayRec.Code != http.StatusAccepted {
		t.Fatalf("replay create expected 202, got %d: %s", replayRec.Code, replayRec.Body.String())
	}
	var replayRes application.CreateResult
	if err = json.Unmarshal(replayRec.Body.Bytes(), &replayRes); err != nil {
		t.Fatal(err)
	}
	if !replayRes.Duplicate || replayRes.Case.ID != firstRes.Case.ID {
		t.Fatalf("replay did not preserve case identity: first=%+v replay=%+v", firstRes, replayRes)
	}

	var caseCount, startJobCount, outboxCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM customer_cases WHERE tenant_id=$1`, tenantA).Scan(&caseCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM workflow_start_jobs WHERE tenant_id=$1`, tenantA).Scan(&startJobCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM case_outbox WHERE tenant_id=$1`, tenantA).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if caseCount != 1 || startJobCount != 1 || outboxCount != 1 {
		t.Fatalf("same-key replay created duplicate workflow state: cases=%d jobs=%d outbox=%d", caseCount, startJobCount, outboxCount)
	}

	// 3. Reusing the same Idempotency-Key with a changed payload must return 409 Conflict.
	conflictRec := sendCreate(`{"customer_ref":"CUST-DIFFERENT","workflow_type":"loan-approval","workflow_version":"1"}`)
	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("changed payload with same key expected 409, got %d: %s", conflictRec.Code, conflictRec.Body.String())
	}

	// 4. Cross-tenant reads must fail closed with 404.
	for _, path := range []string{
		"/v1/customer-cases/" + firstRes.Case.ID.String(),
		"/v1/customer-cases/" + firstRes.Case.ID.String() + "/timeline",
		"/v1/customer-cases/" + firstRes.Case.ID.String() + "/process-events",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-BPMP-Tenant-ID", tenantB.String())
		req.Header.Set("X-BPMP-Signed-Actor-Context", signCaseActorProof(t, private, tenantB.String(), actorID, "", []string{"case.read"}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("cross-tenant GET %s expected 404, got %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}
