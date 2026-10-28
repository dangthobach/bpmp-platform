package gateway

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/core/ports"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/jwtauth"
)

type actorIdentity struct{ ID string }

type authenticatedActor struct {
	tenantID    string
	actorID     string
	proofType   authv1.ActorProofType
	signedProof []byte
}

type HandlerOption func(*Handler) error

func WithBrowserSessions(
	store ports.BrowserSessionStore,
	value config.BrowserSession,
) HandlerOption {
	return func(handler *Handler) error {
		if store == nil {
			return errors.New("browser session store is required")
		}
		if !value.Enabled {
			return errors.New("browser session configuration is disabled")
		}
		if err := value.Validate(); err != nil {
			return err
		}
		handler.sessionStore = store
		handler.sessionConfig = value
		return nil
	}
}

type verifier struct {
	value *jwtauth.Verifier
}

func newVerifier(value config.Identity) (*verifier, error) {
	shared, err := jwtauth.New(jwtauth.Config{
		JWKSPath: value.JWKSPath, Issuers: value.Issuers,
		Audiences: value.Audiences, Algorithms: value.Algorithms,
		MaxTokenBytes: value.MaxTokenBytes, MaxJWKSKeys: value.MaxJWKSKeys,
		ClockSkew: time.Duration(value.ClockSkewSeconds) * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &verifier{value: shared}, nil
}

func (v *verifier) verify(raw, tenantID string, now time.Time) (actorIdentity, error) {
	identity, err := v.value.Verify(raw, tenantID, now)
	if err != nil {
		return actorIdentity{}, err
	}
	return actorIdentity{ID: identity.Subject}, nil
}

func (h *Handler) authenticateActor(
	r *http.Request,
	tenantHeader string,
	now time.Time,
) (authenticatedActor, error) {
	authorizationValues := r.Header.Values("Authorization")
	if len(authorizationValues) > 1 {
		return authenticatedActor{}, errUnauthorized
	}
	authorization := ""
	if len(authorizationValues) == 1 {
		authorization = authorizationValues[0]
	}
	sessionTokens := h.browserSessionTokens(r)
	if authorization != "" && len(sessionTokens) != 0 {
		return authenticatedActor{}, errUnauthorized
	}
	if authorization != "" {
		if !strings.HasPrefix(authorization, "Bearer ") || tenantHeader == "" {
			return authenticatedActor{}, errUnauthorized
		}
		raw := strings.TrimPrefix(authorization, "Bearer ")
		actor, err := h.verifier.verify(raw, tenantHeader, now)
		if err != nil {
			return authenticatedActor{}, errUnauthorized
		}
		return authenticatedActor{
			tenantID: tenantHeader, actorID: actor.ID,
			proofType:   authv1.ActorProofType_ACTOR_PROOF_TYPE_ORIGINAL_JWT,
			signedProof: []byte(raw),
		}, nil
	}
	if h.sessionStore == nil || len(sessionTokens) != 1 {
		return authenticatedActor{}, errUnauthorized
	}
	token := sessionTokens[0]
	if token == "" || len(token) > h.sessionConfig.MaxTokenBytes {
		return authenticatedActor{}, errUnauthorized
	}
	session, err := h.sessionStore.Get(r.Context(), token)
	if errors.Is(err, ports.ErrBrowserSessionNotFound) {
		return authenticatedActor{}, errUnauthorized
	}
	if err != nil {
		return authenticatedActor{}, errUpstream
	}
	if !validID(session.TenantID) || !validID(session.ActorID) ||
		len(session.SignedActorContext) == 0 ||
		int64(len(session.SignedActorContext)) > h.sessionConfig.MaxRecordBytes ||
		len(session.CSRFTokenSHA256) != sha256.Size ||
		!session.ExpiresAt.After(now) ||
		(tenantHeader != "" && tenantHeader != session.TenantID) {
		return authenticatedActor{}, errUnauthorized
	}
	if !safeMethod(r.Method) && !h.validCSRFToken(r, session.CSRFTokenSHA256) {
		return authenticatedActor{}, errForbidden
	}
	return authenticatedActor{
		tenantID: session.TenantID, actorID: session.ActorID,
		proofType:   authv1.ActorProofType_ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT,
		signedProof: append([]byte(nil), session.SignedActorContext...),
	}, nil
}

func (h *Handler) browserSessionTokens(r *http.Request) []string {
	if h.sessionStore == nil {
		return nil
	}
	var values []string
	for _, cookie := range r.Cookies() {
		if cookie.Name == h.sessionConfig.CookieName {
			values = append(values, cookie.Value)
		}
	}
	return values
}

func (h *Handler) validCSRFToken(r *http.Request, expected []byte) bool {
	values := r.Header.Values(h.sessionConfig.CSRFHeaderName)
	if len(values) != 1 || values[0] == "" ||
		len(values[0]) > h.sessionConfig.MaxTokenBytes {
		return false
	}
	digest := sha256.Sum256([]byte(values[0]))
	return subtle.ConstantTimeCompare(digest[:], expected) == 1
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}
