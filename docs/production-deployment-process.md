# BPMP Production Deployment Process

## 1. Release classification

Every release must declare one of these profiles. The profile is deployment
data, not a compile-time constant.

| Profile | Authoritative store | Permitted data | Exit gate |
|---|---|---|---|
| `functional-single-node` | One encrypted RocksDB node | Synthetic/non-PII only | Compiler, engine, Rust-Go contract and process E2E pass |
| `production-ha` | Three- or five-member Raft group, RocksDB per member | Production data | P23 and P46-P52, model checking, partition/crash/KMS chaos and restore drill pass |

The current repository implements the `functional-single-node` composition
root plus an `OpenRaft` authoritative state-machine crate and Linux RocksDB
atomic apply adapter. The three-node partition/failover test and atomic
governance race tests pass. Persistent Raft log, vote, committed index,
last-purged index and state-machine metadata now survive a real node shutdown
and RocksDB reopen test. The server composition root still writes normal
workflow commands through the single-node store, however. It must not be
labeled `production-ha`, and production PII must not be enabled, until peer
transport, membership operations, leader forwarding, `client_write` routing
and the remaining P2 gates are connected in the deployable.

## 1.1 Current consensus and governance evidence

- `openraft` is pinned exactly in the workspace; the state machine accepts only
  bounded, digest-protected atomic batches with explicit preconditions.
- Encryption is prepared once before proposal. Followers apply identical bytes
  and never call KMS, network or wall clock from state-machine `apply`.
- Linux RocksDB applies events, stream metadata, outbox, compensation ledger,
  reconciliation work items, governance audit and Raft idempotency in one sync
  `WriteBatch`.
- `AbortAndReconcile` validates configurable capability, assurance, proof TTL,
  actor separation, Ed25519 signatures and current pending-ledger digest. The
  resulting event is `WorkflowTerminatedForCompliance`; ordinary execution is
  permanently fenced after replay.
- The three-node chaos test proves an isolated minority leader cannot commit,
  the majority elects and commits, and all nodes converge after healing.
- The bounded `stateright` model checks minority-commit exclusion, committed
  prefix preservation, ordered apply and one-node crash durability.
- The Linux persistent-store test performs a real `Raft::client_write`,
  shuts the node down, reopens the same RocksDB, observes the old command as a
  duplicate and commits a new command.
- A Linux race test changes a pending ledger record after governance prepare and
  proves event, work item and governance audit all remain absent.

These are component and integration gates, not the real-process release gate in
section 3.4. No release record may claim that gate until the repository contains
and passes a broker-backed multi-process harness.

## 2. Immutable release inputs

Create one release manifest containing digests for all of the following:

- Rust and Go container images;
- signed WIR artifacts and the WIR verification key identifier;
- published configuration snapshots and policy bundle versions;
- database migration versions;
- Protobuf descriptor and generated-code digest;
- WASM modules keyed by `implementation_ref` and immutable
  `implementation_version`;
- SBOM, dependency audit result and test evidence.

Do not put private keys, database credentials, Kafka credentials or plaintext
DEKs in the release manifest. Mount those from the approved secret manager.
Runtime services have no fallback tenant, key scope, timeout, quota, retry,
routing, policy or workflow version.

## 3. Pre-deployment gates

1. Run `buf lint`, the configured `buf breaking` baseline and generated-code
   drift check.
2. Run Rust formatting, strict Clippy and workspace tests on Linux. Run Go
   formatting, `go vet`, tests and race tests for each module.
3. Run PostgreSQL migration integration tests against the target major version.
4. Run the real-process E2E with engine, Kafka, PostgreSQL, Human Runtime and
   API Gateway. Prove actor proof and idempotency key preservation.
5. Prove crash recovery at these points: before RocksDB commit, after commit
   before Kafka ACK, after Kafka ACK before publisher checkpoint, and after
   Human Runtime projection before consumer checkpoint.
6. For `production-ha`, additionally pass quorum-loss, leader-change, stale
   term, snapshot-install, membership-change and restore tests. Pass stale or
   invalid dual-control proof, KMS outage and revocation-barrier tests.

Any failed gate blocks promotion. A manual waiver cannot convert the
single-node profile into `production-ha`.

## 4. Provisioning order

1. Provision separate PostgreSQL databases and credentials for each owning Go
   or control-plane service. No service receives another service's credential.
2. Provision Kafka topics for committed engine events and Human Runtime
   escalation. Configure retention and ACLs from environment policy.
3. Provision persistent volumes for every engine member. For HA, place members
   in separate failure domains and create the headless peer-discovery service.
4. Issue separate TLS identities for API Gateway, Human Runtime and every
   engine member. Engine requires and verifies client certificates.
5. Publish signed WIR, versioned configuration snapshots, signed authorization
   bundles, JWKS and pinned WASM artifacts to the configured registries.
6. Materialize service configuration from the configuration/secret stores.
   Validate it with the service binary before opening traffic.

## 5. Deployment order

1. Apply Human Runtime PostgreSQL migrations as a dedicated migration job.
2. Start Kafka and verify topic metadata, producer idempotence and consumer
   group permissions.
3. Start the engine. For single-node, verify encrypted append/replay and local
   RocksDB recovery. For HA, bootstrap membership once, wait for quorum and
   verify the elected leader before accepting commands.
4. Verify engine mTLS, WIR signature loading, configuration/policy version
   loading, payload-key resolution, scheduler leases, local WASM registry and
   outbox publisher checkpoint.
5. Start Human Runtime. Verify PostgreSQL readiness, Kafka projection lag,
   escalation publisher ACK and mTLS connection to engine.
6. Start API Gateway last. Keep public routing disabled until upstream mTLS,
   coarse JWT verification, rate-limit configuration and a synthetic command
   pass.
7. Enable traffic gradually. Observe command p95/p99, RocksDB/Raft commit
   latency, quorum health, outbox lag, projection lag, scheduler lease
   conflicts, WASM traps and authorization denials.

## 6. Acceptance transaction

The release smoke test must execute a signed workflow containing a user task
and a pinned local service/script task:

1. API Gateway receives an original actor JWT and an `Idempotency-Key`.
2. Engine re-verifies actor/workload proofs and commits the start command.
3. Engine outbox receives the committed events atomically and publishes them
   in order only after Kafka acknowledgement.
4. Human Runtime projects exactly one work item and checkpoints only after the
   PostgreSQL transaction commits.
5. Completing the work item forwards the original actor proof and exact
   idempotency key; engine remains the final authorization and idempotency
   owner.
6. Wasmtime executes the pinned module within configured limits and completion
   re-enters the authoritative command path with a stable internal command ID.
7. Retrying every client-visible command returns the prior committed result
   without adding duplicate events or work items.

## 7. Rollback and recovery

- Stateless services may roll back to a contract-compatible image. Do not roll
  back across an incompatible Protobuf or PostgreSQL migration.
- Never replace or mutate historical WIR, policy, configuration, WASM or event
  bytes. Publish a new version and route only new instances to it.
- Kafka outage does not roll back an engine command. Keep outbox entries and
  resume from the durable publisher checkpoint.
- Restore engine data only from a tested RocksDB/Raft snapshot plus the
  retained log. Validate tenant, stream sequence, event digest and encryption
  key epoch before reopening writes.
- During a failed rollout, drain API Gateway, stop new commands, preserve
  engine volumes and Kafka offsets, then roll forward with a compatible image.

## 8. Promotion record

Record image/artifact digests, configuration and policy versions, migration
versions, Raft membership, test evidence, operator identity, deployment time,
rollback decision and unresolved risks. The record is append-only audit data;
it must not contain tokens, private keys, plaintext workflow payloads or PII.
