package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRuntimeDeploymentPostgresQueueLeaseAndIdempotency(t *testing.T) {
	dsn := os.Getenv("AUTHORING_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AUTHORING_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "authoring_test_" + uuid.NewString()[:8]
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
	_, thisFile, _, _ := runtime.Caller(0)
	migrationDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "..", "..", "db", "authoring-service", "migrations")
	migrations, err := filepath.Glob(filepath.Join(migrationDir, "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(migrations)
	if len(migrations) != 7 {
		t.Fatalf("expected 7 authoring migrations, found %d in %s", len(migrations), migrationDir)
	}
	for _, migration := range migrations {
		sql, readErr := os.ReadFile(migration)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(migration), err)
		}
	}
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	tenant, actor, command := uuid.New(), uuid.New(), uuid.New()
	model, version := uuid.New(), uuid.New()
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	_, err = pool.Exec(ctx, `INSERT INTO process_models(tenant_id,id,model_key,name,created_by,updated_by) VALUES($1,$2,'invoice','Invoice', $3,$3)`, tenant, model, actor)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO process_model_versions(tenant_id,id,process_model_id,version_num,source_revision,bpmn_xml,content_hash,created_by,created_at,version_tag,summary) VALUES($1,$2,$3,1,1,'<definitions/>',$4,$5,$6,'v1','initial')`, tenant, version, model, make([]byte, 32), actor, now)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("request"))
	item := application.RuntimeDeployment{ProcessModelID: model.String(), ProcessModelVersionID: version.String(), TargetEnvironment: "staging"}
	created, replay, err := store.CreateRuntimeDeployment(ctx, tenant.String(), actor.String(), command.String(), "idem-1", "corr-1", digest[:], item, now)
	if err != nil || replay || created.Status != "QUEUED" || created.DeploymentSequence != 1 {
		t.Fatalf("create deployment: item=%#v replay=%v err=%v", created, replay, err)
	}
	replayed, replay, err := store.CreateRuntimeDeployment(ctx, tenant.String(), actor.String(), command.String(), "idem-1", "corr-1", digest[:], item, now.Add(time.Second))
	if err != nil || !replay || replayed.ID != created.ID {
		t.Fatalf("idempotent replay: item=%#v replay=%v err=%v", replayed, replay, err)
	}
	changedDigest := sha256.Sum256([]byte("changed"))
	if _, _, err = store.CreateRuntimeDeployment(ctx, tenant.String(), actor.String(), command.String(), "idem-1", "corr-1", changedDigest[:], item, now); !errors.Is(err, application.ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
	dueNow := time.Now().UTC().Add(time.Minute)
	claimed, err := store.ClaimRuntimeDeployments(ctx, "worker-a", 4, time.Minute, dueNow)
	if err != nil || len(claimed) != 1 || claimed[0].Status != "COMPILING" || claimed[0].AttemptCount != 1 {
		t.Fatalf("first claim: %#v err=%v", claimed, err)
	}
	claimedElsewhere, err := store.ClaimRuntimeDeployments(ctx, "worker-b", 4, time.Minute, dueNow.Add(time.Second))
	if err != nil || len(claimedElsewhere) != 0 {
		t.Fatalf("live lease was claimed twice: %#v err=%v", claimedElsewhere, err)
	}
	if err = store.SaveDeploymentArtifact(ctx, tenant.String(), created.ID, "worker-b", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", make([]byte, 32), "invoice", "1", dueNow.Add(2*time.Second)); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("non-owner must not save artifact, got %v", err)
	}
	claimedAgain, err := store.ClaimRuntimeDeployments(ctx, "worker-b", 4, time.Minute, dueNow.Add(2*time.Minute))
	if err != nil || len(claimedAgain) != 1 || claimedAgain[0].AttemptCount != 2 {
		t.Fatalf("expired lease recovery: %#v err=%v", claimedAgain, err)
	}
	if err = store.SaveDeploymentArtifact(ctx, tenant.String(), created.ID, "worker-b", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", make([]byte, 32), "invoice", "1", dueNow.Add(2*time.Minute)); err != nil {
		t.Fatalf("lease owner artifact save: %v", err)
	}
	current, err := store.GetRuntimeDeployment(ctx, tenant.String(), created.ID)
	if err != nil || current.Status != "STAGING" || current.ArtifactID == "" {
		t.Fatalf("staged deployment not persisted: %#v err=%v", current, err)
	}
	var outbox, audit int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM runtime_deployment_outbox WHERE tenant_id=$1 AND deployment_id=$2`, tenant, uuid.MustParse(created.ID)).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM authoring_audit_evidence WHERE tenant_id=$1 AND process_model_id=$2`, tenant, model).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if outbox != 2 || audit != 2 {
		t.Fatalf("expected queued+staged outbox/audit, got outbox=%d audit=%d", outbox, audit)
	}
}
