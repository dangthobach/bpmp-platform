# ADR-012: Identity, Authorization, Task History and Batch Contracts

- Status: Proposed for service-owner review
- Date: 2026-11-21
- Owners: Authz/Identity, Human Runtime, Engine, Governance, Platform Security

This proposal records the product decisions already supplied and recommends a configurable implementation. It does not approve runtime endpoints, quotas, retention periods, or domain policies. Existing accepted ADRs remain authoritative where referenced.

## Decisions supplied by product/domain owner

- Keycloak is the identity provider; interactive authentication uses SSO with OIDC Authorization Code + PKCE.
- BPMP manages user membership, roles and permissions per tenant after authentication.
- Identity mutations require optimistic concurrency, idempotency and audit.
- Task history reads authoritative event data from an Event Store.
- Task bulk supports `CLAIM`, `UNCLAIM`, `COMPLETE`, and `DELEGATE`; every item carries an expected version.
- Both all-or-nothing and partial-success batch modes are required.

## 1. Target service boundaries

Keep and extend the existing `authz-control-plane`; do not create a second RBAC/authorization authority.

| Owner | Owns | Does not own |
|---|---|---|
| Keycloak | Credentials, MFA, authentication, IdP session and OIDC tokens | BPMP tenant roles, workflow transitions, task assignment state |
| Authz Control Plane | Federated subject links, tenant membership, org/branch tree, permission catalog, tenant roles/grants, policy versions, signed bundle publication/revocation, policy-admin audit | Credential lifecycle, workflow/task state, final Engine transitions |
| BPMP Engine | Workflow state, authoritative transition authorization, event append, transition idempotency and transition audit in Raft | Tenant role administration or mutable organization tree |
| Human Runtime | Work-item assignment/claim/delegation, per-item versions, local command idempotency, task-owned audit/history facts and batch operation state | Workflow state or the final authorization/commit decision for `COMPLETE` |
| Governance | Compliance retention profiles, legal hold, dual control and erasure workflow where applicable | Rewriting/deleting source events outside an approved governance protocol |
| Configuration Service | Versioned non-security runtime limits and feature/rollout settings validated by owner schemas | Permission semantics, roles, grants, or authorization policy |

The organization tree can remain an Authz-owned module/store, as it is in the existing design. If it later needs an independent lifecycle or scaling boundary, split its deployable without changing its public owner contract. Do not duplicate the organization hierarchy in Human Runtime or Engine databases.

### Authentication and token flow

Recommended browser flow is Authorization Code + PKCE using `S256`, with the Portal BFF as the OAuth client and session boundary:

1. BFF creates `state`, `nonce`, and a PKCE verifier, keeps them server-side, then redirects to Keycloak.
2. Keycloak authenticates the user and returns an authorization code to the exact registered BFF callback.
3. BFF validates state/nonce/issuer/audience, redeems the code using the verifier, and creates a server-side application session.
4. Browser receives only a `Secure`, `HttpOnly`, `SameSite` session cookie; unsafe cookie-authenticated requests require CSRF protection.
5. BFF derives `(issuer, subject)` from the verified identity, resolves the selected tenant membership through Authz, and issues a short-lived actor proof bound to tenant, audience, workload and command.

Do not put access/refresh tokens, tenant roles, or actor IDs in browser storage as authorization authority. If a future remote must authenticate directly without the BFF, register it as a public Keycloak client with exact redirect/origin allowlists, Authorization Code + PKCE `S256`, short-lived tokens held in memory, and backend verification. The direct-SPA path is a separate deployment profile, not a silent fallback from BFF failure.

The stable federated principal key is `(issuer, subject)`. Email, username and display name are mutable attributes, not identity keys. Keycloak claims provide authentication/profile evidence; Authz membership and policy data determine BPMP authorization. Claim allowlists and the response to disabled/deleted IdP accounts require owner review.

## 2. Flexible tenant, organization, branch and role model

### Data model

```text
FederatedPrincipal(issuer, subject, status, profile_snapshot, profile_version)
TenantMembership(tenant_id, principal_ref, status, version)
OrganizationNode(tenant_id, node_id, parent_id, node_type, status, tree_version)
TenantRole(tenant_id, role_id, role_key, role_version, status)
RoleGrant(tenant_id, role_id, permission_id, resource_scope, conditions, version)
MembershipRole(tenant_id, principal_ref, role_id, scope_ref, scope_mode, version)
```

Use tenant-scoped immutable IDs; enforce uniqueness on `(issuer, subject)` and `(tenant_id, role_key)`. Keep profile claims separate from BPMP-managed membership and authorization data. A membership may have multiple scoped role bindings; one user may belong to multiple tenants with different roles.

### Role and permission semantics

- The platform/owner manifest defines the typed permission vocabulary: `(resource type, action, allowed attributes, constraints)`. Tenants may create roles and compose permissions from that catalog, but may not invent executable actions, SQL, CEL/JavaScript policy, or fields unsupported by the resource owner.
- Use RBAC to grant candidate actions and ABAC to constrain each candidate by tenant, organization, branch, queue, resource state, classification, owner and trusted request context.
- Platform operator roles and tenant roles are distinct namespaces. Platform support access is just-in-time, reason-bound, time-limited and audited; it is never inherited into a tenant role.
- Default deny. A tenant may narrow permissions; it cannot disable tenant isolation, owner enforcement, mandatory audit, Engine transition checks, or signed-bundle verification.
- No implicit role inheritance. Role inheritance, if enabled later, must be explicit, acyclic, versioned and bounded in depth.
- Permission grants are independent from workflow transition rules. Engine remains the final transition authority and rechecks the actor before idempotency result disclosure or state mutation.

### Organization and branch scope

Use one tenant-owned, versioned tree with arbitrary depth and stable node IDs. A branch binding has an explicit scope mode: `EXACT_NODE` or `NODE_AND_DESCENDANTS`. Descendant inheritance is visible in the admin UI and requires explicit selection; it is not inferred from a role name. No upward or sibling inheritance. Tenant-wide scope is a separate high-privilege grant.

Each resource owner stamps the authoritative branch ID into its own resource/event data. The browser never supplies authoritative organization ancestry. Authz resolves current ancestry/tree version for a decision. Reparenting increments the tree version and invalidates/re-evaluates branch-dependent policy; stale or unavailable ancestry fails closed. Keep the source resource's branch and tree version with audit evidence so an investigation can explain both the current decision and the tree used at write time.

### Authorization deployment model

Authz is a centralized **policy control plane**, not a synchronous remote PDP hop for every read or command:

```text
Keycloak OIDC/PKCE -> Portal BFF -> owner PEP
                                  |-> Authz membership/org lookup (tenant switch/admin path)
                                  |-> locally verified, signed policy bundle
                                  |-> actor proof -> Engine final transition check

Authz Control Plane -> signed versioned bundles/revocations -> Engine, Projection, Human Runtime PEPs
```

Keep the current signed immutable bundle, monotonic version/epoch, revocation and local evaluator design. Authz management/catalog APIs can be online for administration and membership changes; hot-path owner services use verified local bundles and resource-local attributes. On bundle expiry, signature failure, missing required attributes, stale organization proof or revocation lag beyond the configured security bound, protected actions fail closed. Never allow a tenant profile to turn fail-closed into fail-open.

### Event visibility

Treat history visibility as a separate permission from task read/action: e.g. `task.history.read`, `task.history.read_actor`, `task.history.read_sensitive_fields`, and `audit.export`. Authz provides row/resource scope; the event owner applies a typed field allowlist and redaction policy before returning the page. Visibility may depend on event category, tenant, resource scope, actor relationship, classification and purpose. Unauthorized records are filtered without leaking their existence/count; unauthorized fields are omitted or masked according to a versioned policy. Missing policy or classification metadata fails closed. Do not expose raw event payloads by default.

## 3. Task history contract

“Event Store is authoritative” must respect bounded-context ownership:

- Engine Event Store (RocksDB/Raft) is authoritative for committed workflow transitions and human-task lifecycle events emitted by the Engine.
- Human Runtime's append-only, transactionally written event/audit records are authoritative for Human Runtime-owned assignment operations such as claim, unclaim, delegate and SLA changes. Each record is committed in the same local transaction as the work-item state/version and outbox/inbox evidence.
- Kafka is transport/replay feed, never the source of truth. A task timeline/search projection may be rebuilt from owner events but is not the evidence authority.

The Task History API may serve a unified, rebuildable timeline projection, but every event must retain `source_owner`, `source_event_id`, `source_sequence`, tenant/resource IDs, `occurred_at`, `recorded_at`, actor reference (subject to visibility policy), action, outcome, reason code, from/to version, command ID and correlation/causation IDs. The API exposes per-source watermark/freshness. Do not claim a globally ordered, fully fresh history while an owner feed is behind. If a requested source is unavailable or has a sequence gap, return a typed partial/unavailable state; never return an empty list as if no history exists.

Pagination proposal:

- Opaque signed cursor, bound to tenant, resource, filter, policy version and source offsets; never client-provided numeric offsets.
- Stable keyset paging; preserve per-source sequence and use deterministic `(occurred_at, source_owner, source_event_id)` presentation order for merged display. Causality references remain authoritative where timestamps differ.
- Default page 50, hard cap 100 as initial configurable profile; cursor expiry is configurable and expired cursors return a typed restart-required response.
- Reauthorize every page and every export. A cursor is not a capability and cannot bypass a membership/revocation change.

Retention must be split by purpose and owner: authoritative Engine event retention, Human Runtime task evidence, searchable history projection, and security/admin audit. Governance owns approved retention classes and legal hold. The query projection can expire earlier and rebuild while source evidence remains retained. Do not pick one global TTL or physically delete authoritative evidence from a UI/API. Exact durations, erasure/crypto-shred rules and legal hold precedence require Compliance/Legal and domain owners.

## 4. Batch actions contract

### Request and capability negotiation

Proposed request shape:

```json
{
  "mode": "PARTIAL",
  "items": [
    { "id": "work-item-1", "expectedVersion": 12, "action": "DELEGATE", "values": { "targetPrincipal": "subject-ref" } },
    { "id": "work-item-2", "expectedVersion": 4, "action": "COMPLETE", "values": { "formVersionId": "v3", "outcome": "APPROVE", "data": {} } }
  ]
}
```

Require `Idempotency-Key`, `X-Command-ID`, verified actor proof, and trusted tenant context. Reject duplicate item IDs in one batch. Validate an action-discriminated schema: `DELEGATE` requires an eligible target and reason; `COMPLETE` requires the published form version/output contract and outcome; `CLAIM`/`UNCLAIM` accept no arbitrary values. Return a stable operation ID and item-level status, receipt, error code, retryability, and resulting version.

Add a capability read (proposal: `GET /v1/work-items/batch-capabilities`) that returns the actions, modes, limits and payload schema versions enabled for this tenant/owner deployment. UI uses this response to gray/omit unsupported combinations; server validation remains authoritative. Limits may vary by tenant tier or environment but must never exceed platform hard ceilings.

### Atomicity boundary

Expose `ATOMIC` and `PARTIAL` as explicit, mutually exclusive modes. Do not implement “atomic” as a compensating saga and describe it as all-or-nothing.

- `ATOMIC`: preflight all items for tenant visibility, action permission, expected version, domain validity and supported storage boundary before effects. Commit all or none in one real owner transaction. If any item fails, return no successful item and a deterministic failure list. If the action mix/partition cannot fit one atomic boundary, reject before effects with `ATOMIC_SCOPE_UNSUPPORTED`.
- `PARTIAL`: each item has an independent durable result; successful items commit once, failures are classified, and retries may select only retryable failed/unknown items. Return `PARTIALLY_SUCCEEDED` where appropriate.
- Claim/unclaim/delegate may be candidates for Human Runtime local transaction atomicity when items share the same database transaction/partition and invariants permit it.
- Complete is an Engine transition. It is atomic only if Engine implements one authoritative Raft command that validates and commits all included transitions inside one consistency group. A batch mixing Human Runtime writes and Engine transitions cannot be truly atomic under current no-distributed-transaction architecture. In that case, expose only `PARTIAL` or add a separately named, explicitly compensating workflow; do not mislabel it `ATOMIC`.

Capability negotiation should therefore be per action, mode and deployment, not a global boolean. Example: `CLAIM: [ATOMIC, PARTIAL]`, `DELEGATE: [ATOMIC, PARTIAL]`, `COMPLETE: [PARTIAL]` until the Engine batch command is proven, and mixed action sets: `[PARTIAL]`.

### Sync versus async

Use an adaptive, bounded contract while keeping one operation resource:

- `execution=auto` (default): run inline only when the estimated work is below configured item/cost/deadline thresholds and within one owner boundary; otherwise persist an operation and return `202 Accepted` with `operationId` and `statusUrl`.
- `execution=async`: always queue durably and return `202`.
- No promise of `execution=sync` for a batch that crosses service/consistency boundaries. If a sync preference cannot be honored, return the operation handle rather than blocking until timeout.
- `GET /v1/work-items/batch-operations/{operationId}` returns operation status and paginated per-item outcomes. SSE/WebSocket is only a refresh hint; polling/cursor state remains authoritative.

This avoids long-held HTTP requests for large/slow batches while preserving fast feedback for small local batches. The response distinguishes `ACCEPTED`, `RUNNING`, `SUCCEEDED`, `PARTIALLY_SUCCEEDED`, `FAILED`, and `RECONCILIATION_REQUIRED`; `PARTIALLY_SUCCEEDED` is invalid for `ATOMIC`.

### Resource limits and configurable starting profile

Do not treat `maxItems=100` as the only resource guard. Enforce, advertise and meter:

1. max items/request and canonical JSON bytes;
2. per-action cost units and external fan-out;
3. inline work budget/deadline;
4. worker concurrency per batch, tenant and deployment;
5. outstanding/queued operations per tenant and global queue capacity;
6. per-item, whole-operation and dependency deadlines;
7. result size/page size and operation/history retention.

Suggested **stage tuning profile**, not production limits: max 100 items, max 256 KiB request body, inline at most 20 items and 2 seconds total, async above either threshold; per-batch worker concurrency 8 and at most 2 active jobs per tenant. Set deployment-wide worker/DB limits from measured capacity. These starting values must be overridden by load-test results; configuration cannot exceed platform safety ceilings. Enforce both item count and weighted work units so an action with expensive downstream fan-out cannot evade quotas using a small batch.

Settings live in a versioned, schema-validated `TaskBatchPolicy` profile. Platform owns hard ceilings; tenant policy can lower limits and choose enabled modes/actions within capabilities, but cannot raise ceilings, relax authz, disable audit, or change retry safety. Publish/rollback records actor, reason, policy version and audit. Configuration values are never read from client input as authority.

### Idempotency, retry and audit

- Idempotency namespace: `(tenant_id, verified_actor_subject, operation, Idempotency-Key)`, fingerprint over canonical request including mode and every item/version. Same key/same fingerprint returns the same operation/result; same key/different fingerprint is `409`; in-progress replay returns the existing operation handle.
- Persist batch journal and item journal before/with enqueue; persist each item outcome transactionally with its owner effect. Derive stable child command keys for each Engine transition from batch identity plus item identity. A worker crash after commit but before acknowledgement must replay the receipt, not repeat a transition.
- One retry owner per layer. Retry only transient dependency/rate-limit failures, with finite attempts, exponential backoff, full jitter, `Retry-After` and whole-operation deadline. Never blindly retry authz denial, validation/business errors or expected-version conflicts. Timeout after sending a side effect is unknown: reconcile by idempotency/command ID first. Exhaustion moves to `RECONCILIATION_REQUIRED` or a governed dead-letter path.
- Starting stage values: 4 attempts including the initial call; 200 ms base, 5 s cap, full jitter; 30 s inline deadline. Async job deadline/attempt budget remains a separately configured profile. Tune after fault/load tests; do not multiply retries in browser, BFF, Human Runtime and Engine.
- Audit every accepted/rejected batch and every item/attempt with verified actor `(issuer, subject)`, executor/workload, tenant, action/resource, expected/result version, authz bundle and org-tree version, policy decision reference, operation/batch/item/command IDs, correlation/trace IDs, attempt, timestamp, outcome/reason and source owner. Never audit tokens, secrets, raw form values or unbounded payloads. Store a digest and allowlisted business references when needed.
- For Human Runtime changes, commit work-item state/version, item idempotency and immutable audit/outbox evidence in one PostgreSQL transaction. For Engine transitions, preserve ADR-002/ADR-008 atomic Raft commit. Cross-owner effects use outbox/inbox and reconciliation, never a distributed transaction.

Audit retention is not the same setting as batch operation retention. Governance/Compliance defines audit evidence retention and legal hold; Task policy may set shorter operation-result retention only if idempotency/replay guarantees and support obligations remain covered. Retain dedupe records at least as long as the advertised retry/replay window.

## 5. Configuration and rollout

Configuration is layered and explainable:

```text
platform hard safety ceiling
  > deployment profile (capacity/security)
    > tenant policy (may tighten; may select approved capabilities)
      > request preference (may request async; cannot relax a limit)
```

Every effective value reports its source, version and last update. Invalid or absent security policy denies; invalid optional performance override falls back only to a stricter platform-safe default and emits an operator alert. Tenant administrators cannot edit signing keys, global caps, retention classes, permission manifests or retry safety.

Roll out in slices:

1. Keycloak PKCE/BFF session and `(issuer, subject)` identity-link contract; no UI credential management.
2. Authz tenant membership, role/permission catalog and organization/branch versioning; signed bundles and PEP coverage per owner.
3. Event-source inventory for Engine and Human Runtime, history cursor, visibility rules and retention profile approvals.
4. Task partial batch for `CLAIM`, `UNCLAIM`, `DELEGATE`, then `COMPLETE` with Engine idempotency; history and per-item receipts.
5. Add atomic capability only for proven same-boundary operation groups; Engine multi-transition atomic command is a separate gate.
6. Benchmark quotas/retry/retention under production-like tenant mix; enable tenant-by-tenant behind versioned policy/capability flags, monitor and roll back.

## 6. Acceptance criteria

### Authentication and authorization

- PKCE `S256` verifier/state/nonce mismatch, issuer/audience mismatch, expired token, disabled principal and invalid callback are rejected; no credential/token is persisted in browser storage.
- User with valid SSO but no tenant membership cannot access tenant resources; forged tenant headers, cross-tenant IDs and stale membership/revoke epochs fail closed.
- `(issuer, subject)` remains stable when email/username changes; duplicate subject links are prevented.
- Tenant role cannot grant an action absent from the owner permission manifest; unsupported fields/actions and role cycles are rejected.
- Branch exact scope excludes child; explicit subtree includes descendants; excludes parent/sibling; reparent/disable invalidates or recomputes decisions by new tree version.
- Event history checks resource and field visibility on every page/export; hidden records/counts and sensitive fields do not leak.

### History

- Engine transition and Human Runtime assignment events retain source event ID/version and correlation; duplicate event replay does not duplicate timeline entries.
- Page cursor yields no duplicates or gaps under concurrent event arrival; response reports owner watermarks and projection lag.
- Event store outage, sequence gap, expired cursor, redaction-policy outage and legal-hold state return distinct typed outcomes.
- Projection can be rebuilt from retained owner evidence; expiry of a projection does not delete authoritative event/audit evidence.

### Batch

- Same idempotency key and body under concurrent requests creates one operation/effect; changed body with same key conflicts.
- Per-item authorization and expectedVersion are checked; one unauthorized/foreign-tenant item cannot mutate or disclose protected resources.
- `ATOMIC` injects a failure at every preflight/commit point and proves zero items commit on any failure; unsupported mixed owner/partition mode rejects before effects.
- `PARTIAL` returns durable per-item outcome; crash after commit before ACK and selective retry do not duplicate completed items.
- One tenant at limits receives bounded backpressure/429/Retry-After without exhausting other tenants; concurrency, payload, queue, deadlines and cost limits are measured.
- Retry exhaustion produces a discoverable reconciliation/dead-letter state; audit joins actor, batch, item, command, trace, policy/tree version and final result.

## 7. Inputs still needed before runtime contracts are approved

- Keycloak realm/client, issuer/subject mapping, claim allowlist, account-disable/session revocation SLA and BFF deployment mode.
- Whether organization node types need more than org/branch, who may reparent, and which roles can assign subtree/tenant-wide scope.
- Customer/staff/partner personas, event categories/fields visible to each, and field-masking rules.
- Compliance-approved authoritative event/audit retention classes, legal hold and erasure/crypto-shred rules.
- Production-like batch mix, payload distribution, target inline latency, queue backlog, concurrency, RTO and support/replay window.
- Which same-owner action combinations must be truly atomic; acceptance of `COMPLETE` as partial-only until Engine batch commit exists.

## References

- [ADR-002: RocksDB/Raft authority and Kafka integration feed](../adr/ADR-002-authoritative-event-log-and-kafka.md)
- [ADR-004: database per bounded context](../adr/ADR-004-database-per-bounded-context.md)
- [ADR-008: embedded authoritative transition authorization](../adr/ADR-008-embedded-authoritative-authorization.md)
- [ADR-011: ODS data ownership and integration events](../adr/ADR-011-ods-data-ownership-and-events.md)
- [Authorization and data-access design](../refactor-prototype/authorization-and-data-access.md)
- [Keycloak JavaScript adapter and PKCE options](https://www.keycloak.org/securing-apps/javascript-adapter)
