# BPMP Feature API Coverage

Ngày rà soát: 2026-11-21 (Asia/Saigon)

Tài liệu này phân biệt **API được đăng ký**, **API có handler/upstream**, **API có bằng chứng kiểm chứng**, và **API mới là đề xuất** khi đối chiếu các feature trên prototype với BPMP hiện tại. Đây là inventory triển khai, không phải phê duyệt domain proposal.

Phân loại screen/workspace prototype theo Shell, domain MFE, BFF, service owner và thứ tự port: [BPMP Prototype UI → ODS Architecture Map](ods-prototype-ui-architecture-map.md).

Kế hoạch chi tiết kiểm kê controls, API readiness, prototype mock/security risks, thứ tự phase và acceptance theo user journey: [ODS Prototype Porting Plan](ods-prototype-porting-plan.md).

## Kết luận điều hành

| Phân loại | Số operation | Ý nghĩa |
|---|---:|---|
| `current-registration` | 77 | Có trong OpenAPI runtime và khớp 1:1 với 77 route definitions của API Gateway. Một số nhóm route chỉ được đăng ký khi upstream tương ứng được cấu hình. |
| `proposed` | 256 | Có trong design OpenAPI/catalog; chưa được coi là runtime API, contract được owner duyệt, hay deployable đã sẵn sàng. |
| Tổng design catalog | 333 | `docs/refactor-prototype/api-spec.openapi.json` và `api-catalog.json` là danh sách chi tiết theo operation. |

**Không kết luận 77 operation đều đã chạy trên stage hoặc đều có E2E riêng.** Bằng chứng hiện có là route/spec parity, test theo service/vertical slice và BPMP full-flow E2E đã ghi trong progress ledger. Stage promotion vẫn đang chờ staging target/credentials/signing context được cấp.

### ODS Shell parity snapshot (2026-11-21)

| Slice | ODS UI hiện tại | API đã nối trong ODS | Gaps còn lại |
|---|---|---|---|
| Task | Task MFE, điều hướng My/Group từ Shell | Work-item list scope, detail, audit, claim/unclaim/complete/delegate, published form schema/version và completion outputs | Không có completed-history hoặc bulk-action runtime API; realtime/freshness acceptance với upstream thật còn mở. |
| Authoring/deployment | Studio MFE remote, BPMN visual canvas + source editor, DMN XML và JSON Schema form editor | Process + decision create/detail/revisioned draft/validate/check-in; form create/detail/draft/validate/publish; deployment history/detail polling/rollback qua same-origin `callApi` | Canvas hỗ trợ linear và paired one-level gateways; cấu trúc khác giữ source-only và bảo toàn nguồn. E2E mock xác minh request sequencing/contracts, không chứng minh service acceptance trên stage. |
| Access/config | Studio MFE domain workspaces | Access catalog create/update draft JSON, validate, publish, owner entries, audit, acknowledgements; đọc roles/users/queues; tạo/sửa assignment policy có optimistic version; Configuration profile create/version draft/publish/rollback/restore/retire/diff | Chưa có Identity user/role mutations hoặc visual policy/grant editor; catalog JSON chịu validate từ Authz owner. Configuration values phải hợp schema owner. Gateway route cần upstream cấu hình; identity/branch semantics vẫn là proposal chờ domain owner. |

**Kết luận:** ODS hiện có API-backed vertical slices cho Task, BPMN/DMN/Form Authoring, deployment, Access catalog và Configuration lifecycle; mutations đi qua `callApi` với idempotency key và conflict reconciliation. BPMN graphical canvas đã có giới hạn hỗ trợ được công bố. Identity user/role mutations và Task completed history/bulk action vẫn thiếu runtime API. Không đánh đồng E2E fixture với upstream/staging evidence.

### Quyết định domain đã xác nhận và contract còn cần chốt

Các endpoint bên dưới vẫn chỉ có trong design catalog (`proposed`), chưa phải Gateway runtime routes. Các câu trả lời của product/domain owner được ghi nhận là quyết định đầu vào; chi tiết protocol còn thiếu tiếp tục là proposal cần Authz/Identity hoặc Human Runtime owner chốt trước khi triển khai.

Phương án kiến trúc đầy đủ, gồm PKCE/BFF, role/branch model, event visibility, batch modes, quotas, retry, retention và acceptance gates: [ADR-012 proposal](decisions/ADR-012-authorization-identity-task-history-batch-proposal.md).

#### Identity và tenant access

| Chủ đề | Đã xác nhận | Còn cần chốt trong contract |
|---|---|---|
| Nguồn danh tính/xác thực | Keycloak IdP là nguồn dữ liệu chính; đăng nhập qua SSO. BPMP không phải nguồn quản lý credential. | OIDC issuer/client, subject key ổn định, claim mapping và đồng bộ tên/email; vòng đời tài khoản bị khóa/xóa ở Keycloak phản ánh vào session/membership BPMP ra sao. |
| Tenant và quyền | Sau SSO, user được phân quyền vào từng tenant. Mỗi tenant cấu hình linh hoạt user membership, role và permission riêng. Role/permission tenant là authorization data của BPMP/Authz, độc lập credential IdP. | Tenant switching/session binding; org/branch scope (chưa có quyết định); system roles so với tenant roles; role inheritance; permission catalog và điều kiện xóa role đang được gán. |
| Write guarantees | Optimistic concurrency, idempotency key và audit bắt buộc cho mọi mutation Identity đã được xác nhận. | Updates/actions trên resource hiện hữu dùng `expectedVersion`/ETag và 409 khi stale; create dùng idempotent command + uniqueness vì chưa có version trước đó. Chốt phạm vi/TTL key, event schema/retention audit; mapping lock/unlock/revoke-session sang Keycloak hay membership-local. |

API proposal thuộc Authz/Identity owner: `GET/POST /v1/admin/users`, `GET/PATCH/DELETE /v1/admin/users/{userID}`, `/restore`, `/lock`, `/unlock`, `/revoke-sessions`, user-role assignment; tương tự collection/detail/restore và role-permission assignment cho `/v1/admin/roles`. User resource đề xuất là BPMP tenant membership/access profile tham chiếu cặp immutable `(idpIssuer, idpSubject)`, không phải credential/account tại Keycloak. Tên và semantics của routes chưa được duyệt. Acceptance: mọi read/write tenant scoped; authorization mặc định deny và được kiểm tra tại service trên từng object; IdP-owned credentials/claims không bị BPMP ghi ngược; stale version trả conflict; replay cùng key/payload không tạo hiệu ứng lặp; key dùng payload khác bị từ chối; audit ghi actor, tenant, resource, action, outcome và correlation, không chứa token/secret.

#### Task event history

| Chủ đề | Đã xác nhận | Còn cần chốt trong contract |
|---|---|---|
| Nguồn dữ liệu | Event Store là nguồn lịch sử có thẩm quyền; history không được tổng hợp từ UI state hay chỉ từ log ứng dụng. | Event types nào là user-visible, thứ tự/cursor pagination, visibility actor, PII masking, retention và consistency/freshness sau command. |
| API proposal | `GET /v1/work-items/{workItemID}/history` thuộc Human Runtime. | Response envelope/cursor, event vocabulary/version, authorization model và error codes. |

Acceptance: tenant/object authorization trước khi đọc; cursor ổn định không làm mất hoặc lặp event; event có occurrence time, actor (nếu được phép hiển thị), action, outcome, correlation/causation reference; redact theo policy; event store unavailability được trả rõ ràng chứ không giả lập lịch sử rỗng.

#### Task bulk actions

| Chủ đề | Đã xác nhận | Còn cần chốt trong contract |
|---|---|---|
| Hành động | Hỗ trợ đủ claim, unclaim, complete và delegate. | Hình dạng payload riêng cho complete/delegate, điều kiện hợp lệ và kết quả từng action. |
| Concurrency | Mỗi item bắt buộc có `expectedVersion`. | Tên field/đơn vị version, cách trả version conflict và có cho item lặp trong cùng batch không. |
| Kết quả | Cả atomic batch và partial success đều cần hỗ trợ; client phải chọn mode rõ ràng, không ngầm đoán. | Atomicity khả thi theo ranh giới transaction của Human Runtime; status/HTTP sync hay async; semantics item authorization failure trong atomic mode. |
| API proposal | `POST /v1/work-items/batch-actions` thuộc Human Runtime. | Action-specific request/response, batch status/result query nếu async, giới hạn/quota và retention kết quả. Design OpenAPI hiện đặt `maxItems: 100`; đó mới là con số proposal, chưa được load-test/domain owner phê duyệt. |

Baseline kỹ thuật đề xuất cho owner review: xác thực và kiểm tra tenant/action permission tại server cho từng item; worker re-check quyền khi chạy hoặc dùng ủy quyền bền vững được lưu rõ ràng (phải chọn một); atomic mode chỉ được nhận nếu bảo đảm all-or-nothing thật trong cùng consistency boundary, nếu không thì từ chối trước khi có side effect; partial mode lưu trạng thái/result từng item để retry chọn lọc. Idempotency namespace nên chứa tenant + actor/caller + operation + key, fingerprint payload; cùng key/payload trả trạng thái/kết quả tương đương, cùng key khác payload trả conflict; lưu dedupe cả batch và item để worker restart không lặp transition.

Retry chỉ cho lỗi tạm thời và thao tác an toàn, có deadline/attempt budget hữu hạn, exponential backoff+jitter và tôn trọng `Retry-After`; không retry mù lỗi validation, authorization, business rule hay version conflict. Timeout sau side effect là kết quả chưa xác định: tra trạng thái bằng operation/item identity trước khi phát lệnh mới. Numeric max attempts, delay, deadline, per-tenant concurrency/queue quota, payload limit và result retention phải được benchmark trên stage rồi owner duyệt; chưa chốt hằng số.

Audit/correlation baseline đề xuất: audit bền vững cho accepted/rejected command và kết quả từng item; giữ actor, tenant đã xác minh, executor/worker, action, resource ID, outcome/reason, expected/result version, operation/batch/item ID, request/correlation/trace ID, attempt và timestamp; không ghi credential/token hoặc payload nhạy cảm. Audit retention, kênh ghi bền vững và quyền tra cứu vẫn cần owner/policy chốt.

Sau khi owner xác nhận các phần còn mở, cập nhật runtime OpenAPI, Gateway route/upstream registry, service handler, persistence/event schemas và contract tests đồng bộ; khi đó mới bật các thao tác UI. Bản thiết kế này chưa xem các API proposal là runtime đã triển khai.

## Trạng thái API theo feature BPMP

| Feature trên prototype / Cockpit | API family hiện có và owner | Tình trạng triển khai | Khoảng trống trước khi gọi là parity |
|---|---|---|---|
| **Client Portal / khởi tạo quy trình** | `POST /v1/workflows/{workflowType}/instances` (Engine qua Gateway); worklist từ `GET /v1/work-items` (Human Runtime) | Route hiện hành; command thuộc Engine, work-item là projection của Human Runtime. Cockpit Client Portal gọi worklist và Start Workflow dùng command thật. | Prototype tự thêm instance vào state cục bộ. Cần nối portal vào API thật, lấy catalog workflow khả dụng từ nguồn được phân quyền, hiển thị receipt và trạng thái projection riêng biệt. |
| **Tasklist / Human Work** | `GET /v1/work-items`, `GET /v1/work-items/{workItemID}`, `POST .../claim`, `.../unclaim`, `.../complete`, `.../delegate`, `GET /v1/audit-records`; published form đọc qua Authoring | Các route hiện hành; Task MFE đang nối list/detail/action/audit với BFF. Broker-backed E2E hiện chứng minh các luồng claim cạnh tranh, complete, projection và Engine leader failover. | UI prototype dùng `INITIAL_TASKS`, local complete/claim và bulk action. Form submit với schema thật, delegation/claim UI parity, bulk action có idempotency/version, realtime cursor và latency/freshness gate còn phải đối chiếu từng action. `targetBatchWorkItemActions` và `targetSubmitWorkItemForm` là proposed, không được giả làm API đã có. |
| **Modeler / BPMN, DMN, Form** | 29 operation Authoring hiện hành: process model list/create/detail/draft/validate/version/check-in; decision model tương ứng; form CRUD/draft/validate/publish/version/read; published form read | Studio MFE hỗ trợ BPMN canvas (linear và paired one-level gateway), source mode, create/draft/validate/check-in; DMN source lifecycle; form JSON Schema lifecycle. | BPMN topology ngoài subset được giữ source-only. UI lifecycle E2E dùng Gateway fixtures; service/engine live integration và authoring worker restart/leader-change vẫn là gate riêng. |
| **Runtime deployment / rollback** | `POST /v1/authoring/process-models/{processModelID}/deployments`, `GET /v1/authoring/deployments/{deploymentID}`, `POST /v1/authoring/deployments/{deploymentID}/rollback`, list deployments | Studio MFE gọi deployment command, poll detail đến terminal state, render history và gửi rollback qua `callApi`; command có idempotency key. | Browser E2E dùng mock Gateway; deployment receipt/partial ACK/restart/rollback cần upstream contract integration evidence. Staging vẫn chưa có target được ủy quyền. |
| **Operation / Process Explorer** | 15 operation Projection gồm process query/completed query/capabilities, event query, aggregate, saved query/dashboard CRUD, export lifecycle, query-plan explain | Gateway/Projection handlers hiện hành; Cockpit có query/explorer pages. Read model là eventual/rebuildable, không nhận command. | Prototype operation pages hiển thị `INITIAL_INSTANCES` và cập nhật cục bộ. Cần nối filter/sort/paging, permission-before-aggregate, staleness, export lifecycle và query capability; không thêm process mutation vào Projection. |
| **Optimize / dashboards** | Dùng lại Projection process query, aggregate, saved dashboards/queries, export | API hiện hành ở backend; một số màn Cockpit đã tích hợp query/dashboard. | Prototype metrics/chart đang lấy mock data; xác nhận mỗi chỉ số có định nghĩa, nguồn/refresh/staleness và authorization trước khi nối. Các Report/OLAP endpoints mới là proposed. |
| **Identity / organization** | `GET /v1/access/users` hiện hành; Authz Control Plane có `/api/v1/organizations` và node operations ngoài Portal Gateway OpenAPI | Có API owner-side, nhưng organization API là contract riêng; không được tính vào 77 public Gateway operations. | Đã xác nhận Keycloak là IdP/SSO source-of-truth và BPMP quản lý membership/role/permission theo tenant. Mutation API vẫn chưa có runtime. Session-bound tenant switching, Keycloak claim/session lifecycle, org/branch tree và customer/staff authorization còn cần contract/owner decision. |
| **Access / authorization catalog** | 10 Gateway operations hiện hành: catalog versions/entries/roles, draft create/update/validate/publish, bundle, audit, acknowledgements; queues và assignment policy thuộc Human Runtime | Studio MFE gọi create/update/validate/publish, đọc entries/roles/users/audit/acknowledgements, và tạo/sửa assignment policies với expected version qua `callApi`; Authz/Human Runtime vẫn giữ quyết định owner-side. | Identity user/role mutation API chưa có trong current contract; chưa có visual policy/grant editor. Identity/branch/tenant policy expansion vẫn proposal chờ owner. |
| **Task Management / queue policy** | `GET /v1/access/queues`, `GET/PUT /v1/assignment-policies` (Human Runtime) | Route hiện hành; policy do Human Runtime quản lý, engine vẫn là nơi quyết định transition. | UI prototype cần được đối chiếu field/version/conflict/audit với assignment policy contract. Không cho UI quyết định quyền complete hoặc tự sửa workflow state. |
| **Foundation / configuration** | 9 profile operations trên Gateway tới Configuration Service + `GET /v1/runtime/browser-configuration` | Studio MFE hỗ trợ profile create/detail, draft, publish, rollback, restore, retire và diff; mutations dùng aggregate-version và idempotency. | Values do service validate theo owner policy; UI không chỉnh network endpoints/secrets. Browser E2E fixture chưa xác nhận Configuration Service trên stage. |
| **Case / Document / Integration / MDM / Notification / Report / Governance / Session** | 256 operation proposed trong design catalog | Thiết kế/định hướng, chưa đưa vào runtime public contract. Một `GET /v1/cases/{caseID}` hiện có là case/work-item projection của Human Runtime, **không** phải Case aggregate service. | Chưa triển khai UI như thao tác thật. Chốt domain owner, actor model, persistence/outbox/inbox, retention/SLA/SLO, callback/idempotency, security/acceptance criteria trước khi tạo contract v1 và service. Keycloak/tenant top-level decision đã xác nhận; session/claims, org/branch, customer/staff authorization và retention vẫn mở. |

## Đếm operation theo owner

| Owner | Hiện có (`current-registration`) | Đề xuất (`proposed`) |
|---|---:|---:|
| Gateway | 1 | 2 |
| Engine | 1 | 23 |
| Human Runtime | 12 | 12 |
| Projection | 15 | 5 |
| Configuration | 9 | 0 |
| Authz | 10 | 26 |
| Authoring (bao gồm 9 operation Form bị thiếu tag trong spec) | 29 | 46 |
| Case | 0 | 20 |
| Document | 0 | 12 |
| Integration | 0 | 10 |
| Identity | 0 | 20 |
| Session | 0 | 4 |
| MDM | 0 | 41 |
| Notification | 0 | 8 |
| Report | 0 | 21 |
| Governance | 0 | 4 |
| Audit | 0 | 2 |
| **Tổng** | **77** | **256** |

Các tên operation, method/path, schema, owner candidate, priority và `x-status` đầy đủ nằm trong [design OpenAPI](refactor-prototype/api-spec.openapi.json) và [API catalog](refactor-prototype/api-catalog.json). Runtime contract hiện hành là [Gateway OpenAPI](../apps/go/api-gateway/internal/apidocs/openapi.json). Danh sách tương tác prototype có 1,386 dòng lexical inventory; đây là candidate inventory, không phải xác nhận tương tác đã review hoặc parity được chấp nhận: [UI/API mapping](refactor-prototype/ui-api-mapping.md), [CSV](refactor-prototype/ui-api-mapping.csv).

## Quy tắc trạng thái và bằng chứng

1. **Design-only**: `x-status: proposed`; không gắn nút UI vào như chức năng sẵn sàng.
2. **Registered**: có trong runtime OpenAPI và Gateway route registry. Hai nguồn hiện có cùng 77 operation. `Authoring`, `Configuration` và `Access` có thể bị bỏ khỏi mux nếu upstream tương ứng không cấu hình.
3. **Implemented**: ngoài route còn có request mapping, owner adapter, persistence/authorization tại owner, lỗi/timeout/idempotency/concurrency đúng boundary. Phải có test ở boundary tương ứng.
4. **Verified in deployment**: có deployable/version/environment, config/upstream readiness, test evidence và log/trace. Local Docker E2E không thay thế staging rehearsal.
5. **Prototype wired**: UI thực gọi contract đúng qua SDK/BFF; không tính mock state, raw fetch với bearer token phía browser, hoặc endpoint chỉ tồn tại trong design catalog.

## Thứ tự hoàn thiện đề xuất

1. Hoàn thành API parity cho **Task + Authoring/deployment + Access/config** trên các route đã có: dùng `callApi`/BFF session, xác nhận request/response và action-owner mapping; bao phủ draft/version, publish, deployment ACK/rollback, auth failure, conflict, retry và projection staleness.
2. Bổ sung cho mỗi current operation bảng contract-test/E2E evidence và trạng thái deployed; không đánh dấu toàn bộ 77 là E2E chỉ từ việc Gateway khởi động.
3. Giữ Case/Document/Integration/Identity/MDM/Report/Notification/Session expansion ở design-only đến khi domain owner duyệt quyết định và acceptance criteria. Sau đó mỗi bounded context được làm thành vertical slice riêng theo ADR-010/011.

## Gaps còn cần quyết định

- OIDC/PKCE, BFF session issuance/refresh/logout và signing-key rotation: Portal/Security owner.
- Tổ chức/branch source of truth và tenant switch semantics: Identity/Authz owner.
- Customer/staff actor separation và resource/field-level authorization: Product + Authz owner.
- Case aggregate ownership, case lifecycle và workflow-start saga: Case + Engine owners.
- Document classification, scan/quarantine, retention/hold/erasure: Document/Security/Legal owners.
- Integration callback trust, replay, retry, reconciliation và partner lifecycle: Integration owner.
- SLA/SLO, search cardinality, freshness and p95/p99: Product + SRE + domain owners.

Các quyết định này tiếp tục là **proposal chờ phê duyệt**, theo chỉ đạo hiện tại; tài liệu này không biến chúng thành quyết định đã được chốt.
