# Dynamic Configuration P1

## Ownership

`configuration-service` owns immutable configuration profiles, publication
lifecycle, scope activation, audit, idempotency, and its PostgreSQL database.
API Gateway exposes the browser-safe facade but does not interpret configuration
values. `bpmp-engine` resolves published snapshots through typed gRPC during
runtime registry bootstrap and after a committed Kafka publication event.

No service reads the configuration database directly.

## Lifecycle and consistency

1. Create and draft commands declare an explicit owner and validate exactly one
   complete Protobuf policy: Engine, API Gateway, Human Runtime, Projection, or
   Governance.
2. Publish and rollback run in one PostgreSQL transaction with optimistic
   aggregate version, idempotency result, append-only audit, active scope claim,
   and transactional outbox record.
3. `configuration_active_scopes` has one row per tenant/owner/scope/reference. Its
   primary key prevents two profiles from becoming authoritative for the same
   scope during concurrent publication.
4. Rollback copies known-good content into a new published version. Historical
   version rows are never edited.
5. Engine startup resolves every verified WIR scope through mTLS gRPC. A missing
   or invalid published snapshot fails startup; there is no implicit default or
   static fallback when the remote resolver is configured.
6. One PostgreSQL publisher lease owns a contiguous outbox batch. Events publish
   by `event_sequence`; Kafka acknowledgement for the complete batch precedes
   the atomic `published_at` and checkpoint update. A crash replays the same
   Protobuf event IDs.
7. Engine commits a Kafka offset only after resolving the latest authoritative
   snapshot and replacing all affected registry entries at an exclusive safe
   point. Commands, boundary transitions, and local task completions hold a
   shared permit for their complete execution.
8. API Gateway and Human Runtime use the shared tenant cache consumer. They
   disable auto commit, validate publication metadata, resolve over mTLS, reject
   stale ordinals or hash mismatches, atomically replace an immutable snapshot,
   and only then commit the record.
9. API Gateway reads tenant rate limits, request/response bounds, total upstream
   deadline, bounded retry/backoff, circuit threshold/open interval, bulkhead,
   UI batch controls and encryption key scope from the cache. There is no
   reliability interceptor or tenant key-scope map in bootstrap configuration.
10. Human Runtime reads projection/escalation worker controls, Engine command
    timeout, retry/backoff/multiplier and retryable codes, circuit threshold and
    open interval, assignment claim bound, durable delegation-depth bound and
    query page bounds from the cache. Reliability changes apply per gRPC call
    without restarting the process.
11. Approved instance publications resolve with `instance_id` and install into
    an instance cache entry. They never replace the tenant snapshot. A workflow
    start uses the scoped policy after its bounded body has been decoded.

Profiles contain complete policies. Resolution selects the most specific
matching published profile in this order:

`platform < environment < tenant < workflow type < workflow version < approved instance override`

Reference conventions are:

- platform and environment: deployment-configured reference values;
- tenant: `tenant_id`;
- workflow type: `workflow_type`;
- workflow version: `workflow_type:workflow_version`;
- approved instance override: `instance_id`.

## Public API

Cockpit uses API Gateway routes under `/v1/configuration`:

- `GET/POST /profiles`
- `GET /profiles/{profile_id}`
- `POST /profiles/{profile_id}/versions`
- `POST /profiles/{profile_id}/versions/{version_id}/publish`
- `POST /profiles/{profile_id}/versions/{version_id}/rollback`

Gateway preserves the original JWT, tenant, correlation, command ID, and
idempotency key. Configuration Service verifies the JWT and configured
`configuration.read` or `configuration.manage` capability again.

The Cockpit supports typed policy editing, version history, publish, rollback,
and bounded batch publication. Batch size and concurrency are runtime
configuration values; each item has a stable idempotency key.

## Internal contract

`bpmp.configuration.v1.ConfigurationResolverService` returns a
`ResolvedConfigurationSnapshot` containing owner, ordinal,
schema/config/policy versions, resolved scope, content hash, and the matching
typed bounded-context policy. `ConfigurationPublicationEvent` is invalidation
metadata; consumers always resolve authoritative values over mTLS gRPC rather
than trusting Kafka as a configuration store.

## Remaining P1 breadth

Engine, API Gateway and Human Runtime runtime consumers are implemented and
covered by the broker-backed process E2E. All five typed schemas are supported
by Configuration Service. Remaining work is:

- create the real Projection and Governance deployable composition roots, then
  connect their typed snapshots to owned stores and safe lifecycle boundaries;
- expose browser-safe batch controls from the resolved Gateway snapshot instead
  of Cockpit deployment JSON;
- add workflow type/version scoped cache keys for non-Engine consumers when
  those request paths carry both dimensions;
- expose retire/restore lifecycle and diff query;
- add PostgreSQL query-plan gates and crash injection around every resolver,
  cache-install and offset-commit boundary.
