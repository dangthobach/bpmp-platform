# Dynamic Configuration P1

## Ownership

`configuration-service` owns immutable configuration profiles, publication
lifecycle, scope activation, audit, idempotency, and its PostgreSQL database.
API Gateway exposes the browser-safe facade but does not interpret configuration
values. `bpmp-engine` resolves a published snapshot through typed gRPC during
runtime registry bootstrap and passes that immutable snapshot into the existing
`ConfigurationProviderPort`.

No service reads the configuration database directly.

## Lifecycle and consistency

1. Create and draft commands validate a complete Protobuf `EnginePolicy`.
2. Publish and rollback run in one PostgreSQL transaction with optimistic
   aggregate version, idempotency result, append-only audit, active scope claim,
   and transactional outbox record.
3. `configuration_active_scopes` has one row per tenant/scope/reference. Its
   primary key prevents two profiles from becoming authoritative for the same
   scope during concurrent publication.
4. Rollback copies known-good content into a new published version. Historical
   version rows are never edited.
5. Engine startup resolves every verified WIR scope through mTLS gRPC. A missing
   or invalid published snapshot fails startup; there is no implicit default or
   static fallback when the remote resolver is configured.

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
`ResolvedConfigurationSnapshot` containing schema/config/policy versions,
resolved scope, content hash, and typed `EnginePolicy`. Contract lint and
generated-code drift are checked with Buf.

## Remaining P1 breadth

This slice removes static Engine policy files from the broker-backed E2E
bootstrap. The next Dynamic Configuration increments are:

- publish the configuration outbox to Kafka and hot-reconcile Engine registry
  entries only at migration-safe points;
- add bounded-context schemas for Gateway rate limit/circuit breaker, Human
  Runtime SLA/escalation, Projection batching, and Governance/KMS policy;
- expose retire/restore lifecycle and diff query;
- add PostgreSQL query plans and broker crash/replay tests in an environment
  with Docker/PostgreSQL/Kafka available.
