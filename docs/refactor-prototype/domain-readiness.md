# Domain decision pack và readiness gates

**Trạng thái:** đề xuất để domain/product/security owners duyệt. Không có proposal nào trong tài liệu này được xem là policy đã phê duyệt. Dùng các contract và invariant đã có trong repo cho lát triển khai hiện tại; không mở domain mới dựa trên giả định ở đây.

## 1. Nguồn và mức độ chắc chắn

- `requirements.md`, ADR và `docs/ods-microfrontend-microservices-refactor-plan.md` là nguồn cho invariant nền tảng đã quyết định.
- `docs/refactor-prototype/api-spec.openapi.json` phân biệt operation đang đăng ký với operation đề xuất. Operation đề xuất không phải API chạy được.
- `ui-api-mapping.csv` và `interaction-inventory.csv` là quét từ mã, chưa thay cho walkthrough/browser review.
- UI prototype là một app đơn khối. `Prototype area` trong CSV không khẳng định MFE tương ứng đã được tách hoặc deploy.

Tại lần tạo tài liệu, inventory có 1.386 dòng: 195 API candidate, 344 local draft, 813 local UI, 20 cần phân loại thủ công và 14 control chưa thấy handler trực tiếp. Catalog gắn operation owner cho candidate; tất cả dòng vẫn `PENDING_SOURCE_AND_BROWSER_REVIEW`. Đây là bản phân công ban đầu để domain owners duyệt, chưa phải xác nhận cuối.

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

| Slice | Bắt buộc chứng minh trước `complete` |
|---|---|
| Case | Case DB/outbox/inbox; create→workflow saga retry không duplicate; projection `asOf`/staleness; tenant/row scope; không cross-service SQL |
| Document | Streaming ≤50 MB; interrupted upload; invalid MIME/extension; quarantine/scan; duplicate/late callback; signed URL expiry/revoke; retention approved |
| Integration | Signature/mTLS verification; inbox dedup; deadline/bulkhead/circuit; bounded retry; partial side-effect/compensation recovery |
| Master Data | Immutable version, effective intervals, publication rollback, consumer ack/lag, local snapshot pinning; no online lookup trong deterministic core |
| Notification/Audit | Durable delivery/retry; rebuildable audit search; owner evidence remains authoritative; loss of cache/search cannot lose business state |

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
