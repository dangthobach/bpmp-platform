# Projection query performance

## Repeatable plan gate

`TestRepresentativeCompletedQueryPlan` creates an isolated schema, applies the
projection migrations, generates a bounded representative dataset, runs the
real SQL compiler output through `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`, and
drops the schema. It fails if the event relation performs a sequential scan,
if no index is used, or if the query returns more than its lookahead limit.

Run it against a disposable PostgreSQL database:

```powershell
$env:BPMP_PROJECTION_PLAN_DSN = "postgres://user:password@host/database?sslmode=require"
$env:BPMP_PROJECTION_PLAN_PROCESSES = "100000"
$env:BPMP_PROJECTION_PLAN_EVENTS_PER_PROCESS = "8"
go test -run TestRepresentativeCompletedQueryPlan -v ./internal/adapter/postgres
```

Never point the harness at a database containing data that must be retained. It
creates and drops a uniquely named schema, but a disposable database remains
the required operational boundary.

## Measured baseline

Measured on 2026-10-17 using PostgreSQL 16.1 on the local development host:

| Processes | Events | Page lookahead | Planning | Execution | Relevant indexes |
|---:|---:|---:|---:|---:|---|
| 100,000 | 800,000 | 51 | 6.141 ms | 0.713 ms | `idx_workflow_instances_completed_type_trim_lower`, `idx_workflow_events_type_trim_lower_instance`, `idx_workflow_events_instance_sequence` |

The workload selected completed `approval` processes through `TRIM + LOWER`,
required a matching normalized completion event, and ordered by completion
descending, normalized workflow type, then instance ID. The event relation did
not use a sequential scan.

`ABS` and UTC date bucketing intentionally have no blanket expression indexes.
They remain typed and bounded query features, but each additional functional
index increases projection write cost. Add a targeted index only after this
plan harness (or a sanitized production-like snapshot) demonstrates a recurring
selective predicate and stable benefit for that exact field/transform pair.

This is a synthetic cardinality/selectivity baseline, not a claim about
production latency. Re-run against a sanitized staging snapshot with actual
tenant skew, event counts, string distributions, cache state, concurrent load,
storage and PostgreSQL settings before setting an SLO. Record both cold and warm
plans; investigate row-estimate drift and buffer reads rather than comparing
execution time alone.
