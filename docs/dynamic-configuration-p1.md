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

Engine hot reload and all five typed schemas are implemented. Remaining work is:

- connect the typed API Gateway, Human Runtime, Projection, and Governance
  snapshots to their live runtime caches and safe reconfiguration boundaries;
- handle approved-instance overrides through an instance-scoped cache rather
  than replacing a workflow-wide registry entry;
- expose retire/restore lifecycle and diff query;
- add PostgreSQL query plans and broker crash/replay tests in an environment
  with Docker/PostgreSQL/Kafka available.
