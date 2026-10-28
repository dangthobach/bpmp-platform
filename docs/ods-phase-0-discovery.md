# ODS Refactor Phase 0 Discovery Baseline

Status: complete for repository evidence; production workload values remain explicit external inputs.

## 1. Current context map

| Context | Current owner/source of truth | Relationship to ODS refactor |
|---|---|---|
| Workflow Execution | Rust `bpmp-engine`, RocksDB/Raft | Upstream authoritative command owner; unchanged |
| Human Work Management | Go `human-runtime`, local PostgreSQL projection | Supplies Task BFF query model and forwards actor commands to engine |
| Process Query | Go `projection-service`, local PostgreSQL | Supplies rebuildable process/case-linked queries |
| Public Edge | Go `api-gateway` | Evolves toward Portal Gateway and domain BFF modules |
| Realtime | Go `cockpit-gateway` | Supplies bounded SSE hints; never authoritative state |
| Dynamic Configuration | Go `configuration-service` | Candidate owner for versioned MFE catalog metadata, not remote code |
| Governance | Rust `governance-service` and engine atomic path | Remains separate from ODS business services |
| Presentation | React `cockpit-web` | Strangler source; Shell/remotes replace route by route |
| Case Management | Partial read model in Human Runtime | New ODS Case context is required for an authoritative business aggregate |
| Document Management | Not implemented as an ODS context | New Document Adapter context required |
| External Integration | Worker/adapter capabilities only | New Integration Hub context required |
| Master Data/Notification/Audit Search | No dedicated ODS contexts | New independently owned modules/services required |

Relationship types:

- ODS Task is a conformist consumer of versioned Human Runtime/Engine contracts; it must not copy workflow policy.
- ODS Case publishes business integration events and calls Engine through an anti-corruption adapter using business keys and actor proof.
- Document and Integration contexts publish normalized events; Case consumes those events without querying their databases.
- ODS Common provides versioned master data and notification delivery. It does not become a shared write database.

## 2. Current public API inventory

The API Gateway currently registers browser-facing operations from a single source list in `apps/go/api-gateway/internal/gateway/handler.go`:

- Runtime: `GET /v1/runtime/browser-configuration`.
- Workflow command: `POST /v1/workflows/{workflowType}/instances`.
- Human tasks: list/get/complete/delegate.
- Case/audit read facade: get case and list audit records.
- Process query: completed/general query, capabilities, aggregate and event query.
- Saved queries and asynchronous exports.
- Configuration profile lifecycle and version diff.

Gap against ODS target:

- No domain-prefixed BFF contract (`/api/tasks/v1`, `/api/cases/v1`, etc.).
- No claim/unclaim/group/unit worklist public contract.
- No authoritative ODS Case CRUD/search/360 contract.
- No document, integration, master-data or notification API.
- Current browser client hand-codes many response schemas. Target clients must be generated from OpenAPI and wrapped by `callApi`.

## 3. Internal contract inventory

Existing Protobuf service boundaries:

- `EngineCommandService.HandleCommand` is the only authoritative workflow command entry.
- `HumanRuntimeService` provides work-item queries/complete/delegate, case read and audit read.
- `ProjectionQueryService` provides process/event queries, aggregates, exports and diagnostics.
- `ConfigurationResolverService` provides immutable resolved configuration snapshots.
- Governance and Raft services remain internal security/state-machine boundaries.

Contract gap:

- ODS Case, Document, Integration, Master Data and Notification v1 packages do not exist.
- Public OpenAPI is generated/served by the gateway but needs domain ownership and generated frontend client gates.
- MFE catalog/remote compatibility schemas do not exist.

## 4. Data ownership inventory

Current migration owners:

| Owner | Migrations | State type |
|---|---:|---|
| Configuration Service | 5 | Configuration profiles, publications, readiness, audit context |
| Governance Service | 1 | Approval/governance workflow |
| Human Runtime | 2 | Work-item/case projection and delegation depth |
| Projection Service | 6 | Process/event read models and query facilities |
| Engine | RocksDB/Raft schema | Authoritative event log, snapshots, idempotency and outbox |

The E2E environment currently uses separate schemas in one PostgreSQL instance. Production policy remains database/schema-per-bounded-context with separate credentials. New ODS contexts receive distinct migration directories and credentials from their first stateful slice.

## 5. Kafka inventory

The E2E topology currently includes:

- Engine committed events.
- Configuration publications.
- Human escalations.
- Tenant lifecycle and configuration readiness.
- Authorization query revocation and DLQ.

Consumer groups exist for Human Runtime, Projection, Cockpit and per-process configuration reloaders. This confirms the existing broadcast/integration patterns that new contexts must follow.

New topics are not created speculatively. A topic is added with its first producer and consumer contract. Expected future ownership:

- `bpmp.ods-case.integration-events.v1.<environment>`.
- `bpmp.ods-document.integration-events.v1.<environment>`.
- `bpmp.ods-integration.callbacks.v1.<environment>`.
- `bpmp.ods-master-data.publications.v1.<environment>`.
- `bpmp.ods-notification.delivery.v1.<environment>`.

Names remain provisional until the first contract ADR/code change; message envelope follows the existing correlation and schema rules.

## 6. Frontend baseline decision

The repository's current Cockpit uses React 19, Vite, TypeScript 7, TanStack Query and Zod. The requested ODS target mandates React 19, TypeScript 5.8, MUI 7, Formik/Yup, SWR/Axios and Module Federation 2.

Decision:

- New ODS frontend workspace uses the requested target stack, pinned by one workspace lockfile.
- Existing Cockpit remains on its current stack during strangler migration; it is not downgraded in place.
- React/React DOM are federation singletons. Compile-time TypeScript versions are not runtime-shared dependencies.
- MUI/Emotion remain private to `@ods/shared-ui`; domain MFE imports them only through the design-system contract.
- `@ods/call-api` owns Axios configuration; MFE does not import raw Axios/fetch for platform API calls.
- SWR owns ODS server state; Formik/Yup owns ODS form state/UX validation. Backend validation remains authoritative.

This temporary dual stack is isolated by application boundary and has a retirement target: when the final Cockpit route migrates, the legacy frontend dependency graph is removed rather than merged into shared packages.

## 7. BFF and deployment decision

- `api-gateway` evolves into the Portal Gateway instead of adding another public hop immediately.
- Domain BFFs begin as independently tested modules/ports in the gateway deployable.
- A BFF becomes a separate deployable only for independent scaling, security boundary, team/release cadence or unacceptable blast radius.
- BFFs compose bounded queries only; business decisions stay in domain/application owners.
- The first slice uses existing endpoints behind the new SDK. API aliases are added only when they provide a stable domain contract, not merely a new path.

## 8. Workload and SLO baseline

No production distribution was supplied. The following values are design-test assumptions, not production capacity claims:

| Dimension | Phase 1/2 test assumption | Required external measurement |
|---|---|---|
| Concurrent browser sessions | 1,000 test sessions; architecture must remain horizontally scalable | Peak/normal CCU and session duration |
| Worklist page | 25 default, 100 maximum, keyset cursor | Rows per tenant/user/group and filter selectivity |
| API latency | Query p95 <= 2s; command acceptance p95 <= 1s excluding long external work | Endpoint-specific SLO and network budget |
| MFE load | Warm p95 <= 1s after Shell; bounded 10s loader timeout | Portal/browser geography and CDN evidence |
| Projection convergence | p95 <= 5s under nominal test load | Business tolerance per operation |
| Document | Stream up to 50 MiB; never whole-file buffer in BFF | Daily volume, concurrency, MIME distribution |
| Kafka | Per-aggregate ordering only; duplicate delivery expected | Event rate, burst, partition cardinality and retention |
| Availability | Multi-AZ stateless target; engine quorum rules unchanged | Formal RTO/RPO and maintenance objectives |

Every performance gate must record distribution, concurrency, payload, environment, p95/p99, errors and resource saturation.

## 9. First vertical slice

Phase 1/2 path selected:

```text
ODS Shell -> runtime manifest -> Task MFE -> callApi SDK
          -> existing API Gateway -> Human Runtime -> PostgreSQL projection
```

Initial capability: read-only Personal Worklist. It is selected because it proves federation, shared UI, API SDK, auth/correlation, typed errors and isolation without changing authoritative write semantics.

The next increment adds Task details, then idempotent complete/delegate. Claim/unclaim contracts are added only after their engine/Human Runtime ownership and concurrency semantics are explicit.

## 10. Phase 0 edge cases and outcomes

- Incompatible frontend stacks: isolated by application boundary; no cross-app source imports.
- BFF distributed-monolith risk: modular ports first, extraction criteria documented.
- ODS Common becoming shared database: explicitly prohibited; separate ownership/contracts.
- Missing workload values: recorded as external evidence, not silently guessed as production targets.
- Kafka outage: existing engine outbox semantics remain authoritative.
- Remote failure: Phase 1 requires loader and render boundaries before domain commands migrate.

## 11. Phase 0 exit decision

Phase 0 is complete because repository ownership, inventories, explicit assumptions, target decisions, first vertical slice and ADR backlog are recorded. Production performance sign-off remains a later phase gate and cannot be inferred from Phase 0.
