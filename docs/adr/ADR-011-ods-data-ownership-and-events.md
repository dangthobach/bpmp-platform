# ADR-011: ODS Data Ownership and Integration Events

Status: Accepted  
Date: 2026-10-28

## Context

ODS introduces Case, Document, Integration, Master Data, Notification and Audit Search capabilities. A shared schema or generic Common Service would couple deployments and make ownership ambiguous. Distributed transactions with BPMP would conflict with the engine's authoritative RocksDB/Raft commit path.

## Decision

- Each ODS bounded context owns its PostgreSQL database/schema, credential and migrations.
- No service queries another service's database. Cross-context reads use a versioned API or a local projection built from committed events.
- Stateful changes and their outbox record commit in one local transaction.
- Consumers apply inbox/dedup, local effect and checkpoint atomically where possible.
- Events use versioned Protobuf payloads and the repository's correlation/tenant/schema envelope rules.
- Topics are provisioned with the first real producer/consumer contract, not pre-created for speculative domains.
- Kafka remains an integration feed. BPMP workflow history remains authoritative in RocksDB/Raft.
- Audit search is a rebuildable projection; immutable audit evidence stays with the authoritative owner.

Case creation and workflow start form a saga:

1. Case Service commits the Case in `WorkflowStartPending` plus outbox.
2. An idempotent worker/adapter sends the start command with actor proof and a stable key.
3. Engine commits or returns the prior receipt.
4. Case reconciles to started/failed-manual states from the durable outcome; it is not deleted to simulate rollback.

## Failure behavior

- Kafka unavailable: local committed state remains valid; outbox retries with bounded backoff.
- Duplicate event/callback: inbox/dedup returns the prior effect.
- Unknown schema/poison event: fail closed to a bounded DLQ/operations path; do not skip silently.
- Database failover/deadlock: retry only the local idempotent transaction with deadline and jitter.
- Projection loss: rebuild from retained committed events; never reverse-query the owner's database.

## Consequences

- Cross-domain views are eventually consistent and must expose staleness where decisions depend on freshness.
- Operational cost increases with each stateful bounded context, so contexts are created only with real ownership and lifecycle differences.
- ODS Common is a product/team grouping, not a shared data or business-logic boundary.
