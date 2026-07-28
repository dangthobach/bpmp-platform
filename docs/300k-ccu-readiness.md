# 300k CCU readiness review

Date: 2026-07-28

## Verdict

The repository cannot currently claim 300,000 concurrent users on one
deployment topology. The architecture can evolve toward that target through
Raft-group sharding and a dedicated realtime connection tier, but the present
logic has hard serialization and fanout bottlenecks before load balancers or
additional pods become relevant.

The existing requirement is 100k+ concurrent remote workers. No repository
benchmark or soak test validates 300k connections, 300k active requests, or a
defined active-user ratio. "300k CCU" must therefore be split into:

| Workload | Current assessment |
|---|---|
| 300k mostly idle HTTP/SSE/WebSocket connections | Not implemented or proven |
| 300k connected remote workers with bounded task traffic | Protocol direction is sound; no production-scale evidence |
| 300k simultaneous read requests | PostgreSQL, Redis and pool limits block a credible claim |
| 300k simultaneous authoritative workflow commands | Not achievable through one current Raft group |

## Confirmed bottlenecks

### 1. One in-flight authoritative proposal per engine group

`RaftWorkflowStore` holds `proposal_lock` across preparation, quorum
`client_write` and local apply in
`apps/rust/bpmp-engine-server/src/raft_runtime.rs`. Independent workflow
streams are serialized together. This prevents useful Raft pipelining and
makes the throughput ceiling approximately one quorum round trip plus one
durable apply at a time for each group.

The lock protects the prepare/check/apply contract, so it must not simply be
deleted. The replacement must be an async command path with keyed stream
coordination, concurrent proposals for independent streams, and state-machine
preconditions that reject stale same-stream proposals deterministically.

### 2. RocksDB log, state and workers share one write mutex

`crates/bpmp-adapter-rocksdb/src/rocks.rs` shares `commit_lock` with Raft log
storage and uses it for state-machine apply, outbox checkpoints, local task
checkpoints, timer/boundary projections and correlation claims. Snapshots also
hold this lock while iterating all authoritative column families.

Atomic `WriteBatch` semantics are correct and must remain. Scale should come
from bounded Raft-entry batching, shorter critical sections, snapshot APIs
that do not block normal writes, and multiple independently owned Raft groups.

### 3. Realtime fanout is O(all subscriptions)

`apps/go/cockpit-gateway/subscription/hub.go` stores every subscription in one
map. Every signal scans the entire map while holding a global read lock and
clones labels plus payload once per matching subscriber. Full client queues
drop signals without a durable resync protocol. The package has no production
composition root or 300k connection soak test.

Replace this with sharded indexes keyed by tenant and signal name, immutable
reference-counted payloads, explicit overflow/disconnect/resume semantics,
per-connection byte quotas and connection-lifecycle telemetry.

### 4. PostgreSQL pools are not capacity-configured

Human Runtime, Projection Service and Configuration Service construct pools
with `pgxpool.New` directly. Max/min connections, acquisition timeout,
lifetime, idle timeout and health period are not first-class dynamic
configuration. A 300k request wave will queue behind small defaults or overload
PostgreSQL if each process is tuned independently.

Add validated database-pool policy to Configuration Service, enforce a global
connection budget, use PgBouncer where appropriate, and benchmark query plans
with tenant skew and production cardinality.

### 5. Redis is synchronous on the public request path

API Gateway executes one atomic Lua fixed-window operation for each rate-limit
decision. This is correct across replicas but makes Redis latency and cluster
slot skew part of every public request. Hot tenants can concentrate traffic on
one key. Redis outage behavior and acceptable fail-open/fail-closed policy must
be explicit per operation.

### 6. Evidence is far below the requested scale

The documented Human Runtime gRPC benchmark uses concurrency 8. The current
functional three-node E2E verifies durability and failover, not sustained
throughput, connection churn, tenant skew, queue saturation or long-duration
resource stability. Unit, property and chaos tests do not substitute for a
capacity test.

## Strengths to preserve

- Authoritative transitions use Raft and atomic encrypted
  event/idempotency/audit/outbox batches.
- Idempotency, optimistic versions and deterministic replay are explicit.
- Kafka consumers acknowledge after durable writes and use bounded batches.
- Retry, circuit breaker, bulkhead, health and telemetry infrastructure exists.
- Query paths use projections rather than scanning event logs.
- Dynamic tenant configuration already has versioned publish/cache semantics.

## Required architecture before a 300k claim

1. Define the workload: open connections, active ratio, commands per second,
   read/write mix, payload percentiles, fanout ratio and tenant skew.
2. Introduce a shard directory mapping `(tenant_id, stream_id)` to a Raft
   group. Rebalancing must preserve one authoritative owner and idempotency.
3. Make Engine command handling async and pipeline independent-stream
   proposals. Measure prepare, quorum, fsync, apply and response separately.
4. Replace the Cockpit hub with indexed sharded fanout and durable resume.
5. Make PostgreSQL pool budgets dynamic and shared across service replicas.
6. Add admission control at every expensive boundary, expressed in queued
   bytes as well as item counts.
7. Run production-like tests on Linux with real TLS, Kafka, PostgreSQL, Redis,
   RocksDB volumes and three Engine nodes per Raft group.

## Acceptance test matrix

Use at least three separately reported profiles:

| Profile | Connections | Active ratio | Purpose |
|---|---:|---:|---|
| Connected-idle | 300,000 | 1% | file descriptors, memory, heartbeat and reconnect churn |
| Normal peak | 300,000 | 5% | mixed query, work-item and workflow traffic |
| Stress | 300,000 | 20% | admission control, bounded queues and recovery |

Each profile needs a 30-minute ramp, two-hour soak and controlled failures of a
Raft follower, leader, Kafka broker, Redis node and PostgreSQL connection.
Pass gates must include p50/p95/p99 latency, accepted/rejected throughput,
queue depth and age, reconnect success, event-loop lag, memory slope, GC,
database waits, Raft commit/apply lag and zero duplicate authoritative events.

Until those gates pass, the accurate status is: **architecture direction
supports horizontal evolution, current implementation is not 300k-CCU ready**.
