package queryauth

import (
	"context"
	"encoding/json"
	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPPDPFailClosedAndBinding(t *testing.T) {
	base := `{"schema_version":1,"tenant_id":"tenant","actor_id":"actor","decision":"ALLOW","policy_version":"p1","revoke_epoch":0,"revoke_sequence":7,"all_rows":true,"row_filter":null,"allowed_fields":[],"masked_fields":["initiator_actor_id"]}`
	for _, tc := range []struct {
		name    string
		mutate  func(map[string]any)
		allowed bool
	}{
		{"allow", func(map[string]any) {}, true},
		{"deny", func(v map[string]any) { v["decision"] = "DENY" }, false},
		{"foreign actor", func(v map[string]any) { v["actor_id"] = "other" }, false},
		{"foreign tenant", func(v map[string]any) { v["tenant_id"] = "other" }, false},
		{"missing scope", func(v map[string]any) { v["all_rows"] = false }, false},
		{"unknown field", func(v map[string]any) { v["allowed_fields"] = []string{"raw_sql"} }, false},
		{"schema", func(v map[string]any) { v["schema_version"] = 2 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			if err := json.Unmarshal([]byte(base), &body); err != nil {
				t.Fatal(err)
			}
			tc.mutate(body)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer workload" {
					t.Error("actor proof substituted for workload identity")
				}
				var request map[string]string
				if json.NewDecoder(r.Body).Decode(&request) != nil || request["actor_jwt"] != "original" {
					t.Error("actor proof not preserved")
				}
				if err := json.NewEncoder(w).Encode(body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client, err := NewHTTPPDP(PDPConfig{URL: server.URL, AllowInsecure: true, TimeoutMS: 1000, MaxResponseBytes: 4096}, func() (string, error) { return "workload", nil })
			if err != nil {
				t.Fatal(err)
			}
			value, err := client.Evaluate(context.Background(), application.QueryAuthorizationRequest{TenantID: "tenant", Credential: application.QueryCredential{SignedProof: []byte("original")}, Policy: application.RuntimePolicy{PolicyVersion: "runtime", QueryPDPResourceType: "workflow", QueryPDPAction: "query"}}, "actor")
			if (err == nil) != tc.allowed {
				t.Fatalf("decision %s err=%v", tc.name, err)
			}
			if tc.allowed && (value.Fields.CanRead("initiator_actor_id") || !value.Fields.CanRead("instance_id") || value.PolicyVersion != "runtime/p1/7/0") {
				t.Fatalf("field/version binding missing: %+v", value)
			}
		})
	}
}
func TestPDPLoweringRejectsUnsafeAndUnknownAST(t *testing.T) {
	for _, raw := range []string{
		`{"operator":"LEAF","left":{"type":"resource_field","key":"instance_id;DROP"},"op":"eq","right":{"type":"literal","value":"x"}}`,
		`{"operator":"LEAF","left":{"type":"resource_field","key":"instance_id"},"op":"eq","right":{"type":"user_attr","key":"owner"}}`,
		`{"operator":"AND","conditions":[]}`,
		`{"operator":"LEAF","left":{"type":"resource_field","key":"instance_id"},"op":"sql","right":{"type":"literal","value":"x"}}`,
	} {
		var node condition
		if err := json.Unmarshal([]byte(raw), &node); err != nil {
			t.Fatal(err)
		}
		if _, err := lower(&node, 0); err == nil {
			t.Fatalf("unsafe AST accepted: %s", raw)
		}
	}
}
