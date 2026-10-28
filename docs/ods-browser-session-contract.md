# ODS browser session contract

Status: Phase 1 implementation contract

Version: 1

Owner: Unified Portal produces sessions; Portal Gateway consumes them.

## Trust boundary

The browser receives only an opaque session token in a cookie issued by Unified Portal with `HttpOnly`, `Secure`, `SameSite` and `Path=/` attributes. Access and refresh tokens never enter browser storage. Portal Gateway does not call OIDC on each request; it performs one bounded session-store lookup.

The session token has at least 256 bits of entropy. Redis keys never contain the raw token:

```text
<configured-prefix>:<lowercase-hex(sha256(raw-session-token))>
```

The store is shared only as this versioned contract; neither side imports the other's implementation.

## Redis value schema v1

The value is a bounded JSON object. Byte fields use standard JSON base64 encoding.

```json
{
  "schema_version": 1,
  "tenant_id": "tenant-a",
  "actor_id": "actor-1",
  "signed_actor_context": "<base64 signed context>",
  "csrf_token_sha256": "<base64 32-byte SHA-256 digest>",
  "expires_at_epoch_ms": 1793232000000
}
```

Required invariants:

- `tenant_id` and `actor_id` are valid BPMP identifiers.
- `signed_actor_context` is the immutable Portal-signed actor proof forwarded as `ACTOR_PROOF_TYPE_SIGNED_INTERNAL_CONTEXT`.
- `csrf_token_sha256` is exactly 32 bytes. The raw CSRF token is delivered through a Portal bootstrap response, never stored in this record.
- `expires_at_epoch_ms` and the Redis TTL use the same expiry; the earlier expiry wins.
- Schema versions other than `1`, malformed/oversized records and store errors fail closed.
- Additive fields are allowed within schema v1; semantic or required-field changes require a new version and a dual-read rollout.

## Request behavior

- Bearer JWT remains supported for non-browser clients during migration.
- A request carrying both Bearer and the BFF cookie, duplicate BFF cookies, or duplicate Authorization headers is rejected.
- Session tenant is authoritative. An optional `X-BPMP-Tenant-ID` must match it.
- `GET`, `HEAD` and `OPTIONS` do not require CSRF. Other methods require exactly one configured CSRF header whose SHA-256 digest matches in constant time.
- Expired/missing/invalid sessions return a generic authentication failure. Store failures return stable retryable Problem Details without leaking internals.
