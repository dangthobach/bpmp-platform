# API Gateway OIDC PKCE and Browser Session Contract

Status: implemented in Gateway; deployment and Keycloak integration acceptance
remain pending.

## Login initiation

`GET /auth/oidc/login` starts OIDC Authorization Code + PKCE using S256. The
optional `tenant_id` query parameter is an untrusted profile selector and is
only honored if Authz returns an active profile for that exact tenant. Unknown
query parameters and duplicate selector values are rejected. Starting login
while a session cookie is present returns `409 active_session_exists`; the
browser must first end that session so the old opaque session is not left live.

Gateway creates cryptographically random state, nonce, and verifier, stores the
state record in Redis with a short TTL, and sets a Secure, HttpOnly,
SameSite=Lax `__Host-bpmp_oidc_state` cookie. Only SHA-256(state) is used in the
Redis key. The authorization redirect includes `code_challenge_method=S256`,
the derived challenge, configured issuer/client/redirect/scopes, state and
nonce. The client is a public PKCE client; no client secret is sent.

## Callback

`GET /auth/oidc/callback` requires exactly one matching state query value and
state cookie and one bounded authorization code. The state record is atomically
consumed before token exchange, making callback replay fail closed. Gateway
exchanges the code with the stored verifier over HTTPS with a deadline, rejects
redirects and oversized/non-JSON responses, then validates the ID token's
signature, issuer, audience, `azp` when there are multiple audiences, `exp`,
`iat`, subject and nonce. The access and refresh tokens are ignored and never
persisted.

Gateway calls the internal Authz subject resolver with the verified `iss` and
`sub` using the workload capability `identity.subject.resolve`. IdP role
claims are not used. A single active tenant profile is selected automatically;
multiple profiles return `409 tenant_selection_required` with tenant IDs. The
browser may restart login with one selected `tenant_id`; only a profile returned
by Authz can become the session. An unknown or inactive profile is denied.

For one selected profile Gateway creates a cryptographically random opaque
session token and persists only the hashed-token-keyed session record in Redis.
The session stores tenant, Authz profile UUID, CSRF digest and configured
expiry; it stores no IdP token or credentials. The cookie is Secure, HttpOnly,
SameSite=Lax and `__Host-` scoped. A separate Secure, readable,
SameSite=Strict `__Host-bpmp_csrf` cookie supplies the `X-CSRF-Token` header;
the API SDK reads that cookie automatically. The configured stage session
lifetime is fixed (recommended 30 minutes); sliding idle renewal is not yet
implemented.

## Logout

`POST /auth/session/logout` checks the CSRF value against the stored digest,
deletes the server session, clears session/CSRF/OIDC-state cookies and returns
204. A missing session is idempotent. If Redis cannot confirm deletion, Gateway
returns a retryable upstream error and does not claim logout succeeded.

## Bounds and failure behavior

Gateway configuration requires HTTPS issuer/authorization/token/redirect URLs,
an issuer present in the trusted JWT issuer list, `openid` scope, a 1–10 minute
state lifetime, a 1 minute–8 hour session lifetime, and a 1–10 second upstream
timeout. Token and Authz response bodies are capped at 1 MiB; authorization
codes are capped at 4096 bytes; state transactions are consumed once. Provider
errors, mismatched state/nonce/issuer/audience, missing membership, duplicate
cookies, and Redis/Authz outages never fall back to caller-supplied actor or
tenant claims.

Keycloak back-channel logout/account-disable propagation and the general
identity-revoke epoch feed are not implemented. A session can therefore remain
valid until its configured expiry after IdP-side logout/disable. JWKS currently
uses the configured static file; key rotation requires the deployment's key
reload/restart procedure. These are required before claiming the proposed
60-second revocation SLO or production stage acceptance.
