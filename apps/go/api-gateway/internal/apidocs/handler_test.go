package apidocs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/gateway"
)

func testHandler(t *testing.T) *Handler {
	t.Helper()
	handler, err := New(Config{
		OpenAPIPath:     "/openapi/v1.json",
		ReferencePath:   "/docs",
		ScalarScriptURL: "https://cdn.example.test/@scalar/api-reference@1.63.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestOpenAPIContractContainsEveryPublicOperation(t *testing.T) {
	handler := testHandler(t)
	if len(handler.spec) == 0 {
		t.Fatal("embedded OpenAPI contract is empty")
	}
	var document struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(handler.spec, &document); err != nil {
		t.Fatal(err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("unexpected OpenAPI version %q", document.OpenAPI)
	}
	contractOperations := make(map[string]string)
	for _, pathItem := range document.Paths {
		for method, rawOperation := range pathItem {
			switch method {
			case "get", "post", "put", "patch", "delete":
				var operation struct {
					OperationID string `json:"operationId"`
				}
				if err := json.Unmarshal(rawOperation, &operation); err != nil {
					t.Fatal(err)
				}
				contractOperations[strings.ToUpper(method)+" "+operation.OperationID] = operation.OperationID
			}
		}
	}
	runtimeOperations := gateway.PublicOperations()
	if len(contractOperations) != len(runtimeOperations) {
		t.Fatalf("contract operations=%d runtime operations=%d", len(contractOperations), len(runtimeOperations))
	}
	for _, operation := range runtimeOperations {
		pathItem, exists := document.Paths[operation.Path]
		if !exists {
			t.Fatalf("runtime path %s is absent from OpenAPI", operation.Path)
		}
		rawContract, exists := pathItem[strings.ToLower(operation.Method)]
		var contract struct {
			OperationID string `json:"operationId"`
		}
		if exists {
			if err := json.Unmarshal(rawContract, &contract); err != nil {
				t.Fatal(err)
			}
		}
		if !exists || contract.OperationID != operation.OperationID {
			t.Fatalf("runtime operation %s %s does not match OpenAPI", operation.Method, operation.Path)
		}
	}
}

func TestRegisterServesReferenceAndCacheableContract(t *testing.T) {
	handler := testHandler(t)
	mux := http.NewServeMux()
	handler.Register(mux)

	specRequest := httptest.NewRequest(http.MethodGet, "/openapi/v1.json", nil)
	specResponse := httptest.NewRecorder()
	mux.ServeHTTP(specResponse, specRequest)
	if specResponse.Code != http.StatusOK ||
		!strings.Contains(specResponse.Header().Get("Content-Type"), "openapi+json") ||
		specResponse.Header().Get("ETag") == "" {
		t.Fatalf("invalid OpenAPI response: status=%d headers=%v", specResponse.Code, specResponse.Header())
	}

	notModified := httptest.NewRecorder()
	conditional := httptest.NewRequest(http.MethodGet, "/openapi/v1.json", nil)
	conditional.Header.Set("If-None-Match", specResponse.Header().Get("ETag"))
	mux.ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", notModified.Code)
	}

	pageResponse := httptest.NewRecorder()
	mux.ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if pageResponse.Code != http.StatusOK ||
		!strings.Contains(pageResponse.Body.String(), `data-url="/openapi/v1.json"`) ||
		!strings.Contains(pageResponse.Header().Get("Content-Security-Policy"), "https://cdn.example.test") {
		t.Fatalf("invalid API reference response")
	}
}

func TestNewRejectsNonHTTPSScript(t *testing.T) {
	_, err := New(Config{
		OpenAPIPath:     "/openapi.json",
		ReferencePath:   "/docs",
		ScalarScriptURL: "http://cdn.example.test/scalar.js",
	})
	if err == nil {
		t.Fatal("expected invalid script URL to fail")
	}
}
