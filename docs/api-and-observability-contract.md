# API and Observability Contract

This contract applies to every BPMP deployable and transport boundary. It separates
transport observability from business identity and keeps typed Protobuf messages as
the internal API source of truth.

## Identifier Semantics

| Concept | Scope | Behavior |
|---|---|---|
| Request ID | One inbound request and its direct service calls | Accept a valid caller value or generate a 128-bit value. Echo it in the response. |
| Correlation ID | End-to-end business operation, including asynchronous work | Preserve across HTTP, gRPC, Kafka, retries, outbox publication, and replay. Defaults to the request ID only when a new operation starts. |
| Trace ID | One W3C distributed trace | Derived only from a valid `traceparent`. Never trust a custom caller-supplied trace-ID header. |
| Command ID | Authoritative write command | Business payload/idempotency metadata. It is not interchangeable with request or correlation ID. |
| Tenant ID | Authenticated tenancy scope | Populated only after identity/tenant validation. Transport metadata never grants tenant access. |

Request, correlation, command, and tenant identifiers are 1 to 128 ASCII characters
from `[A-Za-z0-9_.:-]`. Invalid request/correlation values are replaced at ingress.
Invalid authenticated business identifiers fail validation.

## HTTP

Every deployable applies the same logical stages at ingress:

`request metadata -> recovery -> security headers/authentication -> admission rate limit -> deadline -> validation -> handler -> typed response/problem mapping`

The physical wrapper order may place access logging outside recovery so the completion
record always observes the recovered status. Streaming routes must opt out of unary
request deadlines explicitly and retain bounded connection, heartbeat, replay and
outbound-buffer policies. Transport admission limits are process-local safety caps;
tenant and actor quotas remain policy-aware and distributed.

Inbound and response headers:

| Header | Request | Response |
|---|---|---|
| `traceparent` | Optional W3C Trace Context | Propagated by OpenTelemetry to downstream calls |
| `tracestate` | Optional W3C Trace Context | Propagated by OpenTelemetry to downstream calls |
| `X-Request-ID` | Optional | Always |
| `X-Correlation-ID` | Required for an existing business operation | Always |
| `X-Trace-ID` | Ignored | Present when a valid trace span exists |

Successful responses keep their endpoint-specific typed DTO. HTTP errors use
`application/problem+json` with this stable shape:

```json
{
  "type": "https://docs.bpmp.dev/problems/upstream_unavailable",
  "title": "Upstream unavailable",
  "status": 502,
  "code": "upstream_unavailable",
  "request_id": "7b43f95082c84aa7b73c60eb66d4e834",
  "correlation_id": "order-2026-00001234",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "retryable": true
}
```

`code` is the machine contract. `title` and `detail` are human-facing and must not
contain stack traces, secrets, SQL, internal addresses, or authorization details.

## gRPC

Internal APIs remain versioned Protobuf request/response messages. Do not wrap every
message in a generic envelope because that breaks streaming, generated clients, and
typed compatibility.

Canonical metadata keys are:

- `traceparent` and `tracestate` for W3C Trace Context.
- `x-bpmp-request-id` for the request ID.
- `x-bpmp-correlation-id` for the business correlation ID.
- `x-bpmp-trace-id` in server response metadata for log/support lookup.
- `x-bpmp-tenant-id` and `x-bpmp-command-id` only after their existing trust rules
  have been satisfied.

Errors use canonical gRPC status codes. Services must not return database or crypto
implementation errors directly. Stable domain error details may be added additively;
the status code remains the compatibility baseline.

Internal servers require a verified mTLS chain and an explicitly configured leaf
certificate SHA-256 allowlist. Method allowlists use fully-qualified gRPC paths.
Certificate trust is bootstrap configuration rather than tenant configuration because
the configuration resolver itself is part of the bootstrap dependency graph. A trusted
workload never replaces actor proof or resource-level authorization.

Unary deadlines, maximum message sizes, admission rate and burst are required runtime
configuration. Long-lived streams configure a separate stream lifetime instead of
inheriting the unary deadline. Panics are mapped to `INTERNAL`; deadline expiry is
mapped to `DEADLINE_EXCEEDED`, without exposing panic or dependency details.

## Kafka

Every produced record carries `x-bpmp-request-id`, `x-bpmp-correlation-id`, and,
when authenticated, `x-bpmp-tenant-id` and `x-bpmp-command-id`. Producers also
inject `traceparent` and `tracestate` when an active or persisted W3C context is
available. Existing `bpmp-*` event/schema headers remain unchanged.

Consumers extract the producer context before handling, create a consumer span, and
commit offsets only after the local durable effect succeeds. Replayed records retain
their correlation ID. An outbox must persist request, correlation, command, and any
available W3C context with the event; a publisher must not depend on an HTTP/gRPC
context still being alive. A publisher with no persisted W3C parent starts a new
delivery trace while preserving the business correlation ID.

## Logging

Go deployables emit JSON `slog` records with `service.name` and `service.version`.
Request completion records include `request_id`, `correlation_id`, `trace_id`,
transport, operation, status, and duration. Kafka completion records additionally
include topic and, for consumers, partition and offset. `BPMP_LOG_LEVEL` controls the
bootstrap log level before dynamic tenant configuration is available.

Rust deployables emit JSON tracing records. The shared tonic layer records the same
transport fields and propagates them through Governance-to-Engine and Raft peer calls.

Never log JWTs, actor proofs, workload signatures, idempotency values, configuration
secrets, decrypted event payloads, or raw PII. Sampling must not remove audit events;
audit and tracing are separate concerns.

Authenticated adapters enrich completion logs with `tenant_id`, `actor_id`,
`command_id`, `workflow_instance_id`, and effective `policy_version` when available.
Ingress metadata alone never makes these fields authoritative.

## Compatibility

- Header and metadata additions are backward compatible.
- Protobuf fields are additive and removed field numbers remain reserved.
- Error `code` values are stable; new values are additive.
- OpenAPI is authoritative for public HTTP DTOs and Problem Details.
- Buf lint and breaking checks are required for Protobuf changes.
