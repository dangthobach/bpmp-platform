# Domain decision pack và readiness gates

**Trạng thái:** đề xuất để domain/product/security owners duyệt. Không có proposal nào trong tài liệu này được xem là policy đã phê duyệt. Dùng các contract và invariant đã có trong repo cho lát triển khai hiện tại; không mở domain mới dựa trên giả định ở đây.

## 1. Nguồn và mức độ chắc chắn

- `requirements.md`, ADR và `docs/ods-microfrontend-microservices-refactor-plan.md` là nguồn cho invariant nền tảng đã quyết định.
- `docs/refactor-prototype/api-spec.openapi.json` phân biệt operation đang đăng ký với operation đề xuất. Operation đề xuất không phải API chạy được.
- `ui-api-mapping.csv` và `interaction-inventory.csv` là quét từ mã, chưa thay cho walkthrough/browser review.
- UI prototype là một app đơn khối. `Prototype area` trong CSV không khẳng định MFE tương ứng đã được tách hoặc deploy.

Tại lần tạo tài liệu, inventory có 1.386 dòng: 195 API candidate, 344 local draft, 813 local UI, 20 cần review thủ công và 14 control chưa thấy handler trực tiếp. Source audit hiện xác nhận 195/195 API candidate có `contractOwner` và `contractStatus`; 20 dòng `REVIEW_REQUIRED` đã được gom thành 10 tương tác trong bảng bên dưới; 14 control có proposal riêng. Toàn bộ 1.386 dòng vẫn mang `reviewState=PENDING_SOURCE_AND_BROWSER_REVIEW`; owner review chỉ áp dụng theo action/backend mapping liên quan. Đây là routing/source triage, chưa phải phê duyệt cuối.

## 2. Decision register

| ID | Cần domain owner quyết định | Proposal để review | Gate trước khi dùng làm runtime policy |
|---|---|---|---|
| DOM-01 Identity/session | Issuer, OIDC endpoints, tenant membership claims, session lifetime/revocation và quy trình key rotation | Requirements glossary nêu Keycloak. Dùng OIDC/PKCE; Portal Gateway giữ session cookie `HttpOnly`, `Secure`, `SameSite`, CSRF; propagate signed actor proof riêng với workload identity | Identity/security owner cung cấp issuer/audience/JWKS, claim contract, logout/revoke semantics và staging keys |
| DOM-02 Organization/branch/unit | Nguồn chuẩn cho cây organization, membership thời gian, reparent và semantics quyền theo unit | Tenant là scope bắt buộc. Proposal cây parent→descendants, sibling/ancestor không được suy diễn; queue là chiều độc lập và giao với branch scope bằng AND | Authz + Human Runtime owners duyệt graph owner, cycle/move behavior, query filtering và concurrent reparent semantics. Chưa bật Unit Worklist trước gate này |
| DOM-03 Customer vs staff | Persona, field/data classification, action nào customer được thực hiện và liên hệ identity/case | Public/customer DTO tách khỏi staff DTO; owner service authorize từng resource; không dùng role/UI visibility làm enforcement | Product/security duyệt persona-action-resource matrix, visibility, masking và audit class cho từng operation |
| DOM-04 Document/CSP | CSP, file types, malware scan callback, retention/legal hold, region, download authorization | Giới hạn upload 50 MB theo architecture plan; streaming upload; quarantine trước `DocumentAvailable`; Case chỉ giữ reference, không giữ bytes | Document/security owner cung cấp CSP/API/signature contract, retention/classification, expiry/revoke behavior và test data |
| DOM-05 SLA calendar | Time zone, business hours, holiday calendar, pause/resume và escalation policy | Human Runtime giữ deadline authoritative dưới dạng UTC instant; UI chỉ hiển thị deadline nhận từ owner | Operations + domain owner duyệt calendar source/version, DST/holiday semantics và escalation owner |
| DOM-06 Workload/SLO | CCU, task/case cardinality, query mix, peak command concurrency, freshness target và availability/error budget | Giữ page tối đa 50 và keyset pagination; Requirements đặt Human Runtime API P95 ≤500 ms dưới tải bình thường. Không suy ra capacity từ microbenchmark | Product/SRE cung cấp sanitized workload profile, target P95/P99, projection-lag objective và measurement window trước capacity acceptance |
| DOM-07 External integration | Hệ thống đích, credential reference lifecycle, callback authenticity, retry/compensation | Integration Hub owns inbox, external operation ledger, circuit state; external call không quyết định workflow transition | Domain/integration owner cung cấp endpoint/API, auth/signature, idempotency, timeout/rate limits và compensation semantics |
| DOM-08 Master data | Dataset owner, code mapping, effective-date semantics, publication cadence và consumer acknowledgement | Versioned immutable publication; consumer-local bounded replica; deterministic Engine nhận snapshot/resolved evidence tường minh | Data owner ký schema, validity semantics, publication/revoke policy, compatibility và freshness budget |

## 3. Action owner map

| Tương tác/command | UI area | Public facade | Owner dữ liệu/chính sách | Commit authority / consistency |
|---|---|---|---|---|
| Personal/group worklist, detail và audit history | Task MFE | Portal Gateway Task BFF | Human Runtime projection/query; audit owner theo evidence contract | Query eventual; response phải thể hiện page cursor và freshness |
| Claim, unclaim, delegate, complete | Task MFE | Task BFF | Human Runtime kiểm assignment/version và forward actor proof | BPMP Engine duy nhất authorize transition, idempotency và commit; UI reconcile projection |
| Worklist theo organization unit | Task MFE (chưa có trong vertical slice hiện hành) | Chưa chốt | Human Runtime query + Authz row filter cần contract DOM-02 | Chưa triển khai cho tới khi hierarchy và filter owner được duyệt |
| BPMN/DMN draft, validate, immutable check-in | Modeler/Authoring UI | Gateway Authoring routes | Authoring Service sở hữu drafts, source revision, version và audit | PostgreSQL local transaction; Compiler validate/sign WIR, không activate |
| Deploy/rollback workflow version | Modeler/Operations UI | Gateway → durable Authoring job | Authoring điều phối job; Compiler sở hữu artifact; BPMP Engine sở hữu active runtime version | Authoring outbox/lease + Engine Raft intent; báo `ACTIVE` chỉ sau safe-point apply và ACK của toàn bộ Raft members |
| Role/policy/action catalog | Identity/Access UI | Gateway → Authz | Authz Control Plane sở hữu catalog/policy lifecycle; resource owner là PEP | Immutable published version + consumer acknowledgement/revoke epoch |
| User/directory, lock/deprovision | Identity UI | Identity/IdP adapter, chưa hợp nhất vào Authz | External Identity Provider sở hữu credentials/directory lifecycle; Authz sở hữu grants | Revoke session/policy theo owner; không cho Gateway/UI tự đặt identity |
| Case create/search/timeline/comments | Portal/Case UI | Case BFF (planned) | Case Service (planned); Projection chỉ là read model | Case DB/outbox local; start workflow là saga idempotent sang Engine |
| Document upload/link/download | Portal/Document UI | Document BFF (planned) | Document Adapter/CSP theo contract DOM-04 | Upload session + quarantine + callback inbox; Case chỉ lưu reference |
| External endpoint, callback, retry | Foundation/Integration Ops UI | Integration BFF (planned) | Integration Hub (planned) | Operation ledger + inbox/outbox; retry bounded; compensation explicit |
| MDM publication, notifications, audit search | Foundation/Admin/Ops UI | Common/Admin BFF (planned) | Tách owner Master Data, Notification, Audit Projection | Mỗi owner có local persistence/outbox; search/notification không thay business authority |
| Monitoring/query/export | Optimize/Operations UI | Query/Report facade | Projection/Report owner; Authz filter tại query owner | Rebuildable projection; `asOf`/checkpoint và export policy epoch hiển thị rõ |

Row-level mapping cho toàn bộ control/callback có tại [ui-api-mapping.csv](ui-api-mapping.csv). `contractOwner` và `contractStatus` được nối từ API catalog; `ownerReview` vẫn pending. Mỗi vertical slice phải đổi review state sau khi người phụ trách xem handler, authorization, persistence, failure path và browser walkthrough.

### Source-review proposal cho 14 control chưa có handler trực tiếp

Đối chiếu từng entry `NO_DIRECT_HANDLER` với source hiện tại để phân biệt form submit, UI-only control và command/query còn thiếu. Owner dưới đây là candidate để review, không phải assignment đã được duyệt hay endpoint hiện có.

| UI ID | Quan sát từ source | Action/owner candidate | Review cần chốt |
|---|---|---|---|
| UI-0031 | Nút “Kết nối & tải dữ liệu” được submit qua form `onSubmit`; form gọi API kết nối và tải catalog | Authz Control Plane owns catalog/policy API; Organization owner cung cấp tree API | Chốt hai API base URL, token/session handling, tenant source, partial failure giữa hai owner và audit. Hiện là Admin UI connection flow, chưa phải hành động domain |
| UI-0088 | Global search input không có submit/change handler | Shell sở hữu input/navigation; từng resource owner (Task/Authoring/Case/Report) sở hữu search và authorization | Product chốt phạm vi, loại kết quả, ranking, deep-link, quyền lọc và freshness; chưa gán cho một service search tổng hợp |
| UI-0091 | Nút chọn ngôn ngữ không có handler | Shell UI preference | Chốt locale persistence (local preference hay profile owner) và supported locales; không cần business API nếu local-only |
| UI-0112 | Nút trang trước đang disabled và chỉ hiển thị trang 1 | Integration Hub nếu endpoint registry có paging; hiện tại control placeholder | Chỉ bật khi registry API có cursor/total semantics; acceptance cho empty/one/many pages |
| UI-0113 | Nút trang sau đang disabled và chỉ hiển thị trang 1 | Integration Hub nếu endpoint registry có paging; hiện tại control placeholder | Cùng contract UI-0112; không gửi command |
| UI-0114 | Page-size select có lựa chọn 10/20 nhưng không có state handler | Foundation UI local preference; endpoint registry query nếu danh sách được nối backend | Chốt page-size bound/default và reset cursor khi đổi size; hiện select không thay đổi dữ liệu |
| UI-0615 | Nút submit form tạo DMN snapshot; form handler gọi callback prototype | Authoring Service `CheckinDecisionModel` candidate | Chốt request gồm model ID, expected draft revision, digest, version tag/summary và idempotency; trạng thái hiện tại chỉ là mock callback |
| UI-0765 | Checkbox `Executable` dùng `defaultChecked`, không cập nhật selected process | Authoring owns BPMN draft; Compiler validates semantic | Chốt trường BPMN nguồn, default/validation và hậu quả non-executable khi check-in/deploy; hiện input không lưu |
| UI-0794 | Checkbox `Propagate` của Called Element dùng `defaultChecked`; processId handler hiện hard-code propagate=true | Authoring owns Called Element draft property; Compiler validates mapping semantics | Chốt boolean persistence, child-variable scope và compatibility khi gọi process khác version; hiện checkbox không điều khiển giá trị submit |
| UI-0801 | Checkbox `flowable:async` dùng `defaultChecked` | Authoring owns draft metadata; Compiler/Engine own supported execution semantics | Xác nhận có hỗ trợ trong BPMP/WIR; nếu có, chốt safe retry/side-effect semantics; không mặc định port thuộc tính Flowable |
| UI-0802 | Checkbox `flowable:exclusive` dùng `defaultChecked` | Authoring owns draft metadata; Compiler/Engine own supported execution semantics | Xác nhận có trường tương đương và semantics concurrency; từ chối rõ khi chưa hỗ trợ |
| UI-0805 | Source/target input của Input Mapping có `defaultValue`, không persist edit | Authoring owns I/O mapping draft; Compiler validates FEEL/schema | Chốt cấu trúc source→target, expression grammar, duplicate target và validation error location |
| UI-0811 | Source/target input của Output Mapping có `defaultValue`, không persist edit | Authoring owns I/O mapping draft; Compiler validates FEEL/schema | Cùng schema nhưng output direction; chốt unknown target, null/optional và runtime type mismatch |
| UI-0827 | Kafka hook checkbox được dựng từ event names cố định, `defaultChecked`, không persist | Authoring owns declared event binding; Engine owns committed domain event contract; Kafka platform owns topic ACL/schema | Chốt allowlisted event names, topic derivation, outbox/ordering/idempotency và consumer contract; UI không được tự publish Kafka |

Trong 14 entry: 2 handler thực tế đi qua submit form (UI-0031, UI-0615), 5 là UI/navigation hoặc placeholder pagination (UI-0088, UI-0091, UI-0112..UI-0114), và 7 control draft dùng default value/checked nhưng chưa persist. Các phân loại này chỉ xác nhận được từ source; vẫn cần UI owner walkthrough và domain owner review trước khi đổi `ownerReview` khỏi pending.

### Source-review proposal cho 20 dòng `REVIEW_REQUIRED`

Các dòng này đều nằm trong Access UI và gồm các cặp control/event handler. Source audit giúp phân loại hành vi hiện tại; owner/action dưới đây vẫn là candidate, không tuyên bố API đang deploy hay đã được security/domain duyệt.

| UI IDs | Hành vi được xác nhận trong source | Action/owner candidate | Điều kiện còn lại |
|---|---|---|---|
| UI-0022, UI-0023 | Form connect đọc token/tenant, lưu cấu hình kết nối vào `sessionStorage`, sau đó Effect gọi `refresh` | Authz Control Plane cho catalog/policy; Organization owner cho organization tree | Chốt OIDC/BFF session thay cho token nhập tay, tenant binding, CSRF/CSP, partial failure giữa hai owner; DOM-01/02 |
| UI-0033, UI-0036 | Refresh gọi 6 query: entries, versions, audit, branches, queues, roles; dùng `Promise.all`, lỗi một nguồn làm cả refresh thất bại | Authz owner cho catalog/version/audit/roles; Organization owner cho branches; Human Runtime/assignment owner cho queues | Chốt query source/API contracts, freshness, all-or-nothing hay partial state và retry; review owner của `/v1/access/queues` |
| UI-0039, UI-0040 | Đóng thông báo hiện tại bằng cách xóa UI state | Access UI local interaction | Không phải business command; giữ dismissible notice semantics |
| UI-0041, UI-0042 | Chuyển tab policy/catalog/rollout/audit bằng UI state | Access UI local navigation; data vẫn thuộc Authz/Organization owners | Browser walkthrough xác nhận tab state và query không phát sinh quyền ngoài owner API |
| UI-0044, UI-0046 | Tạo draft mới trong client bằng cách copy snapshot published/current, chưa gọi server | Authz Control Plane sở hữu policy draft; lưu bền vững chỉ xảy ra ở Save draft | Chốt draft-from-published behavior, version/revision CAS và khi không có published version |
| UI-0047, UI-0048 | Xóa một grant khỏi client-side draft array | Authz Control Plane sở hữu grant/policy; đây không phải delete của published policy | Chốt dirty-state/undo và bảo đảm publish chỉ dùng version đã validate |
| UI-0069, UI-0070 | Thêm grant rỗng vào client-side draft array | Authz Control Plane sở hữu grant/policy schema | Chốt field validation, capability/action binding, tenant/branch/queue scope semantics theo owner manifest |
| UI-0072, UI-0076 | Save tạo draft mới hoặc CAS-update draft với `draft_revision`, snapshot và change reason | Authz Control Plane | Chốt idempotency key/digest, stale revision conflict, audit evidence và response schema |
| UI-0073, UI-0077 | Validate draft theo version ID; sau response UI refresh trạng thái | Authz Control Plane validator; resource owners cung cấp registered actions/constraints | Chốt validation snapshot pinning, diagnostics schema, invalid/stale draft behavior và owner registration freshness |
| UI-0074, UI-0078 | Publish version đã VALIDATED, gửi version number và reason; UI báo signed policy/bundle sequence | Authz Control Plane ký và phát hành immutable bundle | Chốt publish idempotency, signer/key rotation, revoke/rollback, consumer ACK threshold/timeout và audit |

Các 20 row đã có source-triage nhưng `classification=REVIEW_REQUIRED` và `ownerReview` không được đổi trong generated CSV: browser walkthrough, owner/resource review, authorization/failure-state verification vẫn còn. Đây là chủ ý để không biến code-path observation thành API/domain approval.

## 4. Acceptance contract theo vertical slice

### 4.1 Task

1. Tenant và actor lấy từ trusted proof; browser không gửi actor/group/unit để cấp quyền. Human Runtime lọc theo owner policy và assignment data.
2. Query page tối đa 50, keyset cursor gắn với scope/query; client search hiện tại-page được ghi rõ. Không gọi nó là search toàn tập.
3. Claim/unclaim/delegate/complete mang `expected_version`, command ID, idempotency key và actor proof; retry dùng đúng key + payload. Reuse key với payload khác phải conflict.
4. Human Runtime không tự chuyển workflow state. BPMP Engine tái kiểm quyền và commit authoritative transition.
5. Receipt/acceptance tách khỏi projection convergence. Reconcile/refetch hiển thị version/freshness; SSE chỉ là invalidation hint.
6. Exit tests: duplicate/retry, two-actor concurrent claim, stale version, actor reassignment trước submit, projection lag, cancellation/timeout, reconnect/cursor gap và bounded p95/error budget trên PostgreSQL + Engine integration path.
7. Unit Worklist là acceptance chưa thể đóng cho tới khi DOM-02 có owner và row-filter contract.

Unit Worklist query contract candidate (pending DOM-02/Authz/Human Runtime approval): `GET /v1/work-items?unit_id={id}&queue_id={optional}&status={optional}&page_size<=50&page_token={opaque}`. Tenant and actor derive from trusted Gateway session/proof; client may select a unit only as a filter, never assert membership, descendants or candidate groups. Human Runtime owns assignment/work-item query and current assignment state; Organization owner resolves the tenant-scoped unit tree; Authz owner supplies the row-filter decision. Where both branch and queue scopes apply, intersection is AND. Cursor is opaque and binds tenant, actor, normalized filters, sort key, last key, and authorization/policy revision; each page re-authenticates current membership and policy, and a stale scope revision returns a typed restart-required response rather than continuing under old access. Page response includes `items`, `next_page_token`, `as_of`/projection checkpoint, and freshness metadata. Acceptance must prove reparent/revoke between pages cannot leak rows, forged/cross-tenant unit IDs disclose no existence, cursor replay with changed filters is rejected, queue+unit intersection is correct, projection lag is visible, and empty/expired cursor behavior is deterministic. This is a contract proposal only; do not wire the query until owners approve tree version, reparent semantics, error schema, and SLO.

### 4.2 Authoring và deployment

1. Draft save dùng revision CAS; validate/check-in áp dụng cho chính source revision và digest; version đã check-in immutable.
2. Gateway bind tenant, command/idempotency/correlation và actor proof; Authoring tự xác minh actor/capability/resource.
3. Deployment job bền vững: request dedup, sequence cấp transactionally, outbox/lease recover sau restart; retry chỉ với cùng deployment identity.
4. Compiler trả artifact content-addressed có hash/signature; production signing cần KMS/HSM key rotation và registry bền vững.
5. Engine xác minh scope/hash/config, ghi intent qua Raft, activate tại safe point; `ACTIVE` chỉ khi mỗi Raft member hiện hành ACK đúng artifact. Rollback nhắm tới deployment đã cài, không tới mock/local status.
6. Exit tests: changed payload/same key, worker restart/lease expiry, leader change, stale sequence, compiler invalid response, partial node/offline ACK, missing signature/hash, rollback/restart recovery và PostgreSQL/Engine integration.
7. Unit evidence covers compile→stage→Engine commit→reconcile, a lost commit response with identical retry, partial ACK remaining pending, all-node activation reconciliation, rollback state mapping and selected failure/backoff behavior (`apps/go/authoring-service/internal/application/deployments_test.go`). PostgreSQL integration covers deployment idempotency, live/expired leases, artifact staging, outbox/audit, persisted partial Engine receipt, reclaim by a different worker, and durable `ACTIVE` reconciliation. Release acceptance still requires a real Authoring-to-Engine run that kills/restarts the worker around Engine commit/receipt persistence, restarts Engine or changes Raft leader during partial/offline ACK, and verifies rollback recovery. Production gates also include object-store artifact registry, certificate/key rotation, telemetry/alerts/runbook, durable outbox retention and called-process dependency closure.

### 4.3 Case, Document, Integration, Common

Không bắt đầu implementation cho tới khi operation owner và các DOM decisions liên quan được duyệt. Contract tối thiểu của mỗi vertical slice phải chỉ rõ: public operation + schema version; owner/DB; actor and resource authorization; local transaction/outbox/inbox; idempotency/CAS; typed errors/retry/deadline; audit; query freshness; metrics/SLO; migration/recovery; acceptance tests.

Operation surface dưới đây trỏ tới `api-spec.openapi.json`, một **design artifact** có `x-status=proposed`; nó cung cấp candidate request/response schemas nhưng không chứng minh Gateway route/deployable đã tồn tại. `/v1/cases/{caseID}` hiện đăng ký thuộc Human Runtime projection, không phải Case aggregate target.

| Slice | Public contract candidate / owner | Contract design hiện có | Bắt buộc chứng minh trước `complete` |
|---|---|---|---|
| Customer Case | `GET/POST /v1/customer-cases`; `GET/PATCH /{id}`; `POST /{id}/submit`, `/withdraw`; `GET /{id}/timeline`, `/documents`, `/comments`; `POST /{id}/documents`, `/comments`; `DELETE /{id}/documents/{documentID}`. Case Service owns `case_db`; Portal/Case BFF owns facade | Request/response schemas, operation IDs và candidate error envelope ở `api-spec.openapi.json`; state/saga semantics ở `appendix/functional-analysis.md` và Case ERD ở `data-model-reference.md` | Case DB/outbox/inbox; create/update CAS + duplicate submission; create→workflow-start saga không duplicate qua timeout/crash; business state tách Engine state; comments INTERNAL không lộ customer; document refs không sở hữu bytes; `asOf`/staleness; tenant/row scope; không cross-service SQL |
| Document | `POST /v1/documents/uploads`; `POST /uploads/{uploadID}/finalize`; document metadata CRUD/version, verify, download authorization, hold/restore operations. Document Service owns `document_db` + object/CSP adapter; Document BFF owns facade | Candidate schemas/operation IDs ở OpenAPI; upload/retention ownership ở `DOM-04`, `databases-and-reporting.md`, document ERD | Streaming ≤50 MB; multipart/session interruption + expiry; MIME/extension/content mismatch; quarantine trước `DocumentAvailable`; duplicate/late scan callback; immutable content digest/version; signed download URL expiry/revoke/authorization; retention/legal-hold được owner duyệt |
| Integration | `GET/POST /v1/admin/endpoints`, `GET/PATCH/DELETE /{endpointID}`, restore, batch-actions, probe. Integration Hub owns `integration_db` + secret refs; Integration Ops BFF owns facade | OpenAPI hiện có registry/probe proposal, nhưng **chưa có callback ingress, delivery ledger read, retry/replay, cancel hoặc compensation operation/schema** | MTLS/signature + replay-window verification; inbox dedup; secret rotation without secret readback; SSRF/egress controls; deadline/bulkhead/circuit; bounded retry and idempotency; partial side-effect reconciliation/compensation; owner-defined callback route/auth/error/retention contract trước khi implementation |
| Master Data (MDM) | `/v1/mdm/models`, `/datasets`, record/import, `/publications`, `/resolve-batch`, `/snapshots`, `/changes`, `/subscriptions`, `/consumers`; MDM owns `mdm_db`; each consumer owns its local replica | Detailed candidate APIs, immutable bundle/evidence model, ownership and rollout in `appendix/mdm-service.md`; API schema proposal in OpenAPI | Immutable version/effective intervals; CAS and publication rollback as new generation; consumer ACK/lag/gap recovery; local snapshot pinning and replay after current pointer changes; tenant/scope isolation; no online MDM lookup inside deterministic core; memory/size/freshness bounds measured |
| Notification/Audit Common | Notification owns delivery state; Audit Search owns rebuildable index; business owners keep authoritative evidence | Target actions appear in interaction/API inventory, but no single common business-state owner is proposed | Durable delivery/retry; audit search rebuild from owner evidence; export authorization/policy epoch; cache/index loss cannot lose business state; retention and SLO approved by owners |

Integration callback and delivery operations proposal (pending DOM-07/Integration/Security review; routes are **not** in the current OpenAPI):

| Operation proposal | Contract boundary |
|---|---|
| `POST /v1/integration/callbacks/{bindingID}/{providerKey}` | Provider ingress; resolves tenant/endpoint/secret from the binding, never trusts tenant or actor in body. Verify configured mTLS or HMAC over raw bytes + timestamp + external event ID before persisting. Enforce content-type/body-size/rate limits. Commit callback receipt + inbox row atomically, return `202 {receipt_id, status: ACCEPTED|DUPLICATE}` only after durable acceptance. |
| `GET /v1/admin/integration/deliveries/{deliveryID}` | Admin/ops actor with tenant/resource authorization; returns redacted operation state, attempt count, next attempt time, response class and audit reference; never returns credentials or unrestricted request/response payload. |
| `POST /v1/admin/integration/deliveries/{deliveryID}/retry` | Authorized operator command with `command_id`, `idempotency_key`, `expected_version`, and reason. Retry reuses the original immutable payload digest and downstream idempotency key; changed intent conflicts. It creates a new bounded attempt, not a second business operation. |

Proposed delivery state: `QUEUED → DISPATCHING → SUCCEEDED`; retryable transport/429/5xx becomes `RETRY_WAIT → DISPATCHING`; exhausted or permanent failure becomes `FAILED_TERMINAL`; ambiguous external side-effect becomes `RECONCILING` until provider lookup/compensation resolves it. Callback receipt state is separate: `RECEIVED → VERIFIED → APPLIED`, or `DUPLICATE`/`REJECTED`. The Integration worker never commits an Engine workflow transition; Case/Engine consumes only a verified, deduplicated domain result through its owner contract.

Acceptance for this proposal: valid signature and rotated-key overlap accepted; invalid signature, stale timestamp and unknown binding rejected without disclosing tenant/resource existence; replay of the same provider event ID+digest returns the same receipt; same ID with a different digest is quarantined/rejected and audited; duplicate webhook never repeats the local effect; destination resolving to loopback/private/link-local or outside allowlist is denied; timeout-after-provider-success retries with the same downstream key and resolves to one effect; attempt cap/rate limit/circuit breaker are bounded and observable; manual retry requires capability + CAS + idempotency and cannot rewrite the payload; partial side-effect reaches reconcile/compensation rather than silent success/failure. Owners must still set exact timestamp skew, body cap, timeout, attempt/retention limits, callback auth mode and error schema from provider/security requirements.

Other specific unresolved API gaps before review: Case saga status/readback and workflow-start idempotency receipt; Document scan callback authentication/state vocabulary and CSP contract; and cross-slice error/idempotency header conventions. These remain contract work, not implementation authorization.

Case command/query contract candidate (pending Case/Product/Authz review):

- `POST /v1/customer-cases` creates only a tenant-scoped `DRAFT`; `PATCH /{customerCaseID}` requires `If-Match` with the current row version and is permitted only while editable. Commands bind `X-Command-ID` + `Idempotency-Key` + canonical request digest; same key/same digest replays, same key/different digest conflicts.
- `POST /{customerCaseID}/submit` requires `If-Match` with the expected case version, actor proof, `X-Command-ID` and `Idempotency-Key`. One Case DB transaction changes `DRAFT → START_PENDING`, writes audit/outbox and a workflow-start receipt; proposed HTTP response is `202` with `case_id`, `case_version`, `start_status`, `receipt_id`, and `as_of`. A durable worker calls Engine with the same stable command key. A known permanent rejection may reach a product-approved failure state; an unknown timeout remains `RECONCILING` and queries/retries the same Engine receipt, never creating a second instance.
- `GET /{customerCaseID}` and `/timeline` expose business state separately from workflow-start/runtime state and include `as_of`/freshness. `GET` also returns the durable workflow-start receipt/status (`PENDING`, `RECONCILING`, `STARTED`, or product-approved terminal outcome) so a lost submit response can be read back without resubmitting a new intent. Task decisions use Task command operations; Case has no parallel approve/reject command. Comments carry `INTERNAL|EXTERNAL` visibility and the owner authorizes/redacts each result. Document links are references to Document owner IDs only.
- Acceptance: simultaneous submit of one `expected_version` creates one saga; lost Case/Engine responses replay stable receipts; worker crash before/after Engine commit reconciles one instance; duplicate/out-of-order events do not regress case version; unresolved start remains visibly pending; row/tenant isolation; internal comments never appear in customer list/detail; revoked actor cannot read timeline/comments; projection lag and stale `as_of` are visible; no cross-service SQL. Customer/staff fields and action matrix remain gated by DOM-03.

Document upload/access contract candidate (pending Document/Security/Privacy/CSP review):

- `POST /v1/documents/uploads` creates a short-lived upload session bound to tenant, actor, owner reference, declared size/type and `X-Command-ID`/`Idempotency-Key`; it returns upload ID, bounded part/upload instructions and expiry. Proposed maximum is 50 MiB from the architecture plan, subject to owner confirmation. Bytes stream directly to a private quarantine namespace through the approved CSP/object adapter; never into Case DB or a shared public bucket.
- `POST /v1/documents/uploads/{uploadID}/finalize` requires `If-Match` with the expected upload revision and checksum. It verifies actual bytes/size/type, fixes an immutable object/version reference and enqueues scan work transactionally. Proposed state: `UPLOAD_PENDING → QUARANTINED → SCANNING → CLEAN|REJECTED`; only `CLEAN` content becomes attachable/download-eligible after metadata/policy checks. Failed/expired uploads are not visible as verified documents.
- Scanner callback route proposal is `POST /v1/documents/scan-callbacks/{providerKey}`; authenticate independently of browser session (mTLS or signed provider payload to be selected), and bind scan job + document version + verdict + payload digest. Persist callback receipt and inbox dedup before acknowledging `202`; duplicate same result returns the same receipt, while conflicting or late result is quarantined/audited and cannot overwrite a newer version. `POST /v1/customer-cases/{caseID}/documents` attaches only a same-tenant eligible document reference; it does not copy bytes. Every download request reauthorizes current actor/resource/policy and returns a short-lived signed URL or streamed response; deletion/hold/retention policy revocation must block new downloads.
- Acceptance: interrupted/expired upload cannot finalize; size/MIME/signature/checksum mismatch rejected; quarantine is never downloadable or attachable; duplicate/forged/stale scan callback cannot change state; scan outage leaves explicit pending status; cross-tenant attach/download denied; signed URL expires and revocation/hold blocks fresh URL issuance; retention/legal-hold and deletion evidence meet approved policy; Case stores no bytes. Callback route, MIME allowlist, URL lifetime, retention and legal-hold authority remain DOM-04 decisions.

Cross-slice errors and headers are still open: define a shared schema for stable `code`, retryability, correlation/trace IDs, conflict/current version, receipt/job reference and safe detail; standardize `Idempotency-Key`, `X-Command-ID`, `X-Correlation-ID`, and cursor/freshness headers before generated clients or UI integration.

## 5. Gate để đánh dấu action đã được xác nhận

Mỗi `uiId` của slice được domain owner xác nhận sau khi ghi đủ:

- use case/actor và resource, điều kiện tenant/row/field authorization;
- UI owner và backend operation owner, operation status `current` hoặc `proposed`;
- request/response schema, state transition và source of truth;
- loading/empty/403/409/validation/dependency/partial-result states;
- idempotency/CAS/retry/cancellation/reconciliation behavior;
- browser walkthrough hoặc interaction test, backend contract/integration evidence;
- quyết định liên quan từ DOM register với approver và ngày duyệt.

Chỉ có `current-registration` trong API catalog không đủ để đánh dấu action complete; nó chứng minh route registration, không chứng minh endpoint đã deploy, được authorize đúng hoặc UI dùng đúng semantics.
