# Repository Gap Remediation

Verified against the source tree on 2026-07-30. This document records evidence,
not roadmap completion claims.

## Corrected Findings

| Finding | Verified status | Evidence |
|---|---|---|
| P1-P53 property catalog is incomplete | Stale finding. The release gate reports 53/53 with exactly one canonical owner per property. | `tools/check-property-catalog.ps1 -RequireComplete`, `docs/property-catalog-status.md` |
| `tests/README.md` contradicts the property catalog | Fixed. The README now delegates current ownership and evidence to the canonical status document and release gate. | `tests/README.md` |
| Authz control-plane and evaluator ownership is ambiguous | Already decided. The PostgreSQL control-plane publishes policy artifacts; `bpmp-authz-engine` is the pure authoritative evaluator embedded in the engine. | `docs/adr/ADR-008-embedded-authoritative-authorization.md` |
| Revoked runtime payloads collapse into a generic crypto outage | Fixed for authoritative event/snapshot load. `KeyRevoked` becomes `DataUnavailableForCompliance`, and the application exits before `rehydrate`, `decide`, or commit. Transient KMS/key failures remain `CryptoUnavailable`. | `crates/bpmp-adapter-rocksdb/src/rocks.rs`, `apps/rust/bpmp-engine/src/application.rs` |

The RocksDB revoked-key reopen test is Linux-only because the production
adapter is compiled only on Linux. The engine application stop-path test runs
on every supported development platform.

## Remote Worker Requirement 6

The authoritative remote-worker path is now implemented:

- The bounded tonic bidirectional stream requires mTLS and validates the leaf
  certificate SHA-256 against configured workload/tenant identities.
- Registration also requires a canonical signed workload proof bound to the
  tenant, worker, stream session, protocol version, and proof lifetime.
- Ready, leased, completed, retry, and dead-letter records plus ordered indexes
  are persisted in RocksDB. Every mutation is prepared as a compare-and-set
  batch and committed through `Raft::client_write`.
- Assignment tokens are canonical signed protobuf documents. Their digest is
  stored with the lease; late or substituted ACKs fail closed.
- Only committed, unbound service-task activations enter remote dispatch.
  Local WASM bindings keep local ownership and the local event checkpoint
  advances only after the remote enqueue has committed.
- Worker completion is re-authorized and committed through the idempotent
  `CompleteServiceTask` engine command before the durable lease is completed.
  Output variables are part of the event and deterministic replay state.
- Disconnect and heartbeat/lease expiry schedule bounded retry or dead-letter
  through Raft. Dynamic worker policy is sampled at stream, frame, command, or
  batch safe points.
- `bpmp-engine-server` exposes the dispatch service with reflection and runs
  leader-only lease reaping/dispatch loops.

Linux CI owns the production RocksDB restart and revoked-key tests. Remaining
Requirement 6 release evidence is a broker-backed multi-process worker E2E,
encrypted service-task input projection, and the P99 activation-to-send
benchmark under configured credit and payload bounds.

## Remaining Production Blockers

| Priority | Blocker | Required outcome |
|---|---|---|
| P0 | Production KMS adapter | Implement the design's key-management lifecycle behind a provider-neutral port, wrapped DEK persistence, bounded cache keyed by tenant/scope/version/epoch, revocation barrier, and a real Vault/cloud-KMS adapter. File keys remain development-only. |
| P0 | Remote worker release evidence | Add encrypted input projection, broker/process E2E with worker crash and late ACK, and the P99 ready-to-send benchmark. The authoritative transport and lease path are implemented. |
| P1 | Dead-letter audit | Persist actor, timestamp, reason, original failure reference, replay identity, and result atomically with replay state; expose tenant-scoped query. |
| P1 | Go metrics pipeline | Install an OpenTelemetry meter provider/exporter and service-level latency, error, saturation, retry, queue-lag, and pool metrics. Connect configured alert rules to measured series. |
| P1 | 300k CCU bottlenecks | Remove global proposal/commit/fanout serialization based on measured profiles, then run staged load and soak gates. Horizontal scaling does not compensate for these critical sections. |
| P2 | Product breadth | Process Copilot and progressive BPMN/time-travel UI remain later roadmap phases after production correctness. |

## Verification

Run the bounded gates before merging:

```powershell
buf lint
cargo test -p bpmp-contracts
cargo test -p bpmp-engine
cargo clippy -p bpmp-engine -p bpmp-engine-server -p bpmp-adapter-rocksdb --all-targets -- -D warnings
.\tools\check-property-catalog.ps1 -RequireComplete
```

Run the RocksDB reopen/compliance tests in the Linux CI image. Do not interpret
zero RocksDB tests on Windows as execution evidence; the crate intentionally
gates the production adapter with `cfg(target_os = "linux")`. The canonical
gate is `.github/workflows/rust-linux-authoritative.yml`.
