# BPMP Service Workflows

- Status: Current architecture workflow catalog
- Last verified: 2026-10-30
- Related: `microservices-architecture.md`, ADR-001, ADR-002, ADR-004,
  ADR-005, ADR-008, ADR-010, and ADR-011

These flows describe current repository behavior unless a step is marked
`Planned` or `External`. They intentionally show the commit boundary and the
recovery path; a happy-path-only diagram is not a distributed-system contract.

## Shared rules

- Every command preserves tenant, command/idempotency identity, actor proof,
  workload identity, correlation, and trace metadata as applicable.
- Engine authorization runs before idempotency lookup. A prior receipt must not
  leak to an actor who no longer has permission.
- RocksDB/Raft is authoritative for workflow state. Kafka is a replayable
  integration feed, not the commit authority.
- Consumers disable unsafe auto-commit, apply idempotently, and checkpoint only
  after durable local success.
- Retries are bounded, deadline-aware, and allowed only with a stable identity.

## 1. Direct workflow command

```mermaid
sequenceDiagram
    autonumber
    participant C as API client
    participant G as API Gateway
    participant E as BPMP Engine leader
    participant R as RocksDB/Raft
    participant K as Kafka
    participant P as Projection/Human/Cockpit

    C->>G: REST command + Bearer/session + Idempotency-Key
    G->>G: authenticate, tenant/CSRF/rate/DTO checks
    G->>E: mTLS gRPC + original/signed actor proof + command identity
    E->>E: verify workload and actor independently
    E->>E: authorize concrete transition
    E->>E: lookup idempotency, rehydrate, decide
    E->>R: Raft commit events + audit + receipt + outbox
    R-->>E: quorum/durability acknowledgement
    E-->>G: command receipt
    G-->>C: accepted/committed response
    E->>K: leader outbox publisher sends committed event
    K-->>E: broker acknowledgement
    E->>R: CAS publisher checkpoint
    K->>P: committed integration event
```

Failure and recovery:

| Failure point | Required outcome |
|---|---|
| Gateway rejects token, tenant, CSRF, limit, or DTO | No engine call and no state change |
| Workload valid but actor proof missing/invalid | Engine fails closed before idempotency or transition |
| Leader changes before commit | No success receipt; retry uses the same idempotency key |
| Client times out after possible commit | Return indeterminate/retryable semantics; retry the identical command identity |
| Kafka unavailable after commit | Command remains committed; outbox retains the record and retries |
| Crash after broker ACK before checkpoint | Event may be republished with the same `event_id`; consumers deduplicate |

## 2. Human task activation and completion

```mermaid
sequenceDiagram
    autonumber
    participant E as Engine
    participant K as Kafka
    participant H as Human Runtime
    participant D as Human PostgreSQL
    participant U as Task UI / API Gateway

    E->>E: commit HumanTaskActivated through Raft
    E->>K: publish committed event through outbox
    K->>H: HumanTaskActivated
    H->>D: inbox/dedup + work item + audit + checkpoint transaction
    H-->>K: commit offset after durable success
    U->>H: Complete/Delegate + expected_version + actor proof
    H->>D: validate assignment and optimistic version; persist intent
    H->>E: idempotent gRPC command preserving actor proof
    E->>E: authoritative authz + transition commit
    E-->>H: receipt, but work item is not final yet
    E->>K: committed completion/delegation event
    K->>H: authoritative event
    H->>D: finalize work item + audit + checkpoint
```

The Human Runtime never interprets WIR and never finalizes a workflow
transition from an RPC response alone. If the Engine RPC times out, the durable
intent is retried with the same identity. Duplicate or reordered Kafka delivery
cannot create a second work-item effect. Optimistic version conflicts are
returned to the caller for reconciliation rather than retried as blind writes.

## 3. Query and export

```mermaid
sequenceDiagram
    autonumber
    participant U as Client / UI
    participant G as API Gateway
    participant P as Projection Service
    participant A as Query PDP / Authz
    participant D as Projection PostgreSQL
    participant W as Export Worker

    U->>G: query + actor credential
    G->>P: typed gRPC query + signed context
    P->>A: authorize tenant, subject, fields, filters
    A-->>P: row predicate + readable fields + policy version
    P->>D: bounded keyset/set-based query
    D-->>P: rows + projection/checkpoint metadata
    P-->>G: authorized result and staleness metadata
    G-->>U: response

    U->>G: create export
    G->>P: export request
    P->>A: authorize and freeze policy inputs
    P->>D: persist bounded export job
    W->>D: claim stale-safe job
    W->>D: read authorized pages and persist result/status
```

Query authorization is fail-closed. Revocation lag never converts a PDP deny
into allow: the authoritative PDP remains the fallback safety boundary. Export
jobs persist authorized field/filter policy, not reusable actor credentials.
Claims are reclaimable after failure and output remains bounded by row and byte
limits documented in `docs/query-api.md`.

## 4. Committed event fan-out and projection recovery

```mermaid
sequenceDiagram
    autonumber
    participant E as Engine outbox
    participant K as Kafka
    participant C as Consumer
    participant D as Consumer PostgreSQL

    E->>K: event envelope(event_id, tenant, stream, sequence, schema)
    K->>C: fetch bounded batch
    C->>C: validate envelope/schema/tenant/order
    C->>D: begin local transaction
    C->>D: insert inbox/dedup marker
    C->>D: apply projection/effect
    C->>D: advance local checkpoint
    C->>D: commit
    C-->>K: synchronous offset commit
```

| Condition | Behavior |
|---|---|
| Duplicate `event_id` | Return prior/no-op effect and safely advance delivery position |
| Gap or impossible sequence | Stop/fail closed; alert and reconcile rather than fabricate state |
| Poison/unknown schema | Bounded DLQ or operator path where implemented; never silently skip |
| Crash before local commit | Kafka redelivers; no effect is visible |
| Crash after local commit before offset commit | Kafka redelivers; inbox/dedup prevents double effect |
| Projection loss | Rebuild from retained committed events and reset an explicit checkpoint |

## 5. Configuration publication and hot reload

```mermaid
sequenceDiagram
    autonumber
    participant O as Config operator/API
    participant C as Configuration Service
    participant D as Configuration PostgreSQL
    participant K as Kafka
    participant S as Runtime service replica

    O->>C: create/publish immutable profile
    C->>D: validate + version + audit + outbox transaction
    C-->>O: published version
    C->>K: ordered publication invalidation
    K->>S: publication metadata
    S->>C: mTLS ResolveConfiguration(scope/version)
    C-->>S: authoritative validated snapshot
    S->>S: install at safe point atomically
    S-->>K: commit offset after install
```

Every process replica requires a distinct stable reloader group because the
publication is a broadcast invalidation, not a work queue. Invalid metadata,
signature, resolution, or snapshot stops the reloader/process according to the
service contract; it is not skipped. The last valid snapshot remains active
until a replacement has been completely validated and installed.

## 6. Authz policy publication and query revocation

```mermaid
sequenceDiagram
    autonumber
    participant O as Policy administrator
    participant A as Authz Control Plane
    participant D as Authz PostgreSQL
    participant E as Embedded Engine evaluator
    participant K as Kafka revoke feed
    participant P as Projection Query PDP

    O->>A: policy change / revoke
    A->>D: optimistic transaction + immutable audit/outbox
    A->>A: canonicalize and sign bundle/update
    A-->>E: signed immutable tenant bundle
    E->>E: verify key/hash/signature/sequence and atomic install
    A->>K: signed monotonic actor/tenant revoke epoch
    K->>P: revoke update
    P->>P: verify signature, bundle sequence, monotonic epoch
    P->>D: durable epoch/inbox/checkpoint transaction
    P-->>K: commit offset
```

Unknown keys, tampering, rollback, cross-tenant data, non-canonical artifacts,
or epoch regression fail closed. Policy administration availability is not on
the engine command critical path: the engine evaluates a locally verified
bundle without network or database I/O.

## 7. Remote service task dispatch

```mermaid
sequenceDiagram
    autonumber
    participant E as Engine leader
    participant R as RocksDB/Raft
    participant W as Remote Worker

    W->>E: mTLS bidirectional stream + signed workload registration
    E->>E: bind certificate, tenant, worker, session, capabilities
    W->>E: credit / ready
    E->>R: claim task and durable lease through Raft
    E->>E: issue command- and lease-bound assignment token
    E->>W: assignment + token
    W->>W: execute external capability
    W->>E: ACK/result + exact assignment token
    E->>E: re-authorize completion command
    E->>R: commit workflow completion through Raft
    E->>R: complete durable lease
```

Disconnect, heartbeat timeout, or lease expiry is processed by the leader-only
reaper. It schedules bounded retry or dead-letter according to the pinned
policy. A late or substituted ACK fails because the stored token digest, lease
version, tenant, task, and session must match. Local WASM bindings remain on the
local-task path and are not offered to remote workers.

## 8. Boundary timer and message/error correlation

```mermaid
sequenceDiagram
    autonumber
    participant O as Engine committed outbox
    participant B as Boundary Runtime
    participant R as RocksDB adapter state
    participant I as Transport ingress
    participant E as Engine command path

    B->>O: read committed events after projection checkpoint
    B->>R: atomically project subscriptions + checkpoint
    B->>R: claim bounded due timers with lease
    I->>R: persist signal before source ACK
    B->>R: claim persisted correlations
    B->>B: resolve exactly one tenant/instance subscription
    B->>E: deterministic command + command-bound credentials
    E-->>B: authoritative result
    B->>R: complete/rearm or schedule retry/DLQ
```

Claims become reclaimable after lease expiry. Ambiguous correlation, scope
mismatch, invalid proof, or empty credentials fails closed. Timer credentials
map only to an explicitly configured system capability. Calendar years/months
are not accepted without a separate calendar/time-zone policy.

## 9. SLA escalation

Human Runtime periodically claims due work-item escalation records using its
service-local PostgreSQL policy and publishes the escalation event with broker
acknowledgement. A failed acknowledgement remains retryable; the work item is
not silently marked published. Multiple replicas rely on database claim/lease
isolation, and committed engine workflow state remains authoritative.

## 10. Dual-control abort, reconciliation, and key shred

```mermaid
sequenceDiagram
    autonumber
    participant U as Requester / Approver
    participant G as Governance Service
    participant E as Engine
    participant R as RocksDB/Raft
    participant K as KMS / Vault

    U->>G: create request and maker/checker approvals
    G->>G: verify distinct actors, capability, freshness, expiry, digest
    G->>E: Prepare/Commit AbortAndReconcile + proof
    E->>E: revalidate tenant, proof, ledger digest, policy, key epoch
    E->>R: atomic terminal state + reconciliation items + audit + outbox
    E-->>G: committed command identity
    G->>G: persist/claim key-lifecycle job
    G->>E: revoke data-key cache / barrier request
    E-->>G: barrier acknowledged
    G->>K: shred scoped key
    K-->>G: acknowledged
    G->>G: complete durable lifecycle job
```

KMS shredding is forbidden before the engine commit and revocation barrier.
Transient engine/KMS errors leave the durable job retryable under a bounded
policy. An expired or changed approval digest cannot be refreshed implicitly;
the actors must approve the new state.

## 11. Cockpit realtime update

Cockpit Gateway consumes committed events with its own group and publishes
bounded SSE hints to authenticated tenant-scoped connections. SSE is a
reconciliation hint, not the authoritative read model: reconnect uses a cursor
and the UI refetches Projection/Human APIs. Slow consumers are bounded or
disconnected; they must not block Kafka consumption or other tenants.

## 12. ODS case/document/integration flows

These flows are **planned**, not current deployables. ADR-011 fixes their
ownership rules. The target sequences remain in
`docs/ods-microfrontend-microservices-refactor-plan.md`; they become current
only when a real producer/consumer contract, storage migration, worker, runbook,
and executable evidence are added to the traceability matrix.
