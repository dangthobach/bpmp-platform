# ADR-013: Notification Service Boundaries, Versioned Templates and Delivery Runtime

- Status: Accepted
- Date: 2026-11-26
- Related: ADR-004, ADR-006, ADR-007, ADR-008, ADR-009, ADR-010, ADR-011
- Design: [Notification Service](../refactor-prototype/appendix/notification-service.md)

## Context

ADR-011 names Notification as a bounded context, but the repository has no
notification deployable, contract, topic or migration. The earlier proposed
schema stored one template version per locale, and the API catalog covered only
template CRUD and preferences.

The product requires template, application and service-provider management;
mail and notification preview; delivery monitoring, metrics, audit logs and
circuit-breaker operations; manual sending; template versioning and
placeholders; and loose coupling with the services already deployed.

Constraints:

- Delivery failure must not roll back committed business or workflow state.
- Traffic volumes and latency SLOs have not been measured.
- Engine committed events are published as `bpmp.engine.v1.EventEnvelope`
  Protobuf and current consumers decode them directly. Remote worker
  assignments carry an empty `input_payload`, and the compiler rejects
  `sendTask`.
- Developers author templates.
- Provider protocols must be selectable by configuration. Zalo ZNS is needed
  only as mock data for now.

## Decision

1. **Bounded context.** Notification is a Go deployable family under
   `apps/go/notification-service` with its own PostgreSQL database, credential
   and migrations. It owns templates, layouts, releases, categories,
   applications, provider profiles, routing policies, trigger rules, contact
   points, preferences, suppressions, requests, deliveries and audit evidence.
   Runtime tuning is a new `NotificationPolicy` resolved through Configuration
   Service. Provider secrets and payload keys stay in Vault. Capabilities are
   published by Authz Control Plane.
2. **One request contract.** Producers send `template_key` and `schema_major`,
   JSON data, recipients by subject reference or external address, an
   idempotency key, category, lane and time bounds. Producers do not choose
   providers, channels or content. Entry paths are a producer outbox to
   `bpmp.notification.requests.v1.<environment>`, trigger rules over committed
   events, and gRPC `SubmitNotification`, which returns an acceptance receipt
   and never waits for a provider. Application identity comes from workload mTLS
   or the authenticated Kafka principal, never from payload fields.
3. **Versioning.** A published template version is immutable. It contains a
   variable schema with its `schema_major`, a syntax identifier, a pinned layout
   version and channel × locale variants; a variant is rendered inline or mapped
   to a provider-registered template. Within one major, a new version must
   accept every payload valid for the previous one; breaking changes create a
   new major. Release pointers carry monotonic ordinals, and rollback or
   scheduled activation creates a new release. Every delivery records template
   version, variant, content hash and release ordinal. There is no implicit
   platform-to-tenant fallback: shared templates and environment promotion use
   import with provenance and an unchanged content hash. Layouts, provider
   profiles, routing policies and trigger rules use the same publication
   mechanism.
4. **Rendering.** Go `html/template` and `text/template`, restricted by a
   publish-time AST allowlist; a comparable subset of JSON Schema 2020-12 for
   variable schemas; CEL for rule conditions, data mappings and HTTP-driver
   response classification. Authors may choose `syntax = handlebars/v1`
   (the prototype UI syntax); it is compiled to the `gotmpl/v1` AST and passes the
   same allowlist and validator, so `gotmpl/v1` stays valid and published versions
   are unaffected. Developers author templates in a code editor and
   through a template-as-code CLI. MJML, if used, is compiled at authoring time.
   Preview and dispatch share one renderer and the same compiled artifacts.
5. **Providers.** Protocol drivers (`smtp`, `http`, `mock`, `inapp`; `smpp` only
   when a carrier requires it) are code. Provider profiles and presets are data
   validated by each driver's JSON Schema. Zalo ZNS uses a mock preset until a
   real contract exists. The HTTP driver enforces a host allowlist, SSRF
   protection, TLS verification and response bounds.
6. **Delivery runtime.** PostgreSQL is authoritative for delivery state. River
   is the job queue for the `transactional`, `normal` and `bulk` lanes, scheduled
   sends, retries, outbox relay, cleanup and rollups, behind a `DispatchQueue`
   port. A lane moves to Kafka only when a load test at twice its estimated peak
   breaches the lane SLO hypothesis or the database headroom budget. Every
   provider profile has a circuit breaker, bulkhead and rate limit. Manual
   breaker overrides are audited and expire. Messages are deferred or expired,
   never dropped silently.
7. **Workflow notifications.** Sending after a workflow step completes is a
   versioned trigger rule over `bpmp.engine.committed-events`; it requires no
   Engine, WIR or compiler change. Its idempotency identity is tenant, rule,
   rule revision and event ID. Only variables declared by active rules are
   retained, encrypted. A remote-worker send path is deferred until a workflow
   must wait for notification acceptance and Engine supplies a bounded
   `input_payload`.
8. **Data protection.** Variables marked secret are never persisted in
   plaintext and are erased when the delivery ends or expires. Rendered content
   is not stored unless the category requires a retained copy. Retention values
   live in `NotificationPolicy`; the initial proposal is in the design document.
9. **Technology.** Go with `net/http` and grpc-go, pgx/v5, River pinned exactly
   at v0.51.x, franz-go, go-redis, `santhosh-tekuri/jsonschema/v6`, cel-go,
   `sony/gobreaker/v2`, `wneessen/go-mail` v0.8.2 or later, OpenTelemetry and
   UUIDv7 identifiers. No web framework and no ORM. The frontend follows
   ADR-009, with Monaco Editor private to `notification-mfe`.

## Prerequisites

- The shared Go workspace (`go.work`, module `go.mod` files, CI workflows and
  container builder images) is upgraded from `go 1.25.12` and qualified on Go
  1.27.3 under ADR-006 (superseding the earlier `1.26.5` baseline to include
  later `html/template` security fixes and satisfy `wneessen/go-mail` v0.8.2's
  Go 1.26.10+ requirement).
- River is pre-1.0 and licensed MPL-2.0. It requires license approval and exact
  pinning. Its exported migrations are versioned in
  `db/notification-service/migrations` with the checksum ledger in a dedicated
  technical queue schema.

## Failure behavior

- Notification unavailable: outbox producers keep committing and retry
  publication; gRPC callers receive a typed retryable error and retry with the
  same idempotency key.
- Kafka unavailable: accepted requests keep dispatching from PostgreSQL;
  delivery, in-app and invalidation events wait in the outbox.
- Provider failure: the breaker opens and routing fails over only for retryable
  or throttled outcomes. Without a healthy route the delivery is `DEFERRED`
  until `expires_at`, then `EXPIRED`.
- Ambiguous provider outcome: the category policy decides between resend,
  reconcile-before-retry and no retry.
- Data invalid for the resolved schema: `REJECTED` with a reason, or
  `INVALID_ARGUMENT` on the synchronous path. Nothing is partially rendered.
- Duplicate request, event or callback: the command receipt or inbox returns the
  prior effect. Unknown schema or poison input follows the bounded DLQ path of
  ADR-011.
- Invalid configuration publication: the reloader fails closed as in other
  services.

## Consequences

- Producers depend only on generated contracts and `template_key@major`;
  channel, provider, content and internal contact resolution stay inside
  Notification.
- Immutable versions let compiled templates and validators be cached without
  invalidation; only release heads and breaker overrides are invalidated via
  `bpmp.notification.invalidations.v1.<environment>`.
- Additive contract work: `CONFIGURATION_OWNER_NOTIFICATION = 9`,
  `NotificationPolicy` (`field 17`), `runtimeconfig` and Configuration Service
  validation, package `bpmp.notification.v1`, and recipient-bound in-app hints
  in Cockpit Gateway.
- Topics are provisioned with their first producer and consumer (ADR-011). The
  provisional Phase 0 name `bpmp.ods-notification.delivery.v1` is replaced by
  `bpmp.notification.delivery-events.v1`.
- The notification model in `schema.dbml` is replaced by this design (35 tables,
  41 intra-owner references). The API catalog generator
  (`tools/build-api-design.py`) is extended in the first implementation slice.

## Open conditions before production

- Business confirmation of the lane SLO hypotheses.
- Legal confirmation of retention values.
- River license approval and Go workspace upgrade evidence.
- Load-test evidence for the queue decision and per-provider limits.
- Product-owner confirmation of decisions D1-D9 and APNs support in
  [prototype review](../refactor-prototype/appendix/notification-prototype-review.md).
