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

- Keycloak realm/client/claim mapping and approval of the proposed session/revocation targets below.
- Whether node types need more than organization and branch; the permissions for reparent and tenant-wide scope.
- Customer/staff/partner event visibility rules and field-masking approval.
- Compliance-approved retention periods, legal hold and erasure/crypto-shred rules; the proposal deliberately has no universal production TTL.
- Production-like batch mix and load results to adjust the stage tuning profile below.
- Whether to fund an Engine multi-transition Raft command for atomic `COMPLETE`; until then the contract rejects unsupported atomic combinations.

## 8. Detailed owner recommendations

The values in this section are actionable defaults for implementation and stage testing. They remain proposals until the named owner approves them. Tenant configuration may tighten a value or select a supported mode; it cannot relax platform security/capacity ceilings.

### 8.1 Keycloak claim, session and revocation

**Recommend:** confidential Portal BFF client, OIDC Authorization Code + mandatory PKCE `S256`, exact redirect URI allowlist, server-side `state`/`nonce`/verifier, secure session cookie, CSRF token on unsafe cookie requests. Keep refresh/access tokens server-side, encrypted at rest if persisted, and never expose the Keycloak Admin API credential to a browser or tenant admin.

Proposed initial session profile for stage:

| Setting | Starting value | Enforcement |
|---|---:|---|
| Access token lifespan | 5 minutes | Gateway verifies issuer, audience, signature, `exp`, `nbf`; never treat token roles as BPMP permission authority. |
| BFF session idle timeout | 30 minutes | Rotate session ID after login/step-up; destroy server session at idle expiry. |
| BFF absolute session lifetime | 8 hours | Require a new Authorization Code flow after expiry; step-up `max_age` for privileged operations. |
| Disable/revoke propagation SLO | 60 seconds end-to-end | Measure from Keycloak account/session event to BFF, Authz, Engine and owner PEP rejection. Alert and fail closed if revocation state is stale beyond the bound. |

These are security-profile proposals, not user-configurable tenant values. Keycloak logout alone is insufficient evidence that every issued access token is unusable. Enable OIDC Back-Channel Logout to the BFF; verify logout tokens and deduplicate by issuer/session/event ID. On account disable or administrator revocation, publish a durable IdP lifecycle event to Authz. Authz marks the federated principal inactive, increments a monotonic principal/tenant revoke epoch, invalidates affected BFF sessions, and publishes the signed revoke update. Engine and query/task PEPs reject a proof whose epoch is below their local floor. Short access-token lifetime bounds residual risk while back-channel delivery/reconciliation runs.

If Keycloak does not reliably publish account-disable events, add an Admin Events consumer or bounded reconciliation poller using a service credential with only required read/revoke scopes. Its cursor and last-success time are durable and monitored. Do not make per-request introspection the default authorization path. A missing/late identity status source denies high-risk mutations and raises a stale-revocation alert; it must not silently extend membership.

Claim allowlist proposal: `sub`, `iss`, `aud`, `exp`, `iat`, `auth_time`, `sid`, `acr`/`amr` when supplied, and a small profile allowlist such as `email`, `email_verified`, `name`, `preferred_username`. `(iss, sub)` is immutable identity; profile claims are versioned snapshots. Tenant membership and roles are loaded from Authz and can be revoked independently of Keycloak profile claims.

Migration proposal for the existing Authz directory: expand `user_account` with `idp_issuer` and `idp_subject`, backfill only from verified existing `external_id` mappings, add a unique `(idp_issuer, idp_subject)` constraint, dual-read during one migration window, then stop using username as a join key. Preserve current IDs and tenant role links; do not rewrite historical actor IDs. Accounts that cannot be linked unambiguously remain disabled for SSO until an administrator resolves them.

### 8.2 Organization/branch and role administration

Use a single Authz-owned organization tree per tenant, with multiple roots allowed only if the tenant configuration enables that model. `node_type` is extensible from a platform enum such as `ORGANIZATION`, `BRANCH`, `DEPARTMENT`; adding a type requires a versioned schema and owner policy for whether it participates in branch scope. IDs are immutable; a node cannot change tenant.

Admin mutations:

- `Create/Update/Deactivate/ReparentOrganizationNode` carry `expectedVersion`, `Idempotency-Key`, reason and correlation ID. Reparent is a privileged action requiring explicit confirmation/dual approval when it changes the effective scope of existing grants.
- Reparent validation checks same tenant, no cycle, active parent, permitted node types and bounded subtree size before commit. It commits a new `treeVersion` and one durable `OrganizationTreeChanged` outbox event in the same Authz DB transaction.
- Affected grants are not rewritten. Scope is evaluated against the current tree; the event invalidates branch-expansion caches and triggers audit/reconciliation. Until an owner has loaded the new tree version or safely resolved it online, the affected branch decision fails closed.
- Do not permit hard delete of a node referenced by resources/grants. Deactivate or reparent with a reason and migration preview. Tenant-wide scope is a separately named privileged grant, never the empty-branch interpretation.

Role semantics:

- Platform roles: immutable, platform-owned capability definitions; no tenant may edit or delete them.
- Tenant custom roles: tenant-owned name/description and bindings to the platform permission catalog; key immutable after creation, mutable fields use `expectedVersion`.
- Permission catalog entries are registered by owner manifest with resource type, action, readable/writable fields, supported scope kinds and policy schema version. Tenant admins can compose these entries into role grants and supported typed conditions; they cannot author executable code or grant an unregistered action.
- A role may be assigned at tenant, exact organization/branch or explicit subtree scope. The UI/API shows the effective descendant set and tree version before publish.
- Deleting/deactivating an assigned role is rejected with a stable `ROLE_IN_USE` conflict unless the request atomically replaces/removes all assignments within the same Authz database transaction. No cascade across owners.
- Permission additions, direct grants, tenant-admin changes, role deletion and scope broadening require mandatory audit. Tenant-specified expiry is allowed only within platform min/max bounds.

### 8.3 Event visibility matrix and retention

Separate user-facing task timeline from compliance/security audit search. Proposed policy capabilities:

| Capability | Default subject scope | Fields visible |
|---|---|---|
| `task.history.read` | Current assignee/candidate group for that task; scoped supervisor with explicit branch binding | Event type, time, outcome, safe summary and task version |
| `task.history.read_actor` | Same row scope plus explicit grant | Actor display reference; do not return email/IdP claims by default |
| `task.history.read_payload` | Explicit high-risk grant and purpose/reason | Only owner-allowlisted, classified fields after masking |
| `audit.search` / `audit.export` | Separate auditor/security role and scoped tenant/branch | Audit metadata; export requires separate action, reason, limit and audit |

System events with no end-user audience remain invisible in the task timeline. Missing classification defaults to the most restrictive class. Denied rows are filtered without counts; denied fields are omitted, not replaced with untrusted client text. Each page/export reauthorizes against current membership, policy and tree version.

Retention contract uses mandatory `retentionClass` on every event, not arbitrary per-user TTL. Governance owns a versioned class registry with `sourceOwner`, event category, minimum/maximum retention, archive tier, disposition method, legal-hold behavior and deletion evidence. Event append fails closed if a required class is unknown. Legal hold blocks disposition and is queryable by authorized Compliance roles. A disposition creates a separate immutable certificate with event range/digest, policy version, approver and time; it never edits prior event bytes.

Recommended initial lifecycle (durations intentionally left as Compliance input):

1. Engine/Human Runtime source evidence: retention class plus append-only source, encrypted and backed up under owner policy.
2. Search/timeline projection: rebuildable, may use a shorter configurable hot window; older rows page from archive/source or return a typed `ARCHIVED` result. Projection expiration never implies source deletion.
3. Security/admin audit: separate retention class, mandatory audit cannot be disabled by tenant config, legal hold takes precedence.
4. Idempotency/batch receipts: retain at least the advertised replay/retry period; delete only after the operation is terminal and replay guarantee expires.

Do not set “7 years” or another universal production duration in code. Before enabling purge/crypto-shred, Compliance must approve the retention-class durations and data-subject erasure rules for each deployment/jurisdiction. Stage can use short disposable values isolated from production data.

### 8.4 Atomicity and execution matrix

Publish `GET /v1/work-items/batch-capabilities` with deployment and tenant-specific capabilities, e.g.:

```json
{
  "schemaVersion": "1",
  "actions": {
    "CLAIM": { "modes": ["ATOMIC", "PARTIAL"], "owner": "human-runtime" },
    "UNCLAIM": { "modes": ["ATOMIC", "PARTIAL"], "owner": "human-runtime" },
    "DELEGATE": { "modes": ["ATOMIC", "PARTIAL"], "owner": "human-runtime" },
    "COMPLETE": { "modes": ["PARTIAL"], "owner": "bpmp-engine" }
  },
  "mixedActionModes": ["PARTIAL"],
  "limitsProfile": "task-batch-standard-v1"
}
```

Atomic eligibility proposal:

| Set of items | `ATOMIC` | `PARTIAL` | Reason |
|---|---|---|---|
| Claim/unclaim/delegate, same Human Runtime tenant DB and transaction domain | Enable after transaction and lock-order proof | Enable | Single local PostgreSQL transaction can commit item updates, item dedupe, audit and outbox together. |
| Complete items | Disable initially | Enable | Engine owns final transition; HR projection is not authoritative. Enable atomic only after an Engine Raft batch command validates and appends every transition in one consistency group. |
| Mixed action types/owners | Disable | Enable | Human Runtime DB and Engine Raft have no distributed transaction; compensation is not atomicity. |
| Cross-tenant or cross-partition | Reject | Reject or split into separate tenant operations | One batch must have one trusted tenant context and bounded consistency domain. |

For every atomic request, canonicalize and preflight the complete list (duplicate IDs, visibility, per-item authorization, expected version, current state, action schema, atomic eligibility) before writes. Recheck versions under the committing transaction/Engine command. On any failure, no item changes; return deterministic per-item blockers and `FAILED`. Do not hold DB locks while making network calls. For Engine atomic complete, the command must be one Raft proposal whose deterministic state-machine validation produces either all events/idempotency/audit/outbox records or none. Until that exists and passes crash/leader-change tests, do not advertise atomic complete.

For Human Runtime atomic actions, lock rows in sorted work-item ID order, use one bounded SQL transaction, and append all audit/outbox/item-dedupe rows in that same transaction. Enforce one tenant and one transaction domain per batch. Set a database statement timeout/deadlock retry budget; a deadlock retry repeats the exact idempotent transaction, not an individual item after partial commit.

### 8.5 Quota and retry profile

Start stage with this owner-configurable profile; measure p95/p99 latency, queue age, DB pool use, Engine commit duration, per-action fan-out and noisy-tenant impact before production:

| Knob | Stage starting value | Enforcement/owner |
|---|---:|---|
| Items per batch | 100 | Human Runtime schema; reject before enqueue |
| Canonical request body | 256 KiB | Gateway + Human Runtime |
| Inline threshold | <=20 items AND estimated <=2 s | Human Runtime; otherwise async operation |
| Worker concurrency per operation | 8 | Human Runtime worker policy |
| Active operations per tenant | 2 | Fair queue/bulkhead; tenant override can only lower |
| Default/max history page | 50/100 | Human Runtime query policy; opaque keyset cursor |
| Inline retry attempts | 4 total including first | Single retry owner; 4xx business/authorization/version errors are not retried |
| Retry delay | 200 ms base, 5 s cap, full jitter | Transient 429/503/dependency failure only; respect `Retry-After` |
| Inline total deadline | 30 s hard ceiling; target completion <=2 s | If threshold exceeded, stop blocking and return/retain operation status; never duplicate work |
| Idempotency replay retention | 7 days starting proposal | Must exceed max client retry/reconciliation window; batch and item dedupe retained together |
| Batch result retention | 30 days starting proposal | Operational receipt/query policy, independent of evidence retention |

These numbers are stage starting points only. Production config comes from versioned `TaskBatchPolicy` with platform hard ceilings and tenant-level lowering. The profile additionally includes max weighted work units, payload size, max queue age, global worker/DB budget, per-action concurrency, downstream fan-out, operation deadline and result retention. Define action weights from measured service cost; use both item-count and cost-unit admission. Return `429` with `Retry-After` for quota/backpressure; never discard an accepted operation.

Error/retry contract:

- `400/422`: schema or business invalid; no automatic retry.
- `401/403/404`: authentication/authorization/visibility; no retry until caller context changes.
- `409`: stale expected version or idempotency fingerprint mismatch; reread/reconcile, do not replay blindly.
- `429`: honor bounded `Retry-After` and remaining deadline.
- `502/503/504` or connection timeout after command submission: preserve same key, query operation/receipt; retry only if owner says safe and idempotency record is durable.
- Exhausted attempts: mark item `RECONCILIATION_REQUIRED` or `DEAD_LETTERED`, preserve actor and keys, alert operator; manual replay requires separate capability/reason and same operation identity.

One layer owns retries at a time. Browser does not retry batch commands automatically. BFF forwards the same command/idempotency IDs. Human Runtime retries local DB transactions only on classified transient SQL errors and deadline; Engine owns transition retry/idempotency. This avoids multiplied retries.

### 8.6 Sync/async and batch payload

`execution=AUTO` is default: small work inside the configured cost/deadline and one owner boundary may complete inline with `200`; otherwise persist an operation and return `202` plus `operationId`, `statusUrl`, and `Retry-After`. `execution=ASYNC` always queues. There is no forced synchronous mode for cross-owner/multi-aggregate work. `GET statusUrl` returns progress and per-item receipts; SSE may signal a refresh but is not authority. Replaying the same POST always returns the same operation handle/result.

Action payload schema versions are discriminated and owner-published:

- `CLAIM`: only ID/version; server derives actor as assignee.
- `UNCLAIM`: ID/version and optional reason; only current assignee or scoped manager, as policy permits.
- `DELEGATE`: ID/version, target principal or assignment group reference, reason; server validates target membership, queue eligibility, delegation depth and no self-delegation if policy requires.
- `COMPLETE`: ID/version, published form version, outcome key, schema-validated values and optional reason; Engine revalidates actor and transition; never trust client to assert completion.

Reject duplicate IDs, unknown fields and mixed-tenant resources. Large output values should be referenced by bounded artifact IDs and validated through the owning form schema rather than embedded without a hard byte cap.

### 8.7 Audit fields and acceptance gates

Audit one batch envelope and one outcome per item/attempt. Store verified `issuer/subject`, executor/workload, tenant, operation/batch/item/command IDs, action/resource, expected/result version, Authz bundle sequence, organization tree version, decision reference, outcome/reason, attempt, timestamp, correlation/trace IDs and source owner. Store payload digest and allowlisted field references, not raw values, tokens or credentials. Human Runtime writes work-item state/version, idempotency journal, audit event and outbox in one SQL transaction; Engine writes transition event, dedupe, audit reference and outbox in its Raft atomic command.

Production enablement is blocked until stage demonstrates: Keycloak disable/logout propagation SLO; revoked membership denial in Engine and query PEP; branch reparent invalidation; per-event field redaction; history cursor stability under concurrent events and replay; atomic rollback under injected failure; partial batch worker crash/retry without duplicates; tenant fairness under load; quota responses; and audit reconstruction from operation ID to final business event.

## References

- [ADR-002: RocksDB/Raft authority and Kafka integration feed](../adr/ADR-002-authoritative-event-log-and-kafka.md)
- [ADR-004: database per bounded context](../adr/ADR-004-database-per-bounded-context.md)
- [ADR-008: embedded authoritative transition authorization](../adr/ADR-008-embedded-authoritative-authorization.md)
- [ADR-011: ODS data ownership and integration events](../adr/ADR-011-ods-data-ownership-and-events.md)
- [Authorization and data-access design](../refactor-prototype/authorization-and-data-access.md)
- [Keycloak JavaScript adapter and PKCE options](https://www.keycloak.org/securing-apps/javascript-adapter)
