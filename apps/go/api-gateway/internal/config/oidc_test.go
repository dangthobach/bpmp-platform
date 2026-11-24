package config

import "testing"

func TestOIDCConfigurationRequiresTrustedSecurePKCEProvider(t *testing.T) {
	session := BrowserSession{Enabled: true}
	valid := OIDC{
		Enabled: true, Issuer: "https://idp.example/realm",
		AuthorizationEndpoint: "https://idp.example/realm/protocol/openid-connect/auth",
		TokenEndpoint:         "https://idp.example/realm/protocol/openid-connect/token",
		JWKSURL:               "https://idp.example/realm/protocol/openid-connect/certs",
		CAFile:                "/etc/bpmp/oidc-ca.pem",
		ClientID:              "bpmp-portal", RedirectURI: "https://portal.example/auth/oidc/callback",
		FrontendURL: "https://portal.example/",
		Scopes:      []string{"openid", "profile"}, OperationTimeoutMS: 3000,
		TransactionTTLMS: 300000, SessionTTLMS: 1800000,
	}
	if err := valid.Validate(session); err != nil {
		t.Fatalf("valid OIDC configuration rejected: %v", err)
	}
	tests := map[string]func(*OIDC){
		"http token endpoint":   func(value *OIDC) { value.TokenEndpoint = "http://idp.example/token" },
		"insecure issuer":       func(value *OIDC) { value.Issuer = "http://idp.example/realm" },
		"missing JWKS endpoint": func(value *OIDC) { value.JWKSURL = "" },
		"missing openid scope":  func(value *OIDC) { value.Scopes = []string{"profile"} },
		"duplicate scope":       func(value *OIDC) { value.Scopes = []string{"openid", "openid"} },
		"unbounded transaction": func(value *OIDC) { value.TransactionTTLMS = 600001 },
		"unsafe redirect":       func(value *OIDC) { value.RedirectURI = "https://portal.example/callback?next=https://evil.example" },
		"unsafe frontend":       func(value *OIDC) { value.FrontendURL = "https://evil.example/" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := valid
			value.Scopes = append([]string(nil), valid.Scopes...)
			mutate(&value)
			if err := value.Validate(session); err == nil {
				t.Fatal("invalid OIDC configuration was accepted")
			}
		})
	}
}
