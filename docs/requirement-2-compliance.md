# Requirement 2 Compliance

Requirement 2 is not complete until every Human Runtime acceptance criterion
has executable production-path evidence. The service must not interpret WIR or
finalize workflow transitions before committed engine events.

## Current Assessment

| AC | Current implementation | Status | Remaining evidence |
| --- | --- | --- | --- |
| 2.1 | Committed-event Kafka consumer, typed activation, database-resolved assignment policy, atomic inbox/work-item/audit projection, and PostgreSQL 18 replay test | Core pass | Broker-backed Kafka integration and randomized assignment-policy property catalog |
| 2.2 | Durable completion intent, idempotent RPC retry, typed approval result, actor-preserving engine gRPC client, and finalization only from a committed event | Partial | Rust engine gRPC server binding and cross-service behavioral E2E |
| 2.3 | Database-configured SLA, crash-recoverable leased outbox, bounded worker, synchronous Kafka acknowledgement, retry, and PostgreSQL 18 lease test | Core pass | Broker-backed Kafka crash/recovery integration and broader virtual-clock property catalog |
| 2.4 | Optimistic delegation, immutable audit transaction, public gRPC API, conflict test, and PostgreSQL 18 integration | Core pass | Multi-client API load/concurrency suite |
| 2.5 | Pure stage/milestone lifecycle and PostgreSQL case projection | Partial | Committed case event contract, sentry evaluation inputs and E2E tests |
| 2.6 | PostgreSQL schema, `pgx/v5` adapter, optimistic versions, `is_deleted`, and migration/integration test on PostgreSQL 18 | Core pass | Backup/restore operational test before production readiness |
| 2.7 | Bounded Kafka/database paths; latest local PostgreSQL 18 run measured 200 reads at concurrency 8 with P95 1.0598 ms | Partial | End-to-end gRPC benchmark including actor verification and engine RPC |
| 2.8 | Append-only audit trigger and atomic audit writes for activation, completion request/commit, delegation, case transitions, and SLA escalation | Partial | Audit query API, cancellation projection, and audit-completeness property test |
| 2.9 | Original actor token/context forwarding through generated Go gRPC contracts with workload-substitution and gRPC boundary negative tests | Core pass | Concrete actor verifier and Rust engine authorization E2E |

## Verified Gates

- `go vet ./...`
- `go test -race ./...`
- PostgreSQL 18 migration and integration suite, including projection deduplication,
  stream-sequence collision, missing-dependency rollback, immutable audit,
  optimistic version conflict, escalation lease isolation, and local P95 measurement.
- Kafka consumer tests prove offsets are committed only after durable projection;
  escalation publisher tests prove broker acknowledgement failures remain retryable.

Passing unit tests is not sufficient to report Requirement 2 as 100% complete.
