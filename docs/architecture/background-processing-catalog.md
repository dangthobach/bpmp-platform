# BPMP Background Processing Catalog

- Status: Current implementation inventory
- Last verified: 2026-10-30
- Scope: explicit long-running consumers, publishers, schedulers, lease workers,
  and configuration reloaders started by repository deployables

This catalog distinguishes durable work from in-memory periodic activity. A
timer or goroutine is not a correctness mechanism by itself; durable state,
idempotency, leases, and checkpoints define recovery.

## Cross-cutting worker contract

Every production worker must have:

- an owning deployable and authoritative state store;
- bounded batch, queue, payload, concurrency, retry, and deadline policy;
- a stable idempotency/dedup identity;
- explicit lease/fencing or single-leader ownership when work is exclusive;
- crash recovery for before-effect and after-effect/before-checkpoint windows;
- cancellation and graceful-drain behavior;
- lag/depth/error/retry/lease metrics and an operator action;
- configuration from a validated versioned snapshot unless it is an immutable
  protocol constant or bootstrap setting.

## Engine server

All four authoritative engine polling loops run only on the current Raft leader.
Followers sleep and re-check leadership. Their durable mutations flow through
RocksDB/Raft or a compare-and-set checkpoint as required.

| Worker | Trigger/input | Durable state and identity | Bounds/retry | Recovery and failure |
|---|---|---|---|---|
| Engine outbox publisher | Poll committed outbox after publisher checkpoint | RocksDB outbox sequence, `event_id`, CAS publisher checkpoint | Configured poll, batch, retry/backoff; Kafka idempotent producer/ACK | Kafka outage does not roll back command; crash after ACK republishes same `event_id` |
| Boundary projection/timer/correlation worker | Engine committed outbox, due-time index, persisted signals | Independent projection checkpoint, subscription records, signal/timer lease version | Configured scan/batch, lease, retry, attempt/DLQ and correlation limits | Expired claim is reclaimable; success completes/rearms atomically; ambiguity fails closed |
| Local task worker | Committed local-WASM service-task activations | Local task checkpoint plus authoritative task/command identity | Configured batch and retry policy; Wasmtime resource bounds | Checkpoint advances only after durable outcome; retry remains idempotent |
| Remote task reaper/dispatcher | Ready tasks, worker credit, heartbeat/lease expiry | Raft-persisted task status, lease version/expiry, assignment-token digest | Configured poll, credit, lease, attempts, payload/frame bounds | Leader change reloads durable state; stale/late ACK rejected; exhaustion dead-letters |
| Configuration reloader | Configuration publication topic | Per-process Kafka group plus in-memory verified registry/safe-point generation | Bounded fetch/resolve/install and bootstrap deadlines | Each replica consumes every publication; invalid snapshot stops rather than skips |

Implementation entry points: `apps/rust/bpmp-engine-server/src/linux_runtime.rs`.
The boundary algorithm and timer profile are specified in
`docs/boundary-runtime-adapter.md`.

## Human Runtime

| Worker | Trigger/input | Durable state and identity | Bounds/retry | Recovery and failure |
|---|---|---|---|---|
| Committed-event consumer | Engine committed-event topic | Human PostgreSQL inbox/work-item/audit/checkpoint and Kafka group | Bounded fetch/message size and database transaction | Offset commits only after durable projection; duplicate delivery is a no-op/prior effect |
| SLA escalation worker | Periodic poll of due work items | PostgreSQL due/lease/outbox state and work-item/version identity | Dynamic escalation poll, lease, batch, retry policy | Multiple replicas isolate claims; broker ACK failure leaves publication retryable |
| Configuration reloader | Configuration publication topic + mTLS resolver | Per-process group and validated runtime policy provider | Bounded resolver/retry/deadline | Invalid/unavailable publication stops reload; last valid snapshot is not partially replaced |

The gRPC and health servers are supervised alongside these loops. Shutdown
cancels the shared context, gracefully stops gRPC, and bounds HTTP shutdown.

## Projection Service

| Worker | Trigger/input | Durable state and identity | Bounds/retry | Recovery and failure |
|---|---|---|---|---|
| Committed-event consumer | Engine committed-event topic | Projection PostgreSQL inbox/read model/checkpoint | Bounded Kafka/database policies | Duplicate/crash windows resolve through inbox and transactional checkpoint; rebuild is supported |
| Query revoke consumer | Signed authz revoke topic | Durable revoke epoch, inbox/checkpoint, DLQ and Kafka group | Configured attempts/backoff/commit timeout | Signature/epoch/schema failures never allow; retryable transport retains batch; poison goes to durable DLQ path |
| Query export worker | One-second claim tick over accepted export jobs | PostgreSQL job status/claim and immutable authorized query snapshot | Stale-claim recovery, max 100,000 rows, 16 MiB, pages up to 500 | Crash leaves claim reclaimable; cancellation/status is durable; credentials are not persisted |
| Configuration reloader | Configuration publication topic | Per-process group and policy provider | Bounded resolve/install | Same fail-closed behavior as other runtime reloaders |

Export behavior and operator prerequisites are in `docs/query-api.md`; revoke
operations are in `docs/query-revoke-kafka.md`; rebuild is in
`docs/projection-rebuild.md`.

## Configuration Service

| Worker | Trigger/input | Durable state and identity | Bounds/retry | Recovery and failure |
|---|---|---|---|---|
| Configuration outbox publisher | Poll locally committed publications | PostgreSQL globally ordered outbox/lease/event sequence | Configured poll, lease, batch, producer ACK/retry | Claim is reclaimable; committed profile remains valid during Kafka outage |
| Tenant lifecycle consumer | Tenant lifecycle topic | Configuration PostgreSQL tenant state/inbox/readiness | Bounded Kafka and local transaction | Deduplicate before effect; no cross-service database access |
| Tenant readiness publisher | Poll readiness outbox/state | PostgreSQL readiness event identity/checkpoint | Uses bounded outbox poll and broker ACK | Retry publication without mutating committed readiness history |

Configuration publication is a broadcast invalidation. Runtime consumers use
distinct stable groups per process replica and resolve authoritative values over
mTLS before installing them.

## API/Portal Gateway

| Worker | Trigger/input | Durable state and identity | Bounds/retry | Recovery and failure |
|---|---|---|---|---|
| Configuration reloader | Configuration publication topic | Per-process group and validated in-memory snapshot | Bounded Kafka/resolver/deadline | Process fails closed on invalid replacement; requests never use a partial snapshot |

Redis browser-session and rate-limit operations are request-driven, not
schedulers. They are bounded and fail closed according to the BFF contract.

## Governance Service

| Worker | Trigger/input | Durable state and identity | Bounds/retry | Recovery and failure |
|---|---|---|---|---|
| Configuration reloader | Configuration publication topic | Per-process group and verified policy cache | Bounded resolution/install | Key lifecycle does not start from unvalidated policy |
| Key lifecycle worker | Poll committed key-shred jobs | Governance PostgreSQL job, worker lease, request/command identity | Configured poll, batch, lease; policy KMS retry/backoff | Reclaims expired jobs; retries barrier/KMS; completes only after engine barrier and KMS ACK |

The lifecycle order is immutable: authoritative engine governance commit,
durable job, engine key-cache revocation barrier, KMS shred, durable completion.
An error leaves the job retryable; no plaintext fallback is permitted.

## Authz Control Plane

| Worker | Trigger/input | Durable state and identity | Bounds/retry | Recovery and failure |
|---|---|---|---|---|
| Query revoke publisher | Poll immutable revoke feed | Authz PostgreSQL feed/checkpoint and signed event identity | Configured batch, poll, retry/backoff and broker ACK | DB commit survives Kafka outage; publisher resumes from durable checkpoint |
| Tenant lifecycle publisher | Poll tenant lifecycle outbox | Authz PostgreSQL lifecycle event/outbox | Configured topic/policy | Downstream Configuration deduplicates; publication does not grant transition authority |
| Tenant readiness consumer | Read configuration readiness topic | Authz PostgreSQL readiness/inbox and Kafka group | Bounded consumer policy | Readiness is durable and tenant-scoped; invalid input stops/fails closed |
| Inactive-user deactivation job | Periodic database scan | Authz PostgreSQL user status; batched update predicate | Configured interval/threshold/batch; current code uses three retries with a five-second delay | Each run drains matching rows in batches; after repeated error it aborts that run and retries next interval |

The inactive-user retry count/delay is currently an implementation hardening
gap because those values are fixed in the job. Before production qualification,
move them into the versioned worker policy and add lease/single-run semantics if
multiple Authz replicas may run the job concurrently.

## Cockpit Gateway

| Worker/activity | Trigger/input | State and identity | Bounds/retry | Recovery and failure |
|---|---|---|---|---|
| Committed-event consumer | Engine committed-event topic | Kafka group and bounded in-memory event stream/fan-out | Fetch/message/queue bounds | Restart resumes from group offset; clients reconcile from read APIs |
| SSE heartbeat | Per-connection ticker | Ephemeral authenticated connection/cursor | Configured heartbeat and write deadlines | Slow/disconnected client is removed; heartbeat is not a durability mechanism |

SSE messages are hints. A reconnect or missed message is repaired by refetching
Projection/Human APIs, not by treating the gateway as an authoritative model.

## Planned workers (not current)

The ODS plan describes Case workflow-start saga, Document upload/scan callbacks,
Integration Hub inbox/outbox, notification, and future Process Copilot workers.
They are not part of the current runtime catalog. A planned worker becomes
current only when its deployable, versioned contract, authoritative store,
lease/checkpoint design, configuration, telemetry, runbook, and executable
failure evidence exist.

## Operational signals

At minimum, alert on sustained user-visible or safety-margin failures:

| Signal | Examples of actionable condition |
|---|---|
| Outbox lag/depth | Oldest unpublished record exceeds SLO; repeated broker errors |
| Consumer/projection lag | Checkpoint age or sequence gap exceeds staleness budget |
| Lease health | Reclaim rate, stale lease age, repeated ownership conflict |
| Retry/DLQ | Retry exhaustion, DLQ growth, poison schema/signature errors |
| Scheduler lateness | Due timer/SLA/key job remains unclaimed beyond policy |
| KMS/governance | Barrier failure, KMS failure, reconciliation debt, legal deadline risk |
| Configuration | Resolve/install failure or replicas on divergent versions |
| Remote workers | No credit, heartbeat loss, lease expiry, late ACK rate |
| SSE | Queue saturation, slow-consumer disconnects, reconnect storm |

Metrics must avoid actor/resource high-cardinality labels and sensitive payloads.
Every alert needs a linked recovery or escalation procedure before production.
