# ODS Refactor Progress

Last updated: 2026-10-30 (Asia/Saigon)

This ledger is the source of truth for execution status. Percentages are weighted by evidenced deliverables; phase completion is controlled by exit criteria, not the percentage.

## Program status

| Phase | Status | Progress | Current evidence | Next gate |
|---|---|---:|---|---|
| 0 - Discovery and decisions | complete | 100% | Discovery baseline plus ADR-009/010/011 | Start Phase 1 vertical foundation |
| 1 - Platform foundations | in_progress | 98% | Foundation code, browser-to-gateway trace context, CI gates, resilient digest-only Helm chart, signed-catalog promotion verifier and rollback runbook, compatibility/failure isolation matrix, Kafka consumer trace continuity and isolated metrics endpoint | Hosted image build and staging rollback rehearsal evidence |
| 2 - Task vertical slice | blocked | 65% | Task BFF plus Task MFE personal/group worklists, bounded pagination, claim/unclaim/complete/delegate, stable idempotent retry, command/projection reconciliation and browser command E2E | Phase 1 environment exit gate; then unit worklist contract, SSE hints, concurrent-claim/integration load evidence |
| 3 - Case domain | not_started | 0% | Existing Human Runtime case projection is not the target Case aggregate | Separate Case owner/database/outbox |
| 4 - Documents and integrations | not_started | 0% | Requirements only | Streaming/quarantine and idempotent callback paths |
| 5 - ODS Common and operations | not_started | 0% | Existing observability/configuration plumbing is reusable | Versioned master data, notification and audit projection |
| 6 - Cutover and hardening | not_started | 0% | Strangler strategy documented | Canary/rollback/DR and legacy retirement evidence |

Overall gate-counted progress: **29%**. This is 100% of Phase 0 plus the evidenced portion of Phase 1 (98%); Phase 2 implementation evidence is recorded below but is not counted in the overall figure until the mandatory Phase 1 environment gate passes. The remaining Phase 1 gap is strictly the hosted image build and staging rollback rehearsal (blocked on Docker Desktop environment socket issue).

## Phase 1 evidence

| Deliverable | Weight | Status | Evidence |
|---|---:|---|---|
| ODS workspace and pinned requested stack | 10% | complete | Root `package.json`, lockfile and `tsconfig.ods.json` |
| Manifest-based Shell and independent remote | 20% | complete | `apps/web/ods-shell`, `apps/web/task-mfe`, federation E2E |
| MFE contract and failure isolation | 15% | complete | Catalog allowlist/parser, timeout, load/render boundaries, failure tests; compatibility matrix (contractVersion/minShellVersion gate, previous/current/canary lanes, RemoteBoundary retry isolation) |
| Shared UI and dependency policy | 10% | complete | `@ods/shared-ui` plus deterministic boundary check |
| `callApi` SDK | 15% | complete | Typed success/error, IDs, timeout/cancel, same-origin guard, credentials and injectable CSRF token |
| Signed production catalog | 10% | complete | Canonical signed bytes, Ed25519 WebCrypto verification, key-id rotation map, freshness/lifetime checks and runtime config |
| Portal Gateway BFF session and async tracing | 10% | complete | Session-store port/Redis adapter, signed actor context, tenant/CSRF enforcement, browser W3C trace injection and gateway-to-gRPC propagation; Kafka consumer trace continuity (child span linking, missing header fallback, malformed traceparent recovery, error resilience) and isolated metrics endpoint |
| CI/CD, immutable publish, Helm/rollback | 10% | in_progress | Linux gates, non-root images, signed provenance plus SBOM request, digest-only Helm chart, HPA/PDB/topology spread/NetworkPolicy, signed promotion verifier and rollback runbook; hosted build/rehearsal evidence pending |

## Phase 2 evidence (pre-entry work; phase remains blocked)

| Deliverable | Weight | Status | Evidence |
|---|---:|---|---|
| Task MFE worklists and actions | 30% | in_progress | Personal/group worklists, current-page search/status filter, bounded keyset navigation, claim/unclaim/complete/delegate and task detail; unit worklist contract is not yet available |
| Task BFF facade | 25% | complete | Gateway list/detail/audit plus claim/unclaim/complete/delegate facade; authenticated actor proof, tenant, correlation and idempotency are forwarded to Human Runtime |
| SLA/history/idempotent UX | 15% | complete | SLA badge, typed audit failure/retry, stable command/idempotency identities retaining the same key and payload across retry, and no fabricated candidate group fallback |
| Realtime and convergence UX | 10% | in_progress | Command acceptance is separate from projection convergence; manual reconcile and last-reconciled time are explicit; SSE hint/cursor integration remains |
| Conflict/duplicate/concurrency safety | 10% | in_progress | UI retry and 409 reconciliation tests plus existing Human Runtime idempotency/domain race suite; real concurrent-claim integration evidence remains |
| Representative latency/error budget | 10% | not_started | Existing Human Runtime microbenchmarks are not accepted as end-to-end representative Task vertical evidence |

## Phase 0 evidence

| Deliverable | Weight | Status | Evidence |
|---|---:|---|---|
| Current-state/context map | 20% | complete | `docs/ods-phase-0-discovery.md` |
| API/data/event inventory | 20% | complete | `docs/ods-phase-0-discovery.md` |
| Workload and SLO assumptions | 15% | complete | Unknown values are explicitly classified; measurement plan recorded |
| Frontend/MFE decision | 15% | complete | `docs/adr/ADR-009-ods-frontend-baseline-and-federation.md` |
| Portal Gateway/BFF decision | 15% | complete | `docs/adr/ADR-010-portal-gateway-bff-boundaries.md` |
| Data/event decision and first slice | 15% | complete | ADR-011 and Phase 1 Task Worklist slice |

## Verification log

| Date | Scope | Command/evidence | Result |
|---|---|---|---|
| 2026-10-28 | Architecture document | `git diff --check` | Passed |
| 2026-10-28 | Refactor skill | Manual inspection of frontmatter, references and UI metadata | Passed |
| 2026-10-28 | Refactor skill validator | `quick_validate.py` | Not run successfully: bundled Python lacks `yaml`; no package was installed implicitly |
| 2026-10-28 | Phase 0 inventory | Routes, Protobuf services, migrations and E2E Kafka topology inspected from source | Passed |
| 2026-10-28 | ODS frontend type safety | `npm run typecheck:ods` | Passed for five workspaces |
| 2026-10-28 | ODS unit/component tests | `npm run test:ods` | 11 tests passed |
| 2026-10-28 | ODS production build | `npm run build:ods` | Shell and Task remote built; manifests generated |
| 2026-10-28 | Dependency boundaries | `npm run check:ods-boundaries` | Passed |
| 2026-10-28 | Federation browser E2E | `npm run test:ods-e2e` | 2 tests passed, including missing-manifest isolation |
| 2026-10-28 | Production dependency audit | `npm audit --omit=dev --audit-level=high` | Passed: 0 production vulnerabilities |
| 2026-10-28 | Full dependency audit | `npm audit` | Blocked: Module Federation build/DTS dependency pins vulnerable `undici`; DTS generation disabled, vendor patch/exception still required |
| 2026-10-29 | Signed catalog unit/edge tests | `npm run test:ods` | 16 tests passed; valid signature accepted; tampering, unknown key, expiry and cross-origin config rejected |
| 2026-10-29 | Signed catalog quality gates | boundary check, typecheck, production build, production dependency audit and `git diff --check` | Passed |
| 2026-10-29 | Federation browser E2E | `npm run test:ods-e2e` | 2 tests passed; independent remote loaded and missing manifest remained isolated |
| 2026-10-29 | Portal Gateway session/CSRF | `go vet ./apps/go/api-gateway/...` and `go test -race ./apps/go/api-gateway/...` | Passed; session and legacy Bearer paths race-clean |
| 2026-10-29 | Session edge cases | Gateway and Redis adapter tests | Passed: CSRF missing/wrong, duplicate/ambiguous credentials, expiry, tenant mismatch, missing/malformed/oversized/versioned record and store failure |
| 2026-10-29 | ODS CI quality commands | `npm run verify:ods`, production audit and self-starting Playwright E2E | Passed: 16 unit/component tests, 2 browser E2E and 0 production dependency vulnerabilities |
| 2026-10-29 | ODS container definitions | Linux CI BuildKit matrix | Implemented; local image build not evidenced because the workstation Docker daemon is unavailable; first hosted CI run remains required |
| 2026-10-29 | Signed release promotion | `npm run test:ods-release` | 4 tests passed; valid catalog accepted and tampered, expired and unapproved-origin variants rejected |
| 2026-10-29 | Helm release gates | `helm lint`, `helm template` and negative mutable-image check | Passed; digest-only images, signed envelope, HA replicas, probes/PDB and runtime config mounts rendered |
| 2026-10-29 | Trace propagation evidence | Gateway race suite plus existing actor/idempotency trace test | Passed for HTTP `traceparent` to downstream gRPC metadata; staging collector trace remains an environment gate |
| 2026-10-29 | Browser OpenTelemetry boundary | `npm run verify:ods` | Passed for six workspaces and 20 unit/component tests; telemetry start/end failures are isolated and do not retry business commands |
| 2026-10-29 | Browser trace E2E | `npm run test:ods-e2e` | 2 tests passed; API request carried a valid W3C `traceparent` and missing remote remained isolated |
| 2026-10-29 | Browser trace release gates | `npm run test:ods-release`, production audit, Helm lint and `git diff --check` | Passed; cross-origin telemetry endpoint rejected and 0 production dependency vulnerabilities |
| 2026-10-30 | Compatibility and failure matrix | `npm run verify:ods`, `npm run test:ods-release`, `git diff --check`, production audit | Passed: 16 Shell tests, 5 mfe-contract tests, 6 release gate tests; contractVersion/minShellVersion gates enforced; Shell config locks remoteContractVersion to compiled constant; RemoteBoundary contains render exceptions and retries without blocking siblings; CLI verifier rejects catalog when Shell is too old |
| 2026-10-30 | Kafka consumer trace & metrics endpoint | `go test -race ./...` (platform + gateway) | Passed: 4 Kafka consumer trace lifecycle tests (remote linking, missing header root span, malformed traceparent recovery, nil error resilience), 5 metrics endpoint & port isolation tests, full platform & gateway -race suites clean |
| 2026-10-30 | Kubernetes resilience controls | `helm lint` plus positive/negative `helm template` matrix | Passed; 2 HPAs, 2 ingress NetworkPolicies, zone/host spreading and explicit replicas when autoscaling is disabled; invalid HPA range rejected |
| 2026-10-30 | Phase 2 Task MFE local quality gates | `npm run verify:ods` | Passed: dependency boundaries, all workspace typechecks, 38 unit/component tests and production builds; Task MFE contributes 10 interaction/edge tests |
| 2026-10-30 | Task command browser path | `npm run test:ods-e2e` | Passed: 2 browser tests; independently served remote completed a task with expected version, command ID and idempotency key, then reconciled committed projection version |
| 2026-10-30 | Task owner/backend safety baseline | `go test -race ./apps/go/api-gateway/... ./apps/go/human-runtime/...` | Passed; Gateway/Human Runtime packages are race-clean, including existing actor proof, idempotency and projection tests |

## Active slice

Current status: **Phase 1 is 98% and Phase 2 pre-entry implementation evidence is 65%; Phase 2 remains blocked rather than advanced because its entry gate has not passed**.

Active Phase 2 slice contract:
- Owning deployables: `task-mfe` for UI composition, Portal Gateway for the Task BFF facade, Human Runtime for work-item projection/assignment, and `bpmp-engine` for final transition authorization and write-side idempotency.
- Authoritative data and consistency: engine RocksDB/Raft command commit is authoritative; Human Runtime and Task MFE are asynchronously reconciled projections. Realtime may only trigger refetch.
- Workload assumption: bounded 50-item pages; production task cardinality/query mix and p95 targets are still external inputs, so no capacity claim is made.
- Failure semantics: retryable transport/upstream failures retain the exact command key and payload; version conflict does not auto-retry; audit failure remains scoped; command acceptance is not presented as projection convergence.

Completed in the current slice:
- Personal/group worklists, current-page search/status filtering and bounded next/previous pagination.
- Claim/unclaim/complete/delegate using expected version and stable command/idempotency intent.
- SLA state, task details/history, explicit audit failure and command receipt/projection synchronization UX.
- Component coverage for happy path, retry with the same key/payload, 409 reconciliation and pagination; Playwright remote-to-command contract coverage.

Remaining gate to close Phase 1 and formally enter Phase 2:
- **Hosted build & staging rehearsal**: Workstation Docker Desktop secrets-engine socket prevents local container builds without destructive reset. The remaining verification must occur via hosted CI / staging container registry & cluster (or upon resolving workstation container runtime).

Exact next Phase 2 slice after that gate: define the unit-worklist query contract without client-side policy, wire bounded SSE hints/cursor reconciliation, then exercise two-actor concurrent claim and representative p95/projection-lag measurement against PostgreSQL + engine integration infrastructure.

Mandatory edge cases verified in Phase 1:
- Missing CI gate or mutable artifact reference (blocked by Helm template & verifier).
- Catalog promotion to an unavailable or incompatible remote (rejected by minShellVersion/contractVersion gate).
- Failed rollout, readiness regression and rollback to previous signed catalog (documented in runbook, verified by schema & CLI).
- Trace/correlation loss across browser, Portal Gateway, downstream gRPC and Kafka consumer boundaries (verified by browser E2E, gateway context propagation, and Kafka consumer in-memory tracer tests).
- Telemetry initialization/export failure must not block, duplicate or retry a business request (verified in browser & Go test suites).
- Session / CSRF attacks and expired / malformed credentials (rejected by gateway session suite).

Owner boundaries:
- CI owns deterministic boundary/type/test/build/audit gates.
- Shell/remote pipelines publish immutable versioned artifacts; environment catalog promotion is separate.
- Kubernetes deployment owns health, resource, disruption and rollback policy.
- Domain ownership and workflow command semantics remain unchanged.

## Blockers and decisions needing external evidence

- Phase 1 final environment gate is pending: Docker Desktop 4.94 on this workstation crashes while initializing its secrets-engine socket, so local image builds cannot be evidenced without repairing/resetting Docker state. No Docker data was deleted or reset. The available `docker-desktop` and `minikube` contexts were not assumed to be authorized staging targets.
- Production CCU, request distribution, domain cardinality, document throughput and retention figures are not yet supplied. Phase 0 records conservative design assumptions; performance acceptance cannot be claimed until sanitized production-like data is available.
- Unified Portal OIDC endpoints, cookie/session policy and signing keys are external dependencies. Phase 1 uses explicit ports/configuration and test doubles; production activation remains gated on those inputs.
- CSP upload/callback and external-system contracts are required before Phase 4 implementation.

## Update format

Every subsequent progress update must include active phase/slice, completed evidence, gate status, edge cases verified, risks/blockers and the exact next slice.
