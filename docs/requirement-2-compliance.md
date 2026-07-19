# Requirement 2 Compliance

Requirement 2 is not complete until every Human Runtime acceptance criterion
has executable production-path evidence. The service must not interpret WIR or
finalize workflow transitions before committed engine events.

## Current Assessment

| AC | Current implementation | Status | Remaining evidence |
| --- | --- | --- | --- |
| 2.1 | Committed-event Kafka consumer, typed activation, database-resolved assignment policy, atomic inbox/work-item/audit projection, and PostgreSQL 18 replay test | Core pass | Broker-backed Kafka integration and randomized assignment-policy property catalog |
| 2.2 | Durable completion intent, idempotent RPC retry, typed approval result, actor-preserving Go client, bounded Tonic server, authoritative Rust wire-handler E2E, and finalization only from a committed event | Core pass | Deployment-level Go-to-Rust process test with RocksDB rather than in-memory store |
| 2.3 | Database-configured SLA, crash-recoverable leased outbox, bounded worker, synchronous Kafka acknowledgement, retry, and PostgreSQL 18 lease test | Core pass | Broker-backed Kafka crash/recovery integration and broader virtual-clock property catalog |
| 2.4 | Optimistic delegation, immutable audit transaction, public gRPC API, conflict test, and PostgreSQL 18 integration | Core pass | Multi-client API load/concurrency suite |
| 2.5 | Committed case/sentry event contract and PostgreSQL 18 E2E projection with atomic inbox, stage transition and audit | Partial | Connect compiled CMMN sentry evaluator to authoritative Rust engine lifecycle; broaden beyond the current CMMN subset |
| 2.6 | PostgreSQL schema, `pgx/v5` adapter, optimistic versions, `is_deleted`, and migration/integration test on PostgreSQL 18 | Core pass | Backup/restore operational test before production readiness |
| 2.7 | Full two-hop gRPC benchmark covers Human gRPC, concrete JWT verification, application service and Engine gRPC: latest 200-request run at concurrency 8 measured P95 828 us; PostgreSQL 18 durability path measured P95 794.3 us | Core pass | Repeat under production topology/load profile and retain trend history |
| 2.8 | Append-only audit trigger, atomic audit writes, committed cancellation projection, capability-gated tenant-scoped audit query and keyset pagination | Core pass | Broader audit-completeness property catalog and production retention/archival evidence |
| 2.9 | Concrete JWT/JWKS and signed-context verifier with hot-reloadable keys, command/audience/tenant/time/revoke checks; original proof forwarding; Rust re-authorization and workload-substitution negative tests | Core pass | Production identity/JWKS refresh failure and key-rotation chaos test |

## Verified Gates

- `go vet ./...`
- `go test -race ./...`
- PostgreSQL 18 migration and integration suite, including projection deduplication,
  stream-sequence collision, missing-dependency rollback, immutable audit,
  optimistic version conflict, escalation lease isolation, and local P95 measurement.
- Kafka consumer tests prove offsets are committed only after durable projection;
  escalation publisher tests prove broker acknowledgement failures remain retryable.
- Tonic socket test proves generated Rust client/server compatibility and bounded
  message configuration; authoritative handler test proves authorization, commit,
  idempotency, and duplicate receipt behavior.
- Concrete actor-verifier tests cover signed JWT, signed internal context, tampering,
  command mismatch, and revoke epoch.
- Latest local full-gRPC benchmark: P95 828 us at concurrency 8; benchmark sample
  226,171 ns/op, 51,884 B/op, 464 allocs/op.

Passing unit tests is not sufficient to report Requirement 2 as 100% complete.
