# BPMP Platform

BPMP is an AI-native, event-driven BPM platform. The repository is being built
incrementally from the deterministic Rust engine outward.

## Current implementation

- Canonical generated Protobuf v1 contracts for configuration, WIR, commands, and events.
- Streaming, namespace-aware BPMN AOT compiler for the linear P0 subset.
- SHA-256 integrity digest and Ed25519-signed WIR artifacts with fail-closed loading.
- Pure Rust `decide`, `evolve`, and replay functions.
- Versioned configuration snapshots passed explicitly to every decision.
- Engine application layer with authoritative transition authorization,
  idempotency, deterministic replay, and an in-memory adapter for focused tests.
- Linux RocksDB adapter with synchronous WAL, encrypted event records, dedup,
  idempotency, and outbox entries committed in one WriteBatch.
- Three-node OpenRaft deployment path, peer RPC, snapshots, leader forwarding,
  and leader-only outbox/timer/local/remote-worker schedulers.
- Go API Gateway, Human Runtime, Configuration, Projection, and Cockpit Gateway
  services with versioned gRPC/Kafka contracts and service-owned PostgreSQL.
- PostgreSQL-backed Authz Control Plane, embedded fail-closed Engine evaluator,
  actor/workload identity separation, and signed query-revocation feed.
- Governance approval and key-lifecycle service with Engine revocation barrier
  and Vault Transit adapter.
- Configured interval snapshots encrypted and committed atomically with events;
  replay loads only the bounded event tail after the latest snapshot.
- AES-256-GCM payload crypto adapter backed by an external data-key resolver port.
- Wasmtime 36 LTS local worker with ABI v1 linear-memory transfer, fuel metering,
  strict resource quotas, and isolated typed failures.

The in-memory adapter does not provide production durability. Production uses
the Linux RocksDB/Raft path. Real-PII deployment remains gated on production KMS
qualification, recovery/chaos evidence, and the production-readiness items
tracked in [`docs/repository-gap-remediation.md`](docs/repository-gap-remediation.md).

The compiler implements the documented executable BPMN/DMN/CMMN profile and
fails closed for unclassified or unsupported model elements. Passing that
profile does not claim full standards breadth; current coverage and residual
gaps are recorded in
[`docs/requirement-1-compliance.md`](docs/requirement-1-compliance.md).

## Architecture and documentation

Start with the [documentation index](docs/README.md) and the
[current architecture map](docs/architecture/README.md). The architecture
section links deployable ownership, service workflows, identity/trust flows,
background workers, accepted ADRs, and the contract/topic/store/test
traceability matrix.

## Compile BPMN

All operational and resource values are explicit; the CLI has no hidden limits
or signing key.

```powershell
cargo run -p bpmn-compiler -- `
  --input examples/order.bpmn `
  --output artifacts/order.wir `
  --workflow-version 1 `
  --signing-key secrets/wir-ed25519.key `
  --max-input-bytes 1048576 `
  --max-xml-depth 64
```

The signing key file must contain exactly 32 raw bytes. Production keys must be
provided by an approved secret-management workflow; no key is stored in this repository.

## Verify

Install Buf once with `winget install --id bufbuild.buf --exact`, then open a
new shell so the updated `PATH` is visible.

```powershell
cargo fmt --check
cargo clippy --workspace --all-targets --all-features -- -D warnings
cargo test --workspace --all-targets
buf lint
buf breaking --against contracts/baseline/v1.binpb
```

Run fast local Windows gates with `./tools/check.ps1`. Run the cached Wasmtime
gate with `./tools/check-wasmtime.ps1`, or include it in the full workspace gate
with `./tools/check.ps1 -IncludeWasmtime`. Run native Linux RocksDB integration
tests with `./tools/check-rocksdb-linux.ps1`; its Docker image and named Cargo
volumes avoid repeating the full cold build each run.

## Run locally

Start persistent PostgreSQL, Kafka, Redis and OpenTelemetry infrastructure,
then apply checksum-tracked DDL:

```powershell
.\platform\local\manage.ps1 up
```

See [`platform/local/README.md`](platform/local/README.md) for configurable
ports, migration commands, logs, backup and guarded reset. Run the complete
three-node Engine, service and Cockpit topology with:

```powershell
.\platform\e2e\run.ps1
```
