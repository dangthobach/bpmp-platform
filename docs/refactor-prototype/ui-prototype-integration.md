# UI prototype integration

**Target:** `apps/web/cockpit-web` is the authenticated, API-backed product UI. `bpmp-prototype` is the visual and interaction reference; its mock data and simulated mutations are not production behavior.

## Prototype module mapping

| Prototype module | Product route | Live behavior / API source | Coverage boundary |
|---|---|---|---|
| Client Portal | `/portal` | Live work items assigned to the authenticated actor from `GET /v1/work-items?scope=mine`; links to task list and generic workflow start with initial variables | Portal summary shows only the current API page; no domain-specific application intake API exists. |
| Modeler | `/modeler` | Process catalog and BPMN linear plus constrained paired-gateway canvas/source editing; DMN catalog and supported rule-table/source editing; Forms catalog with JSON Schema/UI schema draft, validate, publish and history; bounded BPMN/DMN XML import/export; BPMN/DMN version history | Authoring stores immutable snapshots with digest, source revision, tag, idempotency and audit. Saving uses CAS; publishing/check-in is not deployment. Nested/complex BPMN, multi-input/output DMN, arbitrary form widgets/layouts, process activation and deployment remain gaps; no prototype sample definitions are loaded. |
| Operation | `/operation` | Process query workbench, per-instance authorized event history, actor-owned saved-query lifecycle, bounded query CSV export, and process/case APIs | Saved queries are scoped to the authenticated actor; workflow migration, arbitrary state move, and process cancellation remain unavailable unless a command/API contract is added. |
| Optimize | `/optimize` | Live Projection aggregates grouped by a selectable workflow type/version, status, active node or policy/config version; capability-gated 7/30/90-day started-at filter; average cycle time for completed instances when readable; actor-owned dashboard configuration CRUD; authorized async CSV export | SLA breakdowns and heatmap metrics still lack matching owner APIs; dashboard data always reruns through current policy and only allowed scopes/fields/operators are queried. |
| Identity | `/identity` | Live organization list, Authz role catalog, and paginated Authz-backed tenant user directory | Read-only directory; provisioning, credentials, MFA and account lifecycle remain owned by the identity provider. |
| Access | `/access` | Dynamic Authz catalog, policy draft, validation, publish, acknowledgements, audit, organization branch and assignment-group sources | Catalog capability still depends on owner manifests and enforcement support. |
| Task Management | `/task-management` | Live assignment groups, paginated assignment policies and current-page work items; create/update via Human Runtime assignment-policy APIs | Requires `human.assignment-policy.read` / `human.assignment-policy.manage`; policy version is checked on update. |
| Tasklist | `/tasklist` (also `/`) | Server-filtered All visible / My tasks / Group queue tabs; per-item detail; live assignment groups; claim/unclaim/delegate/complete; published form lookup by task `form_key` | Human Runtime applies assignment scope from verified actor and group membership; group queue shows only unclaimed candidate-group tasks. Supported JSON Schema fields and the versioned bounded UI profile render dynamically; completion includes the published version ID, checked again by Gateway. |
| Foundation | `/foundation` | Configuration Service profile/version/draft/publish/rollback/restore/diff APIs | Only registered configuration schemas/owners are editable. Prototype endpoint registry, template generator, and allocation mock are not treated as persisted configuration. |
| Audit | `/audit` | Live audit API | Results reflect available audit owner/projection coverage. |

## Interaction rules

- Navigation and actions either call the corresponding API-backed page/action or open an explicit capability state with a route to the nearest supported workflow.
- Never use prototype arrays as a success result, and never show a success toast before the owning API accepts a command.
- Read failures, command failures, projection delays, loading and empty results are distinct UI states. Refresh and retry repeat the actual request.
- Keep authentication in the Cockpit session, use the verified tenant context and preserve command idempotency/version requirements from the API client.
- Do not enable a mock control for editing/deploying processes, managing users, or analytics reports until its owning API contract and enforcement behavior exist. Claim/unclaim and assignment-policy CRUD use registered Gateway APIs and the authenticated actor.

## Implemented in this pass

- Cockpit navigation now follows the prototype module structure and visual direction while retaining the authenticated production data flows.
- Client Portal summary reads actual Human Runtime work items and provides live navigation to task list/start workflow.
- Modeler uses live Authoring APIs for listing, creating, retrieving, saving and validating BPMN and DMN drafts when the Gateway is configured with the Authoring upstream. BPMN supports linear edits and a paired, one-level gateway split/join view; adding/removing branches is limited to parallel gateways, while exclusive conditions display read-only. The DMN mode provides a structured single-input/single-output table editor plus XML source mode. BPMN/DMN XML import reads bounded local files into the unsaved draft; export downloads the current editor source. Save persists changes through revision-checked APIs; BPMN and DMN check-in store immutable source snapshots only after validation and revision digest checks. Version-history tables are newest-first and metadata-paginated; source XML loads only after the user selects a version, and download is local from that owner-returned snapshot. Two selected versions can be compared with a bounded source line diff using the two owner read APIs; large sources fall back to side-by-side read-only panes. “Load as draft” copies the source only to the editor; Save creates a new draft revision, preserving the source snapshot. Nested/complex BPMN and multi-input/output DMN stay source-only; deployment remains a gap. No mock definitions are shown.
- Existing API-backed routes are reachable from the prototype-style shell: task list, start, process explorer/operation, live Optimize aggregates, organization/role directory, assignment-group/task overview, Authz access, configuration/foundation, cases, audit, and BPMN/DMN/form authoring. Tasklist commands use expected versions and idempotency; form tasks resolve the published form and submit validated, versioned outputs. Task Management creates/updates owner-backed Human Runtime assignment policies with capability checks, optimistic concurrency and audit. User lifecycle administration, process activation/deployment, arbitrary form layouts remain gaps.
- Tasklist loads the authoritative work-item detail before resolving its `form_key`; it renders supported string, integer, boolean and primitive-array schema fields. Browser validation is for feedback only: Gateway re-reads the current published schema and rejects a stale `form_version_id` or invalid outputs before Human Runtime/Engine persist completion evidence. Form tasks are completed individually.
- Task forms now consume the published bounded UI schema profile: field ordering, textarea for non-enum strings, full/half width, placeholder and help text. Authoring rejects unsupported UI keys, widgets, invalid field references and type mismatches at validation/publication; the runtime renderer fails closed on incompatible stored UI metadata. The server-side JSON Schema remains authoritative for output validation.
- Form Modeler includes a visual profile editor for field ordering, widget, width, placeholder and help text, plus an advanced JSON view. Both update the same local UI schema draft and are saved/validated/published through the existing revision-checked Authoring flow; the visual editor does not send independent mutations.
- Optimize creates, polls, cancels, and downloads owner-backed Projection CSV exports. Export scope and selected fields are restricted by live query capabilities; export values are decoded from the protobuf JSON bytes representation before download.
- Optimize adds a 7/30/90-day started-at window to the existing aggregate query when Projection capabilities explicitly allow filtering `PROCESS_FIELD_STARTED_AT` with `BETWEEN`; otherwise it sends no time predicate and displays that the range is unavailable. The query is re-evaluated by Projection under current tenant row/field policy.
- Optimize adds per-workflow average cycle time for completed instances using Projection `AVG(duration_ms)` only when query capabilities mark `PROCESS_FIELD_DURATION_MS` selectable; the metric and column are omitted when the field is not readable.
- Optimize saves and paginates actor-owned dashboard configurations through Projection; update uses optimistic version and delete requires the displayed version. The saved document is bounded to 64 KiB and 1–24 unique-ID widgets. It contains configuration, never result rows; the current UI supports the throughput widget, selectable categorical group fields and time window, then reruns owner-authorized aggregates on open. Drill-through is shown only for workflow-type grouping.
- Optimize aggregate rows drill through to Process Explorer with workflow type and active/completed scope prefilled; the user executes the ordinary authorized query, so the API re-applies current row/field policy.
- Process Explorer saves the executed query snapshot, lists the actor's saved queries, runs a stored snapshot, and deletes with the expected version through Projection Service.
- Identity displays the live tenant user directory through Gateway → Authz; rows and roles come from Authz, while user provisioning and lifecycle remain with the identity provider.

## Verified Task Management UI/API mapping

| UI interaction | API route | Owner / enforcement |
|---|---|---|
| Load assignment policy table | `GET /v1/assignment-policies?page_size=&page_token=` | Human Runtime; `human.assignment-policy.read`; bounded keyset pagination |
| Edit an existing policy | Local selection of policy + its server version | UI sends `expected_version`; it does not trust a browser-provided new version |
| Create or update a policy | `PUT /v1/assignment-policies` | Human Runtime; `human.assignment-policy.manage`; XOR assignee/group validation; idempotency and audit |
| Refresh policy/group views after save | React Query invalidation then live reads | Re-reads owner API; no local policy is treated as persisted |
| Configure candidate-group permissions | Authz `/access` module | Authz catalog/policy version lifecycle remains separate from Human Runtime assignment policy ownership |

## Verified Optimize UI/API mapping

| UI interaction | API route | Owner / enforcement |
|---|---|---|
| Choose export scope and selectable fields | `GET /v1/process-instances/query-capabilities` | Projection query capabilities constrain scope and selectable field list |
| Start bounded process export | `POST /v1/query-exports` | Projection; authenticated actor proof; max rows bounded to 10,000 in this UI |
| Poll current export | `GET /v1/query-exports/{exportID}` | Projection binds export access to the creating actor and tenant |
| Cancel a pending export | `DELETE /v1/query-exports/{exportID}` | Projection owner check and cancellation state transition |
| Download completed CSV | Current export response | The browser downloads only CSV returned by the owner API; no mock report data is generated |

## Verified Process Explorer saved-query mapping

| UI interaction | API route | Owner / enforcement |
|---|---|---|
| List saved query pages | `GET /v1/saved-queries?page_size=&page_token=` | Projection binds rows to authenticated tenant and actor |
| Save the executed query snapshot | `POST /v1/saved-queries` | Projection validates filter/sort/scope under current query policy and records an owner-bound snapshot |
| Run a saved snapshot | Existing `POST /v1/process-instances/query` | Re-evaluated under current actor and current authorization policy; prior saved access does not bypass current policy |
| Delete saved query | `DELETE /v1/saved-queries/{savedQueryID}?expected_version=` | Projection actor ownership check plus optimistic version |

## Verified Identity directory mapping

| UI interaction | API route | Owner / enforcement |
|---|---|---|
| Load a page of tenant users | `GET /v1/access/users?limit=&after_user_id=` | Gateway delegates to Authz; service principal tenant must match the requested tenant and `identity.user.read` is checked in the Authz handler |
| Display roles and account/query state | Same user-directory response | Authz `user_role`/`role` and account records are the source; UI does not infer roles or lifecycle state |
| Provision, disable, reset credentials or MFA | Not exposed in this UI | Identity-provider lifecycle APIs are not currently integrated |
