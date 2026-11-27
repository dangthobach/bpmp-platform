package odsruntime

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/go/platform/actorcontext"
)

// LoadActorVerifier loads an Ed25519 actor-context verifier when keyPath is set.
// Returns (nil, nil) if keyPath is empty (explicit local dev mode without signed proofs).
func LoadActorVerifier(keyID, workloadID, keyPath string) (*actorcontext.Verifier, error) {
	if strings.TrimSpace(keyPath) == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize || strings.TrimSpace(keyID) == "" || strings.TrimSpace(workloadID) == "" {
		return nil, errors.New("invalid actor proof public key configuration")
	}
	return actorcontext.New(actorcontext.Config{
		WorkloadID: workloadID,
		MaxBytes:   1 << 20,
		MaxRoles:   256,
		MaxCaps:    512,
		ClockSkew:  30 * time.Second,
		Keys:       map[string]ed25519.PublicKey{keyID: ed25519.PublicKey(raw)},
	})
}

// ActorProofMiddleware enforces Ed25519-signed actor context on authenticated domain API routes
// when verifier != nil. Health, metrics, and provider callback routes bypass browser actor proof.
func ActorProofMiddleware(verifier *actorcontext.Verifier, next http.Handler) http.Handler {
	if verifier == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/healthz" || p == "/readyz" || p == "/metrics" ||
			strings.HasPrefix(p, "/v1/documents/scan-callbacks/") ||
			strings.HasPrefix(p, "/v1/integration/callbacks/") {
			next.ServeHTTP(w, r)
			return
		}

		encoded := strings.TrimSpace(r.Header.Get("X-BPMP-Signed-Actor-Context"))
		if encoded == "" {
			writeUnauthorized(w, "missing signed actor context")
			return
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(raw) == 0 {
			writeUnauthorized(w, "malformed signed actor context")
			return
		}
		headerTenant := strings.TrimSpace(r.Header.Get("X-BPMP-Tenant-ID"))
		commandID := strings.TrimSpace(r.Header.Get("X-Command-ID"))
		verified, err := verifier.Verify(raw, headerTenant, commandID, time.Now().UTC())
		if err != nil {
			writeUnauthorized(w, "invalid or expired actor context")
			return
		}
		// Overwrite request headers strictly from the cryptographically verified proof.
		r.Header.Set("X-BPMP-Tenant-ID", verified.TenantID)
		r.Header.Set("X-BPMP-Actor-ID", verified.ActorID)
		r.Header.Set("X-BPMP-Capabilities", strings.Join(verified.Capabilities, ","))
		next.ServeHTTP(w, r)
	})
}

func writeUnauthorized(w http.ResponseWriter, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"title":"unauthorized","status":401,"detail":"` + detail + `"}`))
}
