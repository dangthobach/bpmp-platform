package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadOptionalSecretPreservesContentAndRemovesFileNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "redis-password")
	if err := os.WriteFile(path, []byte(" secret value \r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	value, err := readOptionalSecret(path)
	if err != nil {
		t.Fatal(err)
	}
	if value != " secret value " {
		t.Fatalf("unexpected secret %q", value)
	}
}

func TestReadOptionalSecretAllowsUnauthenticatedRedis(t *testing.T) {
	value, err := readOptionalSecret("")
	if err != nil {
		t.Fatal(err)
	}
	if value != "" {
		t.Fatalf("expected empty secret, got %q", value)
	}
}
