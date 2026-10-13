# Typed Query PDP and Durable Revocation

## Ownership and Request Path

Gateway forwards the original actor proof. Projection verifies JWT signature,
tenant, audience, age and configured capability ceiling, then calls Authz with
an independent workload JWT. Authz loads registered user attributes from its own
database; actor-provided attributes/roles cannot replace database RBAC/ABAC.
Engine transition authorization remains embedded and is not routed through this
remote query PDP.

`POST /authz/v1/query` takes `tenant_id`, `actor_jwt`, `resource_type`, `action`.
The workload needs `authz.filter` and a matching tenant claim. The versioned JSON
response contains `schema_version`, tenant/actor binding, ALLOW/DENY,
`policy_version`, `revoke_epoch`, `revoke_sequence`, explicit `all_rows`, typed
`row_filter`, `allowed_fields`, `masked_fields`. No SQL is transported.

Row AST supports AND/OR, resource-field versus literal comparisons (eq, neq,
in, not_in, gte, lte). Authz resolves user attributes to literals and bounded
ReBAC row relations (`exists` with a `resource.<field>` target) to IN lists.
ReBAC objects must use the same identifiers as the target read-model field.
Empty relation results currently deny the request, rather than returning an
empty page. Other operators, sources and fields fail closed.

Query authorization uses live temporal/relationship data instead of stale
materialized positive grants. Role-permission conditions are ANDed with policy
conditions; ReBAC leaves retain the AST's AND/OR semantics. Explicit DENY wins.
For multiple matched ALLOW permissions, the first applicable permission scope
is selected; its row restrictions are intersected, not unioned across grants.
Own/branch permissions without an explicit row restriction deny the query.

Each query reads the security revision before user/policy resolution and after
evaluation. Policy, role, attribute, relationship or revoke mutations advance
it transactionally. A concurrent change denies the decision. Authorization is
at the request safe point: a revoke committed after that point affects later
requests, not an already executing database transaction.

## Field Authorization

Fields use Protobuf snake_case read-model names, including epoch timestamp
names, `duration_ms`, `event_count`, and event-query names `event_type`,
`event_node_id`, `event_actor_id`, `event_sequence`, `event_occurred_at`,
`event_config_version`, `event_policy_version`.

Empty allowed_fields has the canonical Authz meaning: all known fields.
Unknown names deny the decision. Masked fields are removed, not replaced with
partial-value patterns. Hidden fields cannot be used for filters, sort or
event predicates; include_deleted requires access to is_deleted. Stable
pagination requires query access to instance_id and the selected sort fields.
Trusted PDP row predicates may constrain hidden fields without exposing them.
Cleared protobuf scalars may serialize as absent or default values depending
on the HTTP serializer; the original value is not returned.

## Deployment

1. Apply Authz migration `014_query_pdp_revocation.sql` before updated binaries.
2. Set a positive `query_max_permissions` in each tenant's Authz config through
   tenant administration. Missing/excessive values deny queries. The query
   fetch is bounded before materialization. The safety ceiling is 10000 rows.
3. Publish ProjectionPolicy `query_pdp_resource_type`, `query_pdp_action`, and
   required token age/budgets through Configuration Service.
4. Configure Projection `query_pdp` transport: URL, workload_token_path,
   timeout_ms, max_response_bytes, allow_insecure. HTTPS is required unless
   insecure transport is explicitly enabled for a development environment.
   Workload tokens must match the queried tenant; a single-token deployment
   must provision additional tenant-scoped workload credentials before serving
   more tenants. Tenant onboarding does not automatically mint workload JWTs.
5. Provision database permissions, row filters and field filters. A JWT query
   capability alone is not a database permission grant.
6. Expired workload credentials must be rotated. Tokens are read bounded at
   request start; deployment secret files are never included in browser config.
   Projection's workload token should carry authz.filter only, not authz.revoke;
   the E2E generator issues a separate projection-pdp.jwt credential.

The query timeout covers PDP and PostgreSQL, not just SQL execution. PDP errors,
redirects, oversized/malformed responses, missing scope or unsupported schema
deny access. There is no cache/legacy-capability fallback in the production
composition root. Cursor identity changes with PDP/revoke/field policy changes.

## Durable Revoke Feed

`POST /admin/v1/query-revoke` requires tenant-scoped `authz.revoke` workload
capability and `{tenant_id, actor_id, expected_version, revoked, reason}`.
The short transaction locks the actor, checks its optimistic version, advances
epoch and entity version, then appends an immutable feed/audit entry containing
the reason and performing workload. A stale version is a conflict. Restore
sets revoked=false but advances epoch again, so old tokens remain invalid.
Legacy revoke/clear routes now use this transaction and also require tenant,
expected version and reason; consumers must update their request contract.

`POST /authz/v1/query-revoke-feed` requires tenant-scoped `authz.filter` and
`{tenant_id, after, limit}` (limit 1..1000). It returns ordered events and
next_sequence. Persist a subscriber cursor only after installing a batch;
replay with the old cursor on a crash. Sequence gaps are valid because policy
writes and other tenants share the global security revision. Revision locking
serializes administrative writes so a committed feed checkpoint cannot skip
an earlier uncommitted event. Feed entries cannot be updated or deleted.

Projection does not maintain a background revoke cache: every PDP response
reads the authoritative actor epoch. The HTTP feed supports direct subscribers
and replay; Kafka push publication/signing/offline query bundles are not part
of this implementation. This favors immediate freshness and fail-closed
availability over a disconnected query path.

## Verification and Remaining Work

Added Go transport/binding/AST tests, field inference/cursor tests and response
redaction tests. Added PostgreSQL transaction rollback/append-only tests and
RBAC+ABAC+live-ReBAC integration tests. Process E2E exercises actual PDP
conditions, row scope, field masking, hidden-field denial, revoke/restore,
feed resume, Control Plane restart and three-node engine failover.

Executed successfully on 2026-10-14: Buf lint/breaking against HEAD; Go vet and
Projection/Configuration-domain race tests; cargo test for authz-db,
authz-engine and authz-server with real PostgreSQL; Docker builds of changed
services; broker-backed process E2E using a dedicated Projection workload
credential. Test containers and isolated E2E state were removed afterward.
Native Windows/PostgreSQL test fixtures use a larger traversal timeout than
the in-container production profile; the real E2E exercises production
configured ReBAC deadlines. Existing sqlx 0.7.4 future-compatibility warnings
remain a dependency-maintenance item.

External JIT attributes and shift verification have no concrete provider here
and fail closed. Signed internal actor contexts, custom masking patterns,
arbitrary AST operators and cross-service joins remain outside this query
profile. The global admin-write revision and per-request remote PDP need
representative load benchmarks before any 300k-CCU claim; this work does not
establish launch readiness or complete all enterprise authorization catalogues.
