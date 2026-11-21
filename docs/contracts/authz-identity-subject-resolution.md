# Authz IdP subject resolution contract

Status: internal Authz endpoints implemented. The Gateway OIDC callback/session
producer and profile update/deactivation mutations remain separate slices.

## Provision and link profile

`POST /admin/v1/identity/profiles` provisions a tenant membership and links it
to an external identity in one operation. It requires the
service capability `identity.profile.manage`, with the caller's tenant claim
equal to the request tenant, plus an `Idempotency-Key` header. The request is:

```json
{
  "tenant_id": "uuid",
  "username": "jane.doe",
  "issuer": "https://sso.example/realms/acme",
  "subject": "2d1a..."
}
```

This is an administrator-driven provisioning operation: the authorized
administrator is responsible for confirming that the submitted issuer/subject
belongs to the intended person through the organization’s IdP process. The
service authenticates and audits the administrator, but this endpoint does not
call the IdP to verify another person’s subject. A self-service account-link
flow requires a separate OIDC proof contract. The API creates an active access
profile with no roles; roles are granted separately by Authz. The profile row,
audit fingerprint, and idempotency result
commit in one PostgreSQL transaction. Identical retries return the original
profile. Reusing the same key with a different request returns `409
IDEMPOTENCY_CONFLICT`; a tenant username or IdP link already in use returns
`409 IDENTITY_PROFILE_CONFLICT`. Idempotency receipts expire after the
stage-proposed seven-day window. The audit record retains actor,
executor/workload, request ID, entity version and a one-way fingerprint of the
issuer/subject pair.

## Operation

`POST /admin/v1/identity/resolve-subject`

The route uses the existing service JWT middleware and requires the exact
workload capability `identity.subject.resolve`. It is not a browser endpoint
and must not be exposed through public Gateway routing. The caller passes only
the `iss` and `sub` from an already verified Keycloak ID token/access token;
Authz does not accept role or permission claims from Keycloak as BPMP grants.

### Request

```json
{
  "issuer": "https://sso.example/realms/acme",
  "subject": "2d1a..."
}
```

Both fields are required. Unknown fields are rejected. `issuer` is limited to
512 bytes and `subject` to 255 bytes after trimming. The caller is responsible
for validating the OIDC issuer, signature, audience, nonce and token lifetime
before calling this internal operation.

### Response

```json
{
  "profiles": [
    {
      "tenant_id": "uuid",
      "user_id": "uuid",
      "username": "jane.doe",
      "active": true,
      "version": 4,
      "attributes_version": 2,
      "roles": ["case.operator"]
    }
  ]
}
```

Only non-deleted active profiles in active tenants are returned. Roles are
resolved from active, unexpired Authz grants. No match returns `200` with an
empty `profiles` array, preventing the operation from becoming a user-existence
oracle across unauthorized callers. A caller missing the required service
capability receives the existing authorization-denied problem response; an
invalid body receives the existing request-validation problem response.

One IdP subject may map to multiple tenant profiles. The Gateway must require
an explicit tenant choice when there is more than one, bind that profile to the
server-side session, and re-check active membership/version when refreshing or
switching tenant context. It must not use the IdP token's tenant, group, role
or permission claims as BPMP authorization.

## Persistence and linking rules

`user_account.idp_issuer` and `idp_subject` are nullable as a pair. The exact
pair is unique within one tenant, and the pair has an index for resolving all
tenant profiles. A verified subject can be linked to multiple tenants because
each tenant owns a distinct BPMP access profile. Legacy `external_id` is not
backfilled: its issuer is unknown. Provisioning/account-linking must be an
audited Authz mutation with expected version, idempotency key and actor proof;
credentials and raw IdP tokens are never persisted.

## Acceptance

- Migration applies to existing and empty Authz databases.
- Null/null legacy links remain valid; half-populated links fail the DB check.
- `(tenant, issuer, subject)` cannot be linked twice to active profiles.
- Resolution matches both issuer and subject exactly.
- Disabled/deleted profiles, disabled tenants and expired roles are excluded.
- Only a service principal carrying `identity.subject.resolve` can call it.
- Provisioning requires `identity.profile.manage` and is tenant-bound.
- Concurrent identical provisioning requests produce one profile and replay
  the same response; mismatched idempotency payloads conflict.
- Audit evidence, profile creation, and replay result commit atomically.
- Subject and role changes take effect from Authz state, not IdP role claims.
# Actor context issuer (implemented, deployment-gated)

`POST /authz/v1/actor-context` is an internal service endpoint protected by the
Authz service JWT middleware and mTLS. It accepts a tenant UUID, configured
audience workload ID, and command/correlation ID. The caller's service token
must carry `identity.actor-context.issue`, a verified UUID actor ID, and the
same tenant. Authz loads the active user membership, tenant-global unexpired
role grants, and capabilities from its PostgreSQL authority; caller-supplied
role/capability claims are never accepted.

The response binds the actor context to tenant, actor, audience, command,
issue/expiry times, and the revoke epoch for that audience, and signs it using the Authz
actor-context Ed25519 key. Gateway checks returned scope, bounded lists, strict
base64, and a maximum 120-second lifetime before forwarding the proof. Engine
remains the final verifier and enforcer. Projection uses the query revoke epoch
and rejects query-revoked profiles. Other audiences do not reuse that query
epoch; their proof currently carries epoch zero, so any downstream identity
revoke floor above zero denies the proof until the dedicated
membership/identity-revoke epoch feed is wired. Database errors, revoked/inactive
memberships, invalid service delegation, missing signing configuration, or
malformed responses fail closed; no unsigned or cached-privilege fallback is
allowed.

Authz configuration when enabling the issuer:

| Setting | Default / constraint |
|---|---|
| `AUTHZ_ACTOR_CONTEXT_SIGNING_KEY_ID` + `AUTHZ_ACTOR_CONTEXT_SIGNING_KEY_PATH` | Required together; private seed is exactly 32 bytes |
| `AUTHZ_ACTOR_CONTEXT_AUDIENCES` | Required allowlist, 1–32 unique workload IDs |
| `AUTHZ_ACTOR_CONTEXT_TTL_MS` | Default 15000; range 1–120000 |
| `AUTHZ_ACTOR_CONTEXT_MAX_PROOF_BYTES` | Default 4096; range 1–65536 |
| `AUTHZ_ACTOR_CONTEXT_MAX_ROLES` | Default 256; range 1–4096 |
| `AUTHZ_ACTOR_CONTEXT_MAX_CAPABILITIES` | Default 512; range 1–4096 |

The proof size, role and capability limits must not exceed the corresponding
Engine `authorization.max_*` settings, and the encoded response must fit the
Gateway's configured upstream response limit. Configure the Authz public key in the
Engine actor keyring before enabling browser-session traffic. Missing issuer
configuration makes the endpoint unavailable and Gateway returns a retryable
upstream failure rather than proceeding without actor evidence.

The Gateway now implements OIDC Authorization Code + PKCE, nonce/state
single-use persistence, token exchange, signed ID-token verification, tenant
profile resolution, opaque session create/destroy, and CSRF bootstrap. It does
not persist provider access/refresh tokens. Keycloak RP-initiated/back-channel
logout and a verified end-user self-link flow remain separate follow-up work.

## Key provisioning gate

OIDC login is opt-in (`OIDC_ENABLED=false` by default). Before enabling it in a
browser-facing environment:

1. Provision an Ed25519 32-byte private seed as a secret mounted only into
   Authz. Set `AUTHZ_ACTOR_CONTEXT_SIGNING_KEY_ID` and
   `AUTHZ_ACTOR_CONTEXT_SIGNING_KEY_PATH` to that secret.
2. Derive the raw 32-byte public key using the platform's approved secret/key
   management workflow. Mount that public key read-only into Engine and add
   `{ "key_id": "portal-actor-context-k1", "path": "/run/secrets/portal-actor-context-public.key" }`
   to `authorization.actor_keys` in Engine runtime configuration. The key ID
   must exactly equal Authz's signing key ID.
3. Include `api-gateway` (the configured Gateway workload ID) in
   `AUTHZ_ACTOR_CONTEXT_AUDIENCES`. Add downstream audiences only where the
   Gateway deliberately routes signed actor proofs to those owners.
4. Restart Authz and Engine, then verify a signed test proof is accepted by
   Engine and rejected for unknown key ID, wrong audience, tampered content,
   expired validity, inactive membership, and revoked access before setting
   `OIDC_ENABLED=true`.

Private key bytes must never be checked in, copied into ordinary environment
variables, or mounted into Engine. Engine receives only the public key. Missing
or mismatched signing/keyring configuration fails closed; it must block the
browser-session traffic promotion gate.

## Profile and role mutation contract

`PUT /admin/v1/identity/profiles/{user_id}` updates username, exact IdP issuer
and subject link, active state, and expected profile version. Role grant and
revoke use `POST`/`DELETE /admin/v1/users/{user_id}/roles/{role_id}` with the
expected profile version. Each mutation requires a tenant-bound management
capability and `Idempotency-Key`; an identical retry returns its stored result,
while key reuse with a different request returns conflict. Profile version,
attribute cache version, audit fingerprint, and idempotency response commit in
the Authz PostgreSQL transaction. Concurrent stale writes return `409` and do
not partially mutate roles or profile links. Credentials remain exclusively
owned by Keycloak.
