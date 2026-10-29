# ODS Refactor Progress

Last updated: 2026-10-29 (Asia/Saigon)

This ledger is the source of truth for execution status. Percentages are weighted by evidenced deliverables; phase completion is controlled by exit criteria, not the percentage.

## Program status

| Phase | Status | Progress | Current evidence | Next gate |
|---|---|---:|---|---|
| 0 - Discovery and decisions | complete | 100% | Discovery baseline plus ADR-009/010/011 | Start Phase 1 vertical foundation |
| 1 - Platform foundations | in_progress | 92% | Foundation code, browser-to-gateway trace context, CI gates, digest-only Helm chart, signed-catalog promotion verifier and rollback runbook | Resilience manifests, compatibility matrix, hosted image build and staging rollback rehearsal evidence |
| 2 - Task vertical slice | not_started | 0% | Existing gateway/Human Runtime provide the migration source | Complete task flow through new MFE/BFF contract |
| 3 - Case domain | not_started | 0% | Existing Human Runtime case projection is not the target Case aggregate | Separate Case owner/database/outbox |
| 4 - Documents and integrations | not_started | 0% | Requirements only | Streaming/quarantine and idempotent callback paths |
| 5 - ODS Common and operations | not_started | 0% | Existing observability/configuration plumbing is reusable | Versioned master data, notification and audit projection |
| 6 - Cutover and hardening | not_started | 0% | Strangler strategy documented | Canary/rollback/DR and legacy retirement evidence |

Overall evidenced progress: **26%**. This is 100% of Phase 0 plus the evidenced portion of Phase 1; later phases receive no scaffolding credit. The Phase 1 figure was recalibrated after a gate audit found that environment rehearsal, Kubernetes resilience controls and the full compatibility/failure matrix were not yet evidenced.

## Phase 1 evidence

| Deliverable | Weight | Status | Evidence |
|---|---:|---|---|
| ODS workspace and pinned requested stack | 10% | complete | Root `package.json`, lockfile and `tsconfig.ods.json` |
| Manifest-based Shell and independent remote | 20% | complete | `apps/web/ods-shell`, `apps/web/task-mfe`, federation E2E |
| MFE contract and failure isolation | 15% | complete | Catalog allowlist/parser, timeout, load/render boundaries and failure tests |
| Shared UI and dependency policy | 10% | complete | `@ods/shared-ui` plus deterministic boundary check |
| `callApi` SDK | 15% | complete | Typed success/error, IDs, timeout/cancel, same-origin guard, credentials and injectable CSRF token |
| Signed production catalog | 10% | complete | Canonical signed bytes, Ed25519 WebCrypto verification, key-id rotation map, freshness/lifetime checks and runtime config |
| Portal Gateway BFF session and async tracing | 10% | in_progress | Session-store port/Redis adapter, signed actor context, tenant/CSRF enforcement, browser W3C trace injection and gateway-to-gRPC propagation; Kafka consumer continuity and collector evidence pending |
| CI/CD, immutable publish, Helm/rollback | 10% | in_progress | Linux gates, non-root images, provenance publish, digest-only Helm chart, signed promotion verifier and rollback runbook; hosted build/rehearsal evidence pending |

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

## Active slice

Next gate: **add and verify Kubernetes autoscaling, scheduling and network-isolation controls, then complete the current/previous/canary compatibility and failure matrix**. The hosted build, staging trace and rollback rehearsal remain the Phase 1 environment exit gate; Phase 2 does not start before that evidence exists.

Owner boundaries:

- CI owns deterministic boundary/type/test/build/audit gates.
- Shell/remote pipelines publish immutable versioned artifacts; environment catalog promotion is separate.
- Kubernetes deployment owns health, resource, disruption and rollback policy.
- Domain ownership and workflow command semantics remain unchanged.

Mandatory edge cases for the slice:

- Missing CI gate or mutable artifact reference.
- Catalog promotion to an unavailable or incompatible remote.
- Failed rollout, readiness regression and rollback to the previous signed catalog.
- Trace/correlation loss across browser, Portal Gateway and gRPC boundaries.
- Telemetry initialization/export failure must not block, duplicate or retry a business request.

## Blockers and decisions needing external evidence

- Phase 1 final environment gate is pending: Docker Desktop 4.94 on this workstation crashes while initializing its secrets-engine socket, so local image builds cannot be evidenced without repairing/resetting Docker state. No Docker data was deleted or reset. The available `docker-desktop` and `minikube` contexts were not assumed to be authorized staging targets.
- Production CCU, request distribution, domain cardinality, document throughput and retention figures are not yet supplied. Phase 0 records conservative design assumptions; performance acceptance cannot be claimed until sanitized production-like data is available.
- Unified Portal OIDC endpoints, cookie/session policy and signing keys are external dependencies. Phase 1 uses explicit ports/configuration and test doubles; production activation remains gated on those inputs.
- CSP upload/callback and external-system contracts are required before Phase 4 implementation.

## Update format

Every subsequent progress update must include active phase/slice, completed evidence, gate status, edge cases verified, risks/blockers and the exact next slice.
