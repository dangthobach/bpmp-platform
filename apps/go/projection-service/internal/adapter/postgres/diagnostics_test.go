package postgres

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizePlanDocumentRemovesIdentifiersAndPredicates(t *testing.T) {
	input := map[string]any{"Planning Time": 1.0, "Plan": map[string]any{"Node Type": "Index Scan", "Relation Name": "secret_table", "Index Name": "tenant_secret_idx", "Filter": "tenant_id = 'secret'", "Actual Rows": 2.0, "Plans": []any{map[string]any{"Node Type": "Seq Scan", "Relation Name": "other"}}}}
	encoded, err := json.Marshal(sanitizePlanDocument(input))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{"secret_table", "tenant_secret_idx", "tenant_id", "other"} {
		if strings.Contains(text, secret) {
			t.Fatalf("redacted plan leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "Index Scan") {
		t.Fatalf("plan shape was removed: %s", text)
	}
}
