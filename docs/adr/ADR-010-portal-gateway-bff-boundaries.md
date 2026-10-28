# ADR-010: Portal Gateway and Domain BFF Boundaries

Status: Accepted  
Date: 2026-10-28

## Context

The repository already has an API Gateway that authenticates, rate limits, preserves actor/correlation context and calls typed internal services. Adding one deployable BFF per MFE immediately would add network hops and operations cost before independent scaling or security needs are known. Keeping all presentation composition in one unstructured handler would create a distributed monolith and policy leakage.

## Decision

Evolve `api-gateway` into the ODS Portal Gateway. Introduce Task, Case, Document, Integration and Common BFFs first as cohesive internal modules behind versioned public REST contracts and explicit application ports.

A domain BFF may:

- Authenticate/coarsely authorize the browser session and enforce CSRF/rate limits.
- Map OpenAPI DTOs to typed internal requests.
- Compose bounded query calls with deadlines, cancellation, bulkheads and an explicit partial-failure policy.
- Preserve original actor proof, tenant, request/correlation/trace and idempotency metadata.

A domain BFF may not:

- Own a domain aggregate, workflow transition, authoritative idempotency result or cross-service transaction.
- Query another service's database.
- Convert dependency failure into business success without a domain-defined fallback.
- Use workload identity as end-user authorization.

Extract a BFF into a separate deployable only when independent scaling, security isolation, team/release cadence or blast-radius evidence justifies the hop. Its public contract and application port must remain stable through extraction.

## Failure behavior

- Cancellation and deadlines propagate to all upstream calls.
- Query fan-out is bounded. A partial response is returned only when the public contract explicitly models partial data and provenance.
- Command timeout after possible commit returns a typed indeterminate/retryable result; retry requires the same idempotency key.
- Circuit open, overload and upstream failure map to stable Problem Details codes without leaking internals.

## Consequences

- The first phases reuse mature gateway security/observability plumbing.
- Module boundaries and tests are required before physical service extraction.
- Domain services remain reusable by non-browser consumers because BFF DTO composition is not embedded in domain logic.
