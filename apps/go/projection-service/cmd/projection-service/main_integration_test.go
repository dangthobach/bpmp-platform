package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var migrationSchemaName = regexp.MustCompile(`^[a-z][a-z0-9_]+$`)

func TestApplyProjectionMigrationsDirectoryIsAtomicAndIdempotent(t *testing.T) {
	dsn := os.Getenv("BPMP_PROJECTION_TEST_DSN")
	if dsn == "" {
		t.Skip("BPMP_PROJECTION_TEST_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("projection_migration_%d", time.Now().UnixNano())
	if !migrationSchemaName.MatchString(schema) {
		t.Fatalf("unexpected schema name %q", schema)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		cleanupPool, cleanupErr := pgxpool.New(cleanupCtx, dsn)
		if cleanupErr != nil {
			t.Errorf("open migration cleanup connection: %v", cleanupErr)
			return
		}
		defer cleanupPool.Close()
		if _, cleanupErr = cleanupPool.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); cleanupErr != nil {
			t.Errorf("drop migration schema: %v", cleanupErr)
		}
	})

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

	migrations, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "db", "projection-service", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err = applyProjectionMigrations(ctx, pool, migrations); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt+1, err)
		}
	}

	var tables int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name IN ('workflow_instance_read_models', 'workflow_event_read_models',
		                     'projection_saved_queries', 'projection_query_exports')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 4 {
		t.Fatalf("expected all query tables, got %d", tables)
	}

	var primaryKey string
	if err = pool.QueryRow(ctx, `
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid = 'projection_saved_queries'::regclass AND contype = 'p'`).Scan(&primaryKey); err != nil {
		t.Fatal(err)
	}
	if primaryKey != "PRIMARY KEY (tenant_id, owner_subject, id)" {
		t.Fatalf("unexpected saved-query primary key: %s", primaryKey)
	}
}
