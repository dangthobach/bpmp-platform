package config

import "testing"

func TestAPIDocsValidation(t *testing.T) {
	t.Parallel()
	valid := APIDocs{
		Enabled:         true,
		OpenAPIPath:     "/openapi/v1.json",
		ReferencePath:   "/docs",
		ScalarScriptURL: "https://cdn.example.test/@scalar/api-reference@1.63.0",
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]APIDocs{
		"reserved API path": {
			Enabled: true, OpenAPIPath: "/v1/openapi.json", ReferencePath: "/docs",
			ScalarScriptURL: valid.ScalarScriptURL,
		},
		"same path": {
			Enabled: true, OpenAPIPath: "/docs", ReferencePath: "/docs",
			ScalarScriptURL: valid.ScalarScriptURL,
		},
		"insecure script": {
			Enabled: true, OpenAPIPath: "/openapi.json", ReferencePath: "/docs",
			ScalarScriptURL: "http://cdn.example.test/scalar.js",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := value.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
