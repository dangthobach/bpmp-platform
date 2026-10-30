# ADR-002: RocksDB and Raft are authoritative; Kafka is integration-only

- Status: Accepted
- Date: 2026-10-30

## Context

Workflow commands require deterministic replay, optimistic stream versions,
atomic event/idempotency/audit/outbox updates, and quorum durability. Kafka is
valuable for decoupled projections and integrations, but broker publication
cannot be the transaction that decides whether a workflow transition occurred.

## Decision

RocksDB state applied through the Engine Raft group is the authoritative event
log and workflow state. The leader acknowledges a command only after the
configured Raft durability/quorum rule has committed the command. Events,
idempotency results, authorization audit, stream metadata, snapshots, outbox
references, compensation/governance records, and scheduler/lease records that
must change atomically are prepared as one Raft state-machine command.

Kafka is a versioned integration feed populated only from committed outbox
records. Kafka unavailability never invalidates or rolls back an acknowledged
engine command. The leader publishes bounded ordered batches, waits for broker
acknowledgement, and advances a durable checkpoint with compare-and-set
semantics. A crash after broker acknowledgement and before checkpoint may
republish the same `event_id`; consumers must deduplicate and apply their local
effect/checkpoint transactionally.

Kafka consumer offsets, caches, and read-model checkpoints are not substitutes
for the workflow event log. Projection loss is repaired by replay/rebuild from
retained committed integration events or an explicitly governed recovery
source; it is never repaired by querying another service's database.

## Failure behavior

- Minority partitions cannot commit authoritative transitions.
- Leader change resumes from Raft/RocksDB state and durable worker checkpoints.
- Kafka outage increases outbox lag while committed workflow state remains valid.
- Duplicate delivery produces at most one local effect through inbox/dedup.
- Sequence gaps, corrupt envelopes, or unsupported schemas stop/fail closed and
  enter an explicit operator/DLQ path where defined; consumers do not skip them.

## Consequences

Engine storage and Raft upgrades require restart, replay, snapshot, model/chaos,
and durability evidence. Kafka throughput can scale independently, but lowering
Kafka acknowledgement strength or consumer correctness cannot weaken the
authoritative commit. Operations must alert on quorum health, outbox lag,
consumer lag, sequence gaps, and recovery debt.
