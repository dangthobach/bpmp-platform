package config

import "testing"

func TestOIDCConfigurationRequiresTrustedSecurePKCEProvider(t *testing.T) {
	session := BrowserSession{Enabled: true}
	identity := Identity{Issuers: []string{"https://idp.example/realm"}}
	valid := OIDC{
		Enabled: true, Issuer: "https://idp.example/realm",
		AuthorizationEndpoint: "https://idp.example/realm/protocol/openid-connect/auth",
		TokenEndpoint:         "https://idp.example/realm/protocol/openid-connect/token",
		ClientID:              "bpmp-portal", RedirectURI: "https://portal.example/auth/oidc/callback",
		Scopes: []string{"openid", "profile"}, OperationTimeoutMS: 3000,
		TransactionTTLMS: 300000, SessionTTLMS: 1800000,
	}
	if err := valid.Validate(session, identity); err != nil {
		t.Fatalf("valid OIDC configuration rejected: %v", err)
	}
	tests := map[string]func(*OIDC){
		"http token endpoint":   func(value *OIDC) { value.TokenEndpoint = "http://idp.example/token" },
		"untrusted issuer":      func(value *OIDC) { value.Issuer = "https://evil.example/realm" },
		"missing openid scope":  func(value *OIDC) { value.Scopes = []string{"profile"} },
		"duplicate scope":       func(value *OIDC) { value.Scopes = []string{"openid", "openid"} },
		"unbounded transaction": func(value *OIDC) { value.TransactionTTLMS = 600001 },
		"unsafe redirect":       func(value *OIDC) { value.RedirectURI = "https://portal.example/callback?next=https://evil.example" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := valid
			value.Scopes = append([]string(nil), valid.Scopes...)
			mutate(&value)
			if err := value.Validate(session, identity); err == nil {
				t.Fatal("invalid OIDC configuration was accepted")
			}
		})
	}
}
