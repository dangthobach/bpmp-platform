# Kế hoạch phân tích và port BPMP Prototype vào ODS

Ngày lập: 2026-11-24 (Asia/Saigon)  
Trạng thái: kế hoạch kỹ thuật dựa trên source inventory; chưa thay thế domain-owner approval, API contract hay staging acceptance.

## 1. Mục tiêu và tiêu chí parity

Port `bpmp-prototype/` theo từng user journey vào ODS, giữ bố cục/hành vi có ích của prototype nhưng chuyển mỗi thao tác sang đúng owner. “Parity” có nghĩa là cùng use case và trạng thái người dùng cần xử lý, không phải sao chép mock data, bearer-token flow hay local mutation. Task/Case implementation tiếp tục trên local stack; staging/production promotion là deployment gate riêng, không phải điều kiện chặn phát triển chức năng.

Một action chỉ được bật trong catalog khi đã có đủ:

1. `uiId` và user journey được walkthrough, actor/persona, resource và capability được xác định.
2. UI owner, public Gateway operation, domain owner, source of truth và consistency boundary được ghi rõ.
3. Request/response, version/CAS, idempotency, audit và event semantics được xác nhận. API `proposed` không được gọi hoặc mock như production.
4. Loading, empty, validation, 401/403, 409, timeout-indeterminate, dependency failure, partial result và stale projection được thiết kế theo contract.
5. Unit/contract/integration/browser evidence theo blast radius; retry cùng command phải giữ nguyên key và payload. Query có cancellation/deadline và pagination bounds.
6. Capability/resource authorization được kiểm ở server. Có observability, rollout/rollback và remote degradation phù hợp.

Điểm bắt đầu là [UI/API mapping CSV](refactor-prototype/ui-api-mapping.csv) và [interaction inventory](refactor-prototype/interaction-inventory.csv), không phải việc bật tất cả nút đang thấy trong prototype. Inventory IDs phụ thuộc snapshot và có thể đổi khi generate lại.

## 2. Kết quả source audit

Inventory hiện tại có **1.386 dòng lexical** trên 25 file TSX/entry; một event callback và một native control có thể là hai dòng của cùng một action. Đã có browser spot-check các màn hình chính để đối chiếu cấu trúc và luồng; đây chưa phải walkthrough đầy đủ từng route, actor, tenant, control và lỗi.

| Prototype inventory area | API candidate | Draft/local domain state | Local UI state | Review thủ công / handler thiếu | Tổng |
|---|---:|---:|---:|---:|---:|
| Shell/UI | 7 | 0 | 35 | 2 | 44 |
| Portal UI | 30 | 64 | 170 | 0 | 264 |
| Authoring UI | 23 | 134 | 155 | 8 | 320 |
| Operations/Analytics UI | 22 | 25 | 90 | 0 | 137 |
| Identity/Access UI | 76 | 55 | 252 | 21 | 404 |
| Task UI | 18 | 21 | 28 | 0 | 67 |
| Task/Access UI | 11 | 29 | 51 | 0 | 91 |
| Foundation UI | 8 | 16 | 32 | 3 | 59 |
| **Tổng** | **195** | **344** | **813** | **34** | **1.386** |

Trong 195 API-candidate rows: **16** mang status `current-registration` duy nhất, **12** gắn cả `current-registration;proposed`, **167** chỉ `proposed`. Đây là số dòng control, không phải số operation duy nhất. Gateway hiện có 78 registered operations theo API operation reference; registration/upstream mux không chứng minh đã deploy hay action đã được UI nối đúng. Cả 1.386 inventory row còn `PENDING_SOURCE_AND_BROWSER_REVIEW`; 20 row cần review thủ công và 14 row chưa có direct handler.

### Rủi ro source đã xác định

- Prototype Client Portal khởi tạo từ `INITIAL_CLIENT_REQUESTS`/`INITIAL_PROCESS_DEFINITIONS`, sinh business key ngẫu nhiên trong UI, tự đổi request state và giữ file/comment/decision ở React state. Đây không phải Case/document lifecycle API.
- Tasklist và Task Management dùng `INITIAL_TASKS`, RBAC fixtures và cập nhật assignment/decision/log cục bộ; không thay thế Engine commit, HR projection hay durable audit.
- Identity và DMN có danh sách/version fixtures cùng nhiều bulk editor state. Chọn role/permission trong UI không cấp quyền authoritative.
- Operation/Optimize hiển thị sample instances/metrics. Chart không có nguồn/công thức/checkpoint hiện tại được xác nhận.
- Modeler có thao tác editor local hữu ích nhưng nhiều draft/DMN/variables đổi local state. Có selector với `onChange={() => {}}`; cần kiểm từng control để loại no-op.
- Access tự nhận API URL, organization API URL, tenant và bearer token, lưu token trong `sessionStorage`, sau đó dùng `fetch` trực tiếp. Deployment adapter dùng lại credential này. Flow này vi phạm ODS same-origin `callApi` + BFF session boundary; phải thay trước khi bật production.
- Endpoints modal thêm/sửa/xóa URL, method và auth type trong local state. Không đưa arbitrary endpoint/secret setup vào browser; connector/binding cần owner-side API, SSRF/secret controls và audit.
- File upload/download, document checklist, OCR, comments và external connector controls chưa có CSP quarantine/callback/inbox contracts tương ứng; không được hiển thị trạng thái đã lưu/đã scan bằng mock.

Source anchors: [prototype App](../bpmp-prototype/src/App.tsx), [Client Portal](../bpmp-prototype/src/components/portal/ClientPortalModule.tsx), [Access](../bpmp-prototype/src/components/access/AccessModule.tsx), [Access API adapter](../bpmp-prototype/src/components/access/accessApi.ts), [deployment adapter](../bpmp-prototype/src/components/modeler/deploymentApi.ts), [Tasklist](../bpmp-prototype/src/components/tasklist/TasklistModule.tsx), [Identity](../bpmp-prototype/src/components/identity/IdentityModule.tsx), [Foundation endpoints](../bpmp-prototype/src/components/foundation/EndpointsModal.tsx).

### Browser spot-check (2026-11-24)

Mở prototype local và xem các màn chính ở chế độ chỉ đọc. Đây là kiểm tra đối chiếu visual/luồng cho bề mặt, không phải xác nhận từng control, persona/tenant fixture, endpoint đang hoạt động hay staging API.

| Workspace | Bằng chứng UI quan sát được | Tác động lên kiến trúc/parity |
|---|---|---|
| Shell + Client Portal | Sidebar có 9 workspace; header có organization/tenant crumb, chuyển Client/Admin, search, bell, language/theme/profile; Portal có Dashboard, Metrics, New Request, My Requests, Staff Desk, Tracking; dashboard có KPI, bảng request và catalog. | Bảo toàn Shell hierarchy và Portal journeys; tách customer self-service khỏi staff desk và không dùng local persona switch làm authorization. |
| New Request | Wizard 4 bước: Process & Customer → Variables → Attachments → Review & e-sign; có business key, branch, customer/entity data và process selection. Không submit. | Cần DOM-03 actor/field policy, Case draft/submit contract, Document upload contract, consent/e-sign semantics. Không suy ra submit API từ nút wizard. |
| Modeler | BPMN/DMN mode, version selector, zoom/deploy/XML, categorized palette, token simulation, minimap, sample process khoảng 25 node và property tabs. | Editor là workspace riêng trong Authoring MFE; giữ canvas affordances trong giới hạn topology hỗ trợ, còn deploy đi qua immutable artifact + Engine receipt. |
| Operation + Optimize | Operation có process/version/task/ID/business-key/status filter, counts, instance table và Move Token/Migrate Version/Cancel controls (disabled ở trạng thái quan sát). Optimize có KPI/performance/cycle-time/throughput/SLA/heatmap/report và filter/export tabs; chart hiển thị sample figures. | Operation query thuộc Projection; các instance mutation đặc quyền cần contract/approval và không suy ra từ view. Optimize cần metric definition, checkpoint/freshness và Report authorization trước khi hiển thị số/export. |
| Identity + Access | Identity overview hiển thị số Users/Roles/Permissions/Resources/Service Accounts và các route quản trị. Access mở đầu bằng form API URL, Org API URL, admin token. | Chia Identity directory, Authz policy/catalog, organization và service-account owners; loại browser-token/direct-fetch connection flow trước production. |
| Tasklist + Task Management | Tasklist có My/Group/Completed tabs, search/priority/status sorting, list-detail/form và unclaim. Task Management đi theo process → BPMN element → queue, action matrix view/claim/execute/cancel, attribute filters và audit. | Tách query/command worklist khỏi assignment-policy editor; policy edit có CAS/audit, transition vẫn do Engine authorize/commit. |
| Foundation | Hub theo tabs gồm Process Registration/Template Generator, Allocation, Notification, Document Template Engine và Source System Endpoints. | Đây là nhiều owner/API boundary; không port thành một backend “common”. Endpoint admin và secret reference thuộc operator control plane. |

Browser review chưa mở dialog xác nhận mutation, không submit request, deploy, đổi policy, cancel/move instance, upload file hay gọi staging API. Wave 0 vẫn cần walkthrough có fixtures theo từng actor/tenant và gắn `uiId` tới route/API/owner; không cập nhật các row thành reviewed chỉ dựa trên spot-check này.

## 3. Phân loại các màn và thứ tự chuyển đổi

| Màn prototype | Journeys/chức năng cần bảo toàn | Đích ODS và data/API owner | Current ODS và việc cần làm |
|---|---|---|---|
| **Sidebar/Header + App** | Chọn workspace/module, collapse, breadcrumb, tenant/persona, search, notification, theme, profile/logout, toast và deep link | Shell: navigation từ signed catalog, route, session/context do BFF xác minh, capability visibility, theme/locale, error boundary. Shared UI chỉ cung cấp primitives. | Shell có catalog, loader/error boundary và layout cơ bản; local catalog chỉ công bố Case preview. Thay mọi giả tenant/user/menu bằng session/capability data thật; search chỉ tìm route được cấp; notification chỉ bật khi có owner feed. Logout/tenant switch phải là session command, không local state. |
| **Client Portal** | Dashboard; new-request wizard; service/workflow selection; business input; document list/upload; review/submit; my requests; officer desk; request/decision/comments; tracking lookup; embedded process; metrics | Case MFE sở hữu Case UX; Case BFF/Case Service sở hữu Case/draft/comment lifecycle; Document MFE/Adapter sở hữu upload/quarantine/scan/download; Task MFE sở hữu staff work item; Projection/Report chỉ đọc. Customer persona có route/policy contract riêng (DOM-03). | Case MFE hiện là local preview create/search/detail/timeline với loopback identity cố định, chưa phải Client Portal đầy đủ. Không gom employee review và customer self-service vào một authorization context. Bắt đầu Case authenticated create→readback→workflow reconciliation; thêm form/document/comments sau contract DOM-03/04. |
| **Modeler** | Process list/select; BPMN/DMN/Form editor; node palette/properties; source/XML; variables; DMN table/rules/version history/compare/inspect; validate/check-in; deploy/rollback/history | Authoring MFE; Authoring Service owns draft/revision/version; Compiler validates and signs artifact; Engine owns activation/runtime transition. Deployment facade/job remains Authoring BFF with Engine receipt. | Studio có authoring/deployment API lifecycle và giới hạn BPMN canvas đã ghi nhận; chưa phải mọi prototype affordance. Kiểm soát topology, unsupported XML preservation, save/validate/check-in và retry same intent. Loại direct `fetch`/browser bearer. Rà riêng DMN/form and version history feature parity. |
| **Operation** | Instance/process explorer, filters/search, detail/timeline, step/task operations, inspect payload, process query and saved views | Operations MFE; Projection owns query/read-model; Task BFF/Human Runtime/Engine own permitted task commands; Audit owner controls event visibility. | Cockpit có một số query surfaces; ODS Operations MFE chưa có. Chỉ port query contract đã đăng ký; thêm paging, filter authorization, `asOf`/staleness. Action trên task deep-link sang Task MFE hoặc gọi Task facade, tuyệt đối không mutate Projection. |
| **Optimize** | Throughput, health, SLA/heatmap, report, time-window filters, refresh/export | Operations/Monitoring MFE; Projection aggregate and saved-query owner; Report owner cho export/large reports. | Prototype chart/sample data chưa có metric definition/freshness evidence. Trước UI, chốt metric numerator/denominator, timezone/window, filter authorization, source checkpoint, retention và export policy. Reports API còn proposed không được tạo fixture thành “API-backed”. |
| **Identity** | Directory/search/detail; user profile; membership/role grants; status/deprovision; permission/resource catalog; service account | Identity MFE riêng khi route/action lifecycle độc lập. Keycloak owns credentials/directory; Authz owns tenant subject link/membership/grants and audit; organization owner supplies tenant-scoped tree; workload/service account có owner riêng. | Gateway/Authz có OIDC and parts of profile/role mutations; không đồng nghĩa directory, organization/branch tree, service account lifecycle đã đầy đủ. Chia thành subjourneys; bật only routes/capabilities có API+authorization evidence. Không đọc token claim rồi tự đổi tenant. |
| **Access** | Catalog/action/resource; policy draft; add/remove grants; branch/queue/resource scope; validate/publish; bundle ACK; audit; tabs | Access/Admin MFE; Gateway Access facade → Authz Control Plane; resource owner is PEP/action catalog. Queue assignment policy belongs Human Runtime, not Authz policy storage. | Studio AccessWorkspace đã gọi lifecycle cho catalog/assignment policy nhưng prototype Access có connection form/token, arbitrary endpoints, branch/org fanout và policy semantics rộng hơn. Chuyển session/callApi trước; tách Access policy khỏi Task assignment; mọi branch/subtree behavior chờ DOM-02. Publish chỉ theo validated immutable revision và ACK. |
| **Task Management** | Queue/element policy, actor/role scopes, audit viewer, rule changes | Task admin route trong Task MFE hoặc Human Workflow MFE; Human Runtime owns assignment/queue policy; Authz supplies identity/capability/row filtering. AccessAuditLog là evidence view có giới hạn field. | Current queue/policy facade có một phần API, nhưng prototype RBAC rows/mock edits không parity. Port read → versioned policy edit → audit/reconcile. Không để queue policy cấp workflow transition permission hoặc bypass Engine. |
| **Tasklist** | Personal/group/completed tabs; task search/filter; detail/form; claim/unclaim/delegate/decision; batch selection; audit/history | Task MFE → Task BFF → Human Runtime query/assignment; Engine authorizes/commits transitions; Authoring published form; history is bounded Engine + Human Runtime timeline. | ODS có personal/group/action/history/partial bulk. Prototype “completed” view and bulk approve/reject have broader/atomic-looking UX: map to only supported API; partial receipts must be explicit. Unit scope and completed-history depend on owner contract. SSE not live until approved session-bound proxy. |
| **Foundation** | Allocation/assignment rules, endpoint directory, system configuration, shared services | Split by owner: Configuration Service profiles; Human Runtime assignment; Integration Hub bindings/callback/delivery; MDM catalog; Notification delivery/preferences. Operations endpoint/secret management is an operator/admin control plane, never arbitrary browser fetch target. | Studio configuration profile lifecycle and Task assignment policy exist. Allocation, endpoint registry, MDM, Notification and Integration are not all current APIs. Endpoints modal local CRUD must be removed or replaced with owner-controlled binding API and secret references. |

## 4. UI action và event implementation contract

| Hành động trong UI | Nơi giữ state | Gọi API/event | Failure/consistency behavior |
|---|---|---|---|
| Sidebar, route, breadcrumb, collapse, theme, tab/dialog, editor selection | Shell URL/navigation state hoặc domain MFE transient/editor state; remote base path nằm trong signed catalog | Không phát business command. Nếu URL có resource ID thì reauthorize khi đọc resource. | Hỗ trợ direct link/back-forward; remote lỗi chỉ cô lập route; state tạm có thể reset theo policy đã định. Catalog route và remote exported path phải khớp. |
| Query/search/filter/sort/page/refresh | MFE query cache, key bao gồm tenant/session scope + canonical filter | GET same-origin Gateway qua `callApi`; keyset cursor cho tập lớn; cancel request obsolete. | Loading/empty/error; 401/403; cursor-expired/restart; `asOf`/stale indicator; không gọi database chéo. |
| Create/update/submit/claim/complete/delegate/publish/deploy/rollback | MFE giữ immutable in-flight intent/receipt, không tự làm authoritative state | Owner BFF với command ID, `Idempotency-Key`, `expected_version`/`If-Match` đúng schema. Service owner persist receipt/audit; Engine quyết định workflow transition. | 409 yêu cầu reconcile, không retry mù; timeout-indeterminate giữ nguyên payload/key và tra receipt/retry cùng intent; accepted ≠ projection converged; bulk hiển thị từng item. |
| Upload/scan/download/callback | Document adapter/CSP/integration inbox; MFE chỉ giữ upload-session/status reference | Bytes stream tới quarantine/private object store; finalize checksum/version; callback authenticated + inbox dedup. | Không preview/attach/download trước `CLEAN`; expired/interrupted rõ trạng thái; URL ngắn hạn và mỗi lần cấp lại phải reauthorize. |
| Workflow event/SSE/notification | Event source ở domain owner; transport/session tại Gateway/Cockpit Gateway | Backend publish committed/outbox event theo schema; browser nhận opaque cursor/invalidation scope rồi GET lại. | Duplicate/out-of-order/gap → refetch; reconnect bounded; auth expiry dừng; Kafka outage không làm rollback command đã commit. |
| Search/export/dashboard aggregate | Query owner và Report service | Authorized projection query; export là lifecycle command của Report owner nếu cần. | Permission trước aggregate, bounded query/export, checkpoint/retention/partial provenance và audit. |

Không cho phép MFE import prototype component, gọi domain service trực tiếp, đọc Kafka, giữ client secret, gửi tenant/actor để cấp quyền, hoặc giả lập receipt/status bằng `setState`.

## 5. Kế hoạch triển khai theo gate

Không tuyên bố phase/gate hoàn tất nếu thiếu evidence. Implementation của Task và Case được phép tiếp tục trên current/local contract; promotion tới staging/production vẫn chờ deployment acceptance. Thời lượng trong kế hoạch gốc (27–43 tuần, nhiều team) là tham khảo; không thể re-estimate theo lịch khi chưa biết staffing, domain-owner turnaround, staging window và workload.

### Wave 0 — UI inventory và route/action baseline

**Owner:** Platform/MFE Enablement + từng UI/domain owner.  
**Deliverables:** map `uiId → journey → target route/MFE → operation → owner → readiness`; thực hiện browser walkthrough có actor/tenant fixtures; nhóm callbacks thành journey (không dùng 1.386 row làm 1.386 task); cập nhật `ownerReview` chỉ sau review thật; đóng 14 no-direct-handler, phân loại 20 review-required và xác định các no-op/draft/local mock.  
**Gate:** không đổi review state tự động từ source scan; route nào không có contract current vẫn ở feature flag/catalog off.

### Wave 1 — Platform Phase 1 closure

**Owner:** Platform, Portal/Security, SRE.  
**Deliverables:** giữ signed catalog, MFE route contract, session/CSRF, same-origin `callApi`, trace, CI/container/chart rollback; kiểm tra controls của Shell chỉ dùng session/context/capability. Hoàn tất local release rehearsal trên Docker Desktop. Staging rehearsal là deployment acceptance riêng khi có target được ủy quyền.  
**Local release acceptance:** signed catalog promotion → readiness failure → rollback trên Docker Desktop; catalog signature/expiry/origin/compatibility; remote load/render failure; auth/CSRF/trace; immutable local image digests và previous known-good revision. Đây là gate để tiếp tục prototype implementation, không phải staging/production acceptance. Authorized staging promotion được theo dõi riêng khi có môi trường.
**Deployment dependency:** chưa có staging target/owner/window/credentials/signing keys/real digests. Việc thiếu cloud không chặn Task/Case implementation; local rehearsal không được báo cáo là staging acceptance.

**Cockpit sign-in blocker discovered and locally cleared (2026-11-25):** the previous Cockpit connected screen required a tenant ID and a pasted bearer token, persisted that token in `sessionStorage`, and sent it to service URLs. This contradicted the agreed Keycloak SSO + BFF session boundary. Cockpit now starts Gateway OIDC Authorization Code + PKCE, restores identity from `GET /auth/session`, keeps only tenant/actor identifiers in memory, uses the HttpOnly browser session cookie for Gateway requests, adds the CSRF token to unsafe requests, and calls CSRF-protected logout. The token paste UI and browser-held access token are removed. The Docker Desktop fixture provisions Keycloak and seeds the issuer+subject link into Authz. User trust-imported both local development CAs; browser acceptance completed Keycloak login and returned to Cockpit without entering a token. The first authenticated runtime-config call exposed missing Authz actor-context signing settings; the fixture now uses the existing `internal-actor-v1` signer trusted by Engine/Human Runtime, with an explicit audience allowlist. Authz restarted healthy and Cockpit loaded authenticated navigation/Work queue. The dynamic-Redis OIDC limiter mismatch was also fixed with a bounded per-IP 10/minute policy. Remaining: Cockpit Gateway SSE still rejects this browser session (`Realtime: unauthorized`); Identity directory/roles currently return `Forbidden`, Organization API route is unavailable, and Human Runtime is unhealthy in the existing stack. Add cookie/session-aware realtime and directory/organization BFF paths before treating Task/Case UI as live authenticated journeys. SSO access is unblocked; Task remains 80% and Case 45% until their separate realtime/performance and live domain gates close. Multiple tenant profiles still require a tenant chooser.

The local Keycloak fixture is disposable E2E-only: user `e2e-user` / password `bpmp-e2e-password`; the bootstrap admin password comes from `platform/e2e/.env`. Do not reuse these accounts or credentials outside that stack. Browser access uses `http://localhost:14173`, Keycloak uses `https://localhost:14443`, and callback uses `https://localhost:18443`. Trust both generated development CAs in the current user's certificate store before using the browser flow: `Import-Certificate -FilePath .\platform\e2e\runtime\secrets\keycloak-ca.pem -CertStoreLocation Cert:\CurrentUser\Root` and `Import-Certificate -FilePath .\platform\e2e\runtime\secrets\ca.pem -CertStoreLocation Cert:\CurrentUser\Root`. Keycloak's signing keys are loaded at Gateway startup; automated JWKS refresh/key rotation is still required before production use.

### Wave 2 — Phase 2 Task parity và realtime

**Owner:** Task/Human Runtime, Engine, Gateway, Shell/Identity.  
**Deliverables:** Tasklist personal/group, detail, form, claim/unclaim/delegate/complete, history, partial bulk; Task Management assignment policy theo route/capability; review DOM-02 và DOM-06 trước Unit Worklist; session-bound SSE invalidation sau đó.  
**Acceptance:** owner authz/tenant/row mask; concurrent claim one-winner/409; stale version; same-key replay vs changed body; timeout-after-commit receipt; cancellation; projection lag; cursor replay/gap/revoke/session-expiry; bounded load, p95/error/freshness trên workload được chủ sở hữu xác nhận. `ATOMIC` và unit scope không được bật nếu engine/owner boundary chưa chứng minh.

### Wave 3 — Bật các current API-backed authoring/admin journeys có kiểm chứng

**Owner:** Authoring, Compiler/Engine, Authz, Human Runtime, Configuration.  
**Journeys:** BPMN/DMN/form list→draft→validate→immutable check-in/publish; deployment submit→poll receipt→active/rollback; Access catalog draft→CAS save→validate→publish→consumer ACK; Configuration draft→publish/rollback/restore/retire/diff; Identity profile/role subjourneys chỉ nếu auth/session and API path sẵn sàng.  
**Acceptance:** public same-origin SDK, expected revision, idempotency, 403/409, retry/reconcile, audit and route-specific E2E; custom visual editor unsupported topology remains source-only; browser E2E fixture không tính là live owner acceptance. Không nhân bản Access policy logic giữa Studio và Task MFE.

### Wave 4 — Phase 3 Case + Client Portal split

**Owner:** Case, Product, Authz, Engine, Document (cho upload).  
**Deliverables:** authenticated Case BFF; Case create receipt/readback; workflow-start saga + live Engine/Authz credential proof; committed event inbox/dedup; Case 360 timeline/process tracker/projection freshness; tách staff/customer routes sau DOM-03. Local Case create/search/detail/timeline UI và Case-owned inbox/read path tiếp tục triển khai; production/customer route vẫn phụ thuộc DOM-03 và authenticated Gateway boundary.  
**Acceptance:** lost response/retry yields one Case/one workflow; worker crash before/after Engine commit reconcile; duplicate/out-of-order event cannot regress; cross-tenant/customer-field masking; comments visibility; Case `asOf`/projection watermark. Local acceptance can proceed with local Engine/Authz dependencies; production route remains disabled until authenticated Gateway and domain/security acceptance. Staging watermark evidence is an additional deployment check when a staging target exists.

### Wave 5 — Phase 4 Documents và Integration

**Owner:** Document/CSP/Security/Legal và Integration/partner owners.  
**Deliverables:** upload session → streamed quarantine → finalize/checksum → scan callback inbox → clean attachment → authorized short-lived download; integration binding/callback/delivery/retry/reconcile UI.  
**Acceptance:** 0/limit/over-limit, MIME/checksum mismatch, malware, interrupted upload, expiry, forged/duplicate/late callback, external timeout/429/5xx, ambiguous side effect, circuit/bulkhead and bounded queues. Duyệt data classification/retention/legal hold trước data thật.

### Wave 6 — Phase 5 Operations, Optimize và ODS Common

**Owner:** Projection/Report, Operations/SRE, MDM, Notification, Audit.  
**Deliverables:** Operation explorer/queries/saved views; Optimize dashboards; authorized report/export jobs; MDM versioned catalog; notification preferences/delivery; rebuildable audit search; alerts/runbooks. Foundation UI được chia route theo owner, không lập một common backend chứa mọi policy.  
**Acceptance:** permission-before-aggregate; definition/as-of/freshness for each metric; export authorization epoch; audit rebuild matches source; lost cache/index does not lose business state; alert and restore drills.

### Wave 7 — Phase 6 strangler cutover và hardening

**Owner:** Platform/SRE/Security + domain owners.  
**Deliverables:** route-by-route catalog rollout, tenant/capability canary, compatibility matrix, rollback, telemetry comparison and data reconciliation; remove Cockpit route only after zero traffic/dependency window.  
**Acceptance:** load/soak/chaos/DR/security, no regression to existing portal, successful rollback and signed reconciliation. Legacy state is not deleted until retention and ownership sign off.

## 6. Backlog ưu tiên và dependency

| Priority | Slice | Dependency | Definition of ready |
|---|---|---|---|
| P0 (local) | Signed Docker Desktop release rehearsal | current Shell/Task images, OCI manifest digests, ephemeral local key | Signed catalog verifies; Helm rollout/readiness canary/rollback evidence saved; no staging claim |
| Deployment | Authorized staging promotion | environment owner, target, secrets, signing keys, registry digests, change window | Inputs present; runbook rehearsal executed and evidence linked; does not block local implementation |
| P0 | Reconcile API/UI inventory snapshot | source browser review, runtime OpenAPI vs route registry/upstream status | All candidate IDs assigned; `proposed` separated; 34 review/no-handler resolved or explicitly deferred |
| P1 | Task current-scope parity + owner-approved DOM-02/06 | Authz/Human Runtime cursor/filter decision for Unit Worklist only | Continue current-scope UI/API parity; no unit route before contract |
| P1 | Task SSE and representative p95/freshness | session-authenticated proxy, origin/CSRF/cursor/reconnect limits | Security + workload approved; reconciliation and outage evidence |
| P1 | Authoring/Access/Configuration endpoint-by-endpoint acceptance | valid session, active upstream, owner/API contracts | all visible mutation controls carry receipt/version/idempotency/error state |
| P2 | Case authenticated BFF/start/inbox/freshness | Case/Product/Authz/Engine contract approvals | Continue local preview, Case-owned inbox/read UI and reconciliation; production route requires authenticated domain path, no fixed identity |
| P3 | Document/Integration | CSP/provider contracts, Security/Legal approval, retention and secret-management model | upload/callback/delivery failure paths implemented and verified |
| P3 | Identity directory/org and owner routes for Foundation | DOM-01/02/03, Keycloak/Authz/Organization APIs | capability matrix and actor/persona behavior accepted |
| P4 | Operation/Optimize/Report/Common | metric definitions, projection/report owner and SLO | all visible metrics have source, permission, checkpoint and freshness |
| P5 | route cutover/legacy decommission | all preceding gates, usage telemetry | strangler acceptance and signed data reconciliation |

**Parallelism rule:** Wave 0 source/browser inventory and staging-input collection can proceed together. Task and Case implementation may proceed on their current owner contracts/local acceptance while staging-input collection continues; production exposure still requires the relevant security/domain gates. Other cross-domain work may parallelize only after public contract and security boundary freeze. Don't parallelize multiple UI clones against an unsettled contract.

## 7. Estimates, assumptions and risks

- Architecture plan gives **27–43 weeks across multiple teams** for all phases. This plan does not tighten that range; critical path includes owner decisions, real Engine/Authz integration, external CSP/partner contracts and later deployment/cutover windows. Missing cloud access does not stop local Task/Case implementation.
- Unknowns: availability of environment/signing credentials; staffing and UI/product owner review time; live Authoring/Engine/Authz config; Keycloak and Organization API deployment; tenant/case/task cardinality, traffic distribution, p95/p99, projection-lag SLO; document volume/retention; partner rates and callback schemas.
- Highest migration risk: reproducing prototype customer/staff mixed persona and data visibility incorrectly; Access/Identity conflation; displaying mock acceptance as authoritative; browser-held tokens/arbitrary endpoint URLs; retrying a command with a new idempotency identity after an ambiguous response; exposing task/event payload through SSE; dashboards without metric definitions.
- Risk response: capability-filter navigation but enforce authorization at every API; stable command intent + readback; narrow per-domain DTOs; event outbox/inbox owned at source; schema/contract checks; route-level catalog flags; design-only controls stay hidden or clearly non-production.

## 8. Verification and reporting

For each journey add the minimum risk-matched evidence: UI state/validation tests, API contract/provider tests, service concurrency/idempotency/authz tests, Playwright browser flow, plus integration/fault/benchmark evidence where its owner requires it. Use representative data and measure endpoint P50/P95/P99, query cardinality, projection lag, command receipt-to-convergence, event backlog, bundle size and frontend memory for journeys where they affect acceptance. Do not add tests for static wiring when type/build gates already cover it; do not omit tests for authority/security/failure paths.

Update [ODS Refactor Progress](ods-refactor-progress.md) after evidence-producing slices only: exact active phase, changed deployables, source-of-truth boundary, workload/assumptions, commands and outcomes, edge cases, gates, residual blockers and next slice. Keep this plan and `domain-readiness.md` proposals pending until named owners accept them.

## 9. Source references

- [Prototype UI architecture map](ods-prototype-ui-architecture-map.md)
- [ODS architecture/migration phases](ods-microfrontend-microservices-refactor-plan.md)
- [Feature/API coverage](bpmp-feature-api-coverage.md)
- [Domain readiness and DOM-01..08](refactor-prototype/domain-readiness.md)
- [API operation reference](refactor-prototype/api-operation-reference.md)
- [Interaction inventory](refactor-prototype/interaction-inventory.md)
