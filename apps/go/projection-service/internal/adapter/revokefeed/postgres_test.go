package revokefeed

import (
	"context"
	"errors"
	"fmt"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDurableEpochReplayAndQuarantine(t *testing.T) {
	dsn := os.Getenv("BPMP_PROJECTION_TEST_DSN")
	if dsn == "" {
		t.Skip("BPMP_PROJECTION_TEST_DSN required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("revoke_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
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
	ddl, err := os.ReadFile(filepath.Join("../../../../../../db/projection-service/migrations/003_query_revoke_kafka.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(ddl)); err != nil {
		t.Fatal(err)
	}
	store, _ := NewPostgresStore(pool)
	record, keys := signedRecord(t)
	event, err := Verify(record, keys, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Apply(ctx, record, event); err != nil {
		t.Fatal(err)
	}
	if err = store.Apply(ctx, record, event); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(store.Check(ctx, event.TenantId, event.ActorId, 0), application.ErrQueryForbidden) {
		t.Fatal("revoked actor allowed")
	}
	event.RevokeEpoch = 2
	event.EventSequence = 9
	event.Revoked = false
	event.EventId = "restore"
	record.Offset = 1
	if err = store.Apply(ctx, record, event); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(store.Check(ctx, event.TenantId, event.ActorId, 1), application.ErrQueryForbidden) {
		t.Fatal("restore revived old epoch")
	}
	if err = store.Check(ctx, event.TenantId, event.ActorId, 2); err != nil {
		t.Fatal(err)
	}
	bad := &kgo.Record{Topic: record.Topic, Partition: 0, Offset: 2, Value: []byte("invalid")}
	incident, err := store.Quarantine(ctx, bad, "invalid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Quarantine(ctx, bad, "invalid"); err != nil {
		t.Fatal(err)
	}
	changed := *bad
	changed.Value = []byte("different-payload")
	if _, err = store.Quarantine(ctx, &changed, "invalid"); err == nil {
		t.Fatal("reused coordinates with a different hash accepted")
	}
	if !errors.Is(store.Check(ctx, event.TenantId, event.ActorId, 2), application.ErrQueryForbidden) {
		t.Fatal("poison fence absent")
	}
	var offset int64
	if err = pool.QueryRow(ctx, "SELECT last_offset FROM query_revoke_checkpoints").Scan(&offset); err != nil || offset != 1 {
		t.Fatalf("premature checkpoint %d %v", offset, err)
	}
	if _, err = pool.Exec(ctx, "SELECT resolve_query_revoke_incident($1,$2,'test','reviewed')", incident.IncidentId, incident.SourceSha256); err == nil {
		t.Fatal("incident resolved without DLQ ACK")
	}
	if err = store.AcknowledgeDLQ(ctx, bad, incident.IncidentId); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "SELECT resolve_query_revoke_incident($1,$2,'test','reviewed')", incident.IncidentId, incident.SourceSha256); err != nil {
		t.Fatal(err)
	}
	if err = store.Check(ctx, event.TenantId, event.ActorId, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE query_revoke_incidents SET reason_code='changed'"); err == nil {
		t.Fatal("incident audit is mutable")
	}
	if _, err = pool.Exec(ctx, "DELETE FROM query_revoke_incident_resolutions"); err == nil {
		t.Fatal("resolution audit is mutable")
	}
}
