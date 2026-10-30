# BPMP Architecture Traceability Matrix

- Status: Current repository traceability index
- Last verified: 2026-10-30

This matrix connects ownership to contracts, topics, state, background work,
operations, and executable evidence. Paths are repository-relative. `Planned`
means the item is an architectural target rather than a current deployable.

## Deployable traceability

| Owner | Sync API/contract | Async input/output | Authoritative/local state | Background work | Runbook/design | Executable evidence | Status |
|---|---|---|---|---|---|---|---|
| BPMN compiler | WIR Protobuf in `contracts/proto/bpmp/wir/v1` | Signed WIR artifact | Artifact bytes/signature; no runtime state | None | `docs/bpmn-enterprise-compiler-profile.md`, `docs/requirement-1-compliance.md` | `apps/rust/bpmn-compiler/tests`, compiler crates/tests | Current |
| Engine server/core | `EngineCommandService`, `RemoteWorkerDispatchService`, `EngineGovernanceService`, `RaftPeerService` | Produces `bpmp.engine.committed-events.v1.*`; consumes configuration publications | RocksDB event/snapshot/audit/idempotency/outbox/lease state replicated through Raft | Outbox, boundary, local task, remote reaper/dispatch, config reload | `docs/rocksdb-operations.md`, `docs/boundary-runtime-adapter.md`, `docs/repository-gap-remediation.md` | `apps/rust/bpmp-engine/tests`, `crates/bpmp-adapter-rocksdb/tests`, Raft model/chaos tests | Current |
| Authz control plane | REST/gRPC surfaces under `apps/rust/authz-control-plane` | Produces query revokes and tenant lifecycle; consumes tenant configuration readiness | Dedicated Authz PostgreSQL | Revoke publisher, lifecycle publisher/consumer, inactive-user job | ADR-008, `docs/query-pdp.md`, `docs/query-revoke-kafka.md` | Authz workspace tests, query revoke E2E evidence | Current; inactive job hardening noted |
| API/Portal gateway | Public REST facade; engine/human/projection/configuration clients | Consumes configuration publications | No workflow state; Redis BFF session/rate limit | Config reload | ADR-010, `docs/api-integration.md`, `docs/ods-browser-session-contract.md` | `apps/go/api-gateway` tests including JWT/session/CSRF and negative authz | Current |
| Human Runtime | `HumanRuntimeService` | Consumes engine events/config; produces `bpmp.human.escalations.v1.*` | Human PostgreSQL work item/inbox/audit/outbox/checkpoint | Event consumer, escalation, config reload | `docs/requirement-2-compliance.md`, production deployment process | Go unit/race tests and PostgreSQL integration tests | Current |
| Configuration Service | HTTP admin API, `ConfigurationResolverService` | Produces configuration publications/readiness; consumes tenant lifecycle | Configuration PostgreSQL versions/audit/outbox/readiness | Outbox publisher, tenant lifecycle consumer, readiness publisher | ADR-007, `docs/dynamic-configuration-p1.md`, `docs/kafka-topology.md` | Configuration Go tests and E2E topology generation | Current |
| Projection Service | `ProjectionQueryService` | Consumes engine events, query revokes, configuration publications | Projection PostgreSQL read model/inbox/checkpoint/query/export/revoke/DLQ | Event/revoke consumers, export worker, config reload | `docs/query-api.md`, `docs/query-pdp.md`, `docs/query-revoke-kafka.md`, `docs/projection-rebuild.md` | Projection Go/race/PostgreSQL tests | Current |
| Governance Service | `GovernanceApprovalService`; calls `EngineGovernanceService` | Consumes configuration publications | Governance PostgreSQL approval/key-lifecycle jobs | Config reload, key lifecycle | ADR-005, `docs/vault-kms-operations.md`, production deployment process | Governance Rust tests and E2E acceptance flow | Current; production KMS gate remains |
| Cockpit Gateway | HTTPS/SSE | Consumes engine committed events | Ephemeral connection/cursor state | Event consumer, per-connection heartbeat | `apps/go/cockpit-gateway/README.md`, `docs/cockpit-web-architecture.md` | Cockpit Gateway Go tests | Current |
| Cockpit Web | REST + SSE clients | SSE hints | Browser-only UI state | None authoritative | `docs/cockpit-web-architecture.md` | Vitest/Playwright under `apps/web/cockpit-web` | Current legacy surface |
| ODS Shell / Task MFE | Portal/Task REST facade | SSE reconciliation hints | Browser-only UI state | None authoritative | ADR-009/010, ODS plan/progress/release docs | ODS unit/E2E suites | Current Phase 1/2 slice |
| Process Copilot | Not defined | Future committed-event/read-model input | Future service-local store | Future bounded analysis jobs | `microservices-architecture.md` roadmap | None | Planned |
| Case/Document/Integration | Contracts not yet present | Topics must be created with first real producer/consumer | Separate service-owned PostgreSQL | Saga/outbox/callback workers not implemented | ADR-011 and ODS refactor plan | None | Planned |

## Existing Kafka topology

Environment suffixes and exact bootstrap values come from
`platform/e2e/manifest.json`; production uses the same naming contract with a
different environment suffix.

| Topic purpose | Producer | Consumer/group owner | Ordering/durability rule | Recovery evidence |
|---|---|---|---|---|
| Engine committed events | Engine leader outbox | Human, Projection, Cockpit groups | Outbox order/checkpoint; envelope carries stream sequence and `event_id` | Duplicate/crash tests in engine and Go consumers |
| Configuration publications | Configuration outbox | Unique group per runtime process replica | Tenant partition key; publication is broadcast invalidation | `docs/kafka-topology.md`, config reload tests/E2E |
| Human escalations | Human Runtime | Downstream operational integration | Durable local outbox/lease before publish | Human PostgreSQL lease and ACK failure tests |
| Authz query revocations | Authz Control Plane | Projection revoke group | Signed, monotonic epoch and durable source feed | `docs/query-revoke-kafka.md` verification section |
| Authz revoke DLQ | Projection revoke consumer | Operator/recovery procedure | Invalid/poison record remains durable and never grants allow | Query revoke tests/runbook |
| Tenant lifecycle | Authz Control Plane | Configuration tenant-lifecycle group | Versioned tenant event and local inbox/dedup | E2E manifest and lifecycle tests |
| Tenant configuration readiness | Configuration Service | Authz readiness consumer | Tenant-scoped readiness record | E2E lifecycle acceptance |

## Contract ownership

| Contract package | Owner | Consumers | Compatibility gate |
|---|---|---|---|
| `bpmp.engine.v1` | Engine | Gateway, Human, Governance, tests/tools | Buf lint/breaking, Rust/Go generated drift, cross-language tests |
| `bpmp.engine.v1` worker | Engine | Remote workers | Same plus stream/lease/token compatibility tests |
| `bpmp.human.v1` | Human Runtime | Gateway/Task BFF | Buf and Go server/client tests |
| `bpmp.projection.v1` | Projection Service | Gateway/Cockpit clients | Buf plus query integration tests |
| `bpmp.configuration.v1` | Configuration Service with bounded-context validators | All runtime services | Buf plus snapshot validation/hot-reload tests |
| `bpmp.governance.v1` | Governance and Engine jointly at boundary | Governance, Engine | Buf plus dual-control/KMS failure tests |
| `bpmp.authorization.v1` | Authz control plane/data-plane boundary | Engine, Human/Gateway verifiers, Projection PDP | Buf, signature/canonical/revoke negative tests |
| `bpmp.raft.v1` | Engine Raft adapter | Engine peers only | Model/chaos/restart and wire compatibility tests |
| `bpmp.storage.v1` | Engine persistence adapter | Engine/RocksDB tooling only | Durable fixture/reopen/replay compatibility tests |
| `bpmp.wir.v1` | Compiler + Engine boundary | Compiler, Engine | Golden/canonical/signature/load/replay tests |

## Invariant-to-evidence traceability

| Invariant | Design decision | Implementation owner | Primary evidence |
|---|---|---|---|
| Only Engine interprets WIR and commits transitions | ADR-001 | Engine/domain core | Compiler-to-engine and deterministic replay/property tests |
| RocksDB/Raft authoritative; Kafka integration-only | ADR-002 | Engine server | Atomic batch, outbox, restart, Raft model/chaos tests |
| No cross-service SQL | ADR-004 | Every bounded context | Separate migrations/config credentials and integration tests |
| Governance cannot write Event Store or shred early | ADR-005 | Governance + Engine | Dual-control commit and key-lifecycle/barrier tests |
| Durable contracts are versioned Protobuf | ADR-003 | Contract owners | Buf lint/breaking and generated-code drift gates |
| Dynamic policy is versioned, validated, replayable | ADR-007 | Configuration + all consumers | Config publish/resolve/reload/rollback tests |
| Workload cannot substitute for actor | ADR-008 | Gateway/Human/Engine/Authz | Workload-substitution negative tests and authz atomic audit tests |
| Projection is rebuildable and never command authority | ADR-001/004 | Projection | Checkpoint crash tests and rebuild runbook |
| External retries cannot double-effect | Requirements/idempotency rules | Gateway, Engine, workers, consumers | Duplicate command/event/ACK and crash-window tests |
| Key failure is fail-closed with no plaintext fallback | Requirements/ADR-005 | Engine crypto + Governance | Revoked-key/reopen and KMS failure tests |

## Documentation and evidence gaps

| Gap | Owner | Required closure |
|---|---|---|
| Unified Portal OIDC endpoints, refresh/logout, session issuance, and signing-key operations are external | Portal/Security | Versioned producer contract, threat model, rotation/outage/logout runbooks, deployment E2E |
| Authz inactive-user job has fixed retry count/delay and no explicit distributed claim | Authz | Versioned worker policy, replica-safe lease/singleton, failure metrics/tests |
| Production KMS and full revocation-barrier qualification remain gated | Governance/SRE | Real provider adapter evidence, rotation/revocation/cache chaos, restore/runbook |
| Some planned ODS services have no contract or implementation | ODS domain owners | Add only with real bounded context, contract, store, worker catalog, runbook, and tests |
| Production-scale latency/lag/CCU evidence is incomplete | Performance/SRE | Representative load profiles, p95/p99, saturation, recovery and soak results |

## Change checklist

Before merging a cross-service change, update this matrix when it changes any
owner, contract, topic, store, job, runbook, or evidence path. Verify that:

1. The authoritative source and consistency boundary remain unambiguous.
2. Sync and async contracts are versioned and backward compatible.
3. Retries have stable idempotency, bounds, and deadlines.
4. Consumers cover duplicate, crash-before/after-effect, and checkpoint recovery.
5. Actor/workload identity and tenant scope are preserved end to end.
6. Operations can detect lag, retry exhaustion, lease loss, or divergence.
