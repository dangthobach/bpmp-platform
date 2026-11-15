package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMigrationConfigRequiresOnlyDatabaseAndMigrationPath(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		content string
		wantErr bool
	}{
		{
			name:    "minimal config",
			content: `{"postgres_dsn":"postgres://authoring:migration@postgres/authoring?sslmode=verify-full","migration_path":"/opt/bpmp/authoring/migrations"}`,
		},
		{
			name:    "missing database",
			content: `{"migration_path":"/migrations"}`,
			wantErr: true,
		},
		{
			name:    "missing migration path",
			content: `{"postgres_dsn":"postgres://authoring:migration@postgres/authoring"}`,
			wantErr: true,
		},
		{
			name:    "rejects unrelated runtime credentials",
			content: `{"postgres_dsn":"postgres://authoring:migration@postgres/authoring","migration_path":"/migrations","tls":{"server_private_key":"secret"}}`,
			wantErr: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "migration.json")
			if err := os.WriteFile(path, []byte(testCase.content), 0o600); err != nil {
				t.Fatal(err)
			}
			config, err := loadMigrationConfig(path)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("loadMigrationConfig() error = %v, wantErr %v", err, testCase.wantErr)
			}
			if !testCase.wantErr && (config.PostgresDSN == "" || config.MigrationPath != "/opt/bpmp/authoring/migrations") {
				t.Fatalf("unexpected migration config: %+v", config)
			}
		})
	}
}
