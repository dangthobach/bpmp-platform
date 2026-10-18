package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	for attempt := 0; attempt < 2; attempt++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errs <- applyProjectionMigrations(ctx, pool, migrations)
		}()
	}
	wait.Wait()
	close(errs)
	for migrationErr := range errs {
		if migrationErr != nil {
			t.Fatalf("concurrent migration: %v", migrationErr)
		}
	}
	if err = applyProjectionMigrations(ctx, pool, migrations); err != nil {
		t.Fatalf("idempotent migration: %v", err)
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

	var ledgerRows int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM bpmp_schema_migrations WHERE namespace=$1`, projectionMigrationNamespace).Scan(&ledgerRows); err != nil {
		t.Fatal(err)
	}
	if ledgerRows != 6 {
		t.Fatalf("expected 6 migration ledger rows, got %d", ledgerRows)
	}
	if _, err = pool.Exec(ctx, `UPDATE bpmp_schema_migrations SET checksum_sha256=repeat('0',64) WHERE namespace=$1`, projectionMigrationNamespace); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("expected append-only ledger rejection, got %v", err)
	}

	t.Run("checksum drift is rejected", func(t *testing.T) {
		driftDir := copyProjectionMigrations(t, migrations)
		first := filepath.Join(driftDir, "001_projection.sql")
		contents, readErr := os.ReadFile(first)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if writeErr := os.WriteFile(first, append(contents, []byte("\n-- drift\n")...), 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
		driftErr := applyProjectionMigrations(ctx, pool, driftDir)
		if driftErr == nil || !strings.Contains(driftErr.Error(), "checksum mismatch") {
			t.Fatalf("expected checksum mismatch, got %v", driftErr)
		}
	})

	t.Run("missing applied migration is rejected", func(t *testing.T) {
		missingDir := copyProjectionMigrations(t, migrations)
		if removeErr := os.Remove(filepath.Join(missingDir, "006_query_storage_hardening.sql")); removeErr != nil {
			t.Fatal(removeErr)
		}
		missingErr := applyProjectionMigrations(ctx, pool, missingDir)
		if missingErr == nil || !strings.Contains(missingErr.Error(), "is missing") {
			t.Fatalf("expected missing migration rejection, got %v", missingErr)
		}
	})

	t.Run("failed migration and ledger row roll back together", func(t *testing.T) {
		brokenDir := copyProjectionMigrations(t, migrations)
		broken := []byte("CREATE TABLE migration_must_rollback(id BIGINT PRIMARY KEY); SELECT missing_column;")
		if writeErr := os.WriteFile(filepath.Join(brokenDir, "007_broken.sql"), broken, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
		if brokenErr := applyProjectionMigrations(ctx, pool, brokenDir); brokenErr == nil {
			t.Fatal("expected broken migration to fail")
		}
		var exists bool
		if queryErr := pool.QueryRow(ctx, `SELECT to_regclass('migration_must_rollback') IS NOT NULL`).Scan(&exists); queryErr != nil {
			t.Fatal(queryErr)
		}
		if exists {
			t.Fatal("failed migration left a table behind")
		}
	})
}

func copyProjectionMigrations(t *testing.T, source string) string {
	t.Helper()
	destination := t.TempDir()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		contents, readErr := os.ReadFile(filepath.Join(source, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(destination, entry.Name()), contents, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	return destination
}
