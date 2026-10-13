# Query Authorization Implementation Progress

## Implemented Slice

- Get, List and Completed queries share actor verification, typed row filtering,
  tenant policy budgets and bounded PostgreSQL execution.
- List and Completed cursors are HMAC signed and bound to query scope, tenant,
  actor, policy, filter and sort. Client filters cannot replace authorization
  constraints; they are combined using AND.
- Tenant and actor minimum revoke epochs plus required maximum JWT age are
  published as ProjectionPolicy configuration. Readers use one policy snapshot
  per request, with no database/network lookup inside predicate compilation.
- Unknown protobuf enum values preserve their int32 value through lowering and
  fail validation instead of wrapping into valid DSL constants.
- Docker standalone Projection builds include their JWT dependency checksum.
- E2E fixtures include all/own, other-actor and denied query credentials. The
  harness accepts a local fixture generator to avoid unrelated Rust rebuilds.

## Verification

Executed on the local workspace on 2026-10-11:

- Buf lint and breaking comparison against HEAD passed.
- Projection, Configuration domain, Gateway and shared JWT Go tests passed.
- Go vet for Projection, Configuration domain and JWT passed.
- Race tests for Projection and JWT passed, including real PostgreSQL 17 tests
  with isolated schemas, persisted lifecycle/replay dedup, same-timestamp
  pagination, cross-actor cursors, row ownership, client OR, EXISTS/NOT EXISTS,
  revoke epochs and cross-tenant denial.
- Docker builds for Projection, Configuration Service and API Gateway passed.
- Broker-backed process E2E passed with three engine nodes and leader failover,
  Kafka/Redpanda, PostgreSQL, Human Runtime, Gateway, Projection and Governance.
  New query probes verified own-row visibility, no rows for another actor and
  HTTP 403 without query capability. Unchanged service images were reused;
  this run is not evidence of a fresh build/test of all Rust components.

The first process run exposed a harness defect: omitted empty protobuf arrays
were counted as a single PowerShell null. The corrected probe passed on rerun.

## Remaining Ordered Work

Update 2026-10-14: the typed remote PDP, database RBAC/ABAC, live ReBAC,
field authorization and durable ordered HTTP revoke feed are implemented.
Production Projection has no capability-only fallback. See [Query PDP](query-pdp.md).
The list below records the original plan; signed internal context, Kafka push
revocation and remaining performance/rebuild work are still pending.

1. Define a versioned typed query-PDP response owned by Authz, including row
   constraints, allowed fields and revocation freshness. Bind its effective
   version into cursors and reject unsupported constraints without fallback.
2. Integrate RBAC/ReBAC/ABAC, signed internal context and Authz revocation feed;
   capability all/own is not equivalent to those enterprise policies.
3. Add policy publication/cache-install E2E revocation, read-model rebuild and
   crash/replay scenarios, then PostgreSQL EXPLAIN and representative load tests.
4. Add metadata-driven query UI and saved queries only after field-level
   authorization is authoritative; do not rely on browser filtering for security.
5. Finish production key lifecycle/worker hardening and capacity benchmarks.

Existing Projection profiles need an explicit positive query_max_token_age_ms
before deploying this reader. Old unsigned List cursors must be discarded.
This slice does not establish completion of every P0 item or launch readiness.
