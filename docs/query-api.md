# Projection query API

The Projection Service owns all typed process and event queries. The API Gateway forwards the signed actor proof; it does not authorize fields or add SQL fragments. Clients must discover the effective actor/tenant vocabulary through `GET /v1/process-instances/query-capabilities`.

Public endpoints:

- `POST /v1/process-instances/query`: scoped process query, multi-level ordering, selection, collapse, and bounded event expansion.
- `POST /v1/process-instances/aggregate`: bounded grouping and typed `COUNT`, `SUM`, `AVG`, `MIN`, and `MAX` metrics.
- `POST /v1/process-events/query`: event query with process-row authorization and signed keyset pagination.
- `POST|GET|DELETE /v1/saved-queries`: owner-scoped saved queries with optimistic versions.
- `POST|GET|DELETE /v1/query-exports`: durable asynchronous CSV exports.
- `POST /v1/ops/query-plan`: redacted `EXPLAIN` or `EXPLAIN ANALYZE`; requires `ProjectionPolicy.query_diagnostics_capability`.

Query validation failures use `application/problem+json` and include `violations[]` with `path`, `rule`, and `message`. Cursor tokens are signed, query-bound, size-limited, and rejected after a filter/sort/scope change.

## Deployment

Apply `db/projection-service/migrations/005_query_api.sql` before starting a binary containing the export worker. Saved-query and export rows remain inside the Projection bounded context. Export jobs snapshot the authorized row filter, selected/readable fields, and policy version at acceptance time; actor credentials are never persisted. The worker uses `FOR UPDATE SKIP LOCKED`, reclaims stale claims after five minutes, limits output to 100,000 rows and 16 MiB, and neutralizes spreadsheet formula prefixes.

Set `query_diagnostics_capability` only on operations profiles. When it is absent, plan diagnostics fail closed. Diagnostic responses exclude SQL, bind values, relation/index names, predicates, and constants.

## Performance verification

Synthetic plan regression is covered by the Projection PostgreSQL integration harness described in `docs/query-performance.md`. Production acceptance still requires running the same typed workload against a sanitized production-sized dataset and recording `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` p95/p99 evidence; the repository cannot manufacture production-distribution evidence.
