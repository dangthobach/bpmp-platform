# Phân quyền và truy cập dữ liệu

**Trạng thái: đặc tả và lát triển khai Authz đầu tiên.** Authz Control Plane sở hữu lifecycle/version của authorization catalog và policy, ký và phát hành bundle; Engine và Projection đã có consumer/evaluator cục bộ. Đây chưa phải xác nhận authorization parity cho toàn platform. Không tạo authority RBAC/policy thứ hai, không thay quyền chuyển trạng thái của BPMP Engine và không phải migration vật lý.

Đặc tả này phục vụ định hướng BPMP đa lĩnh vực: các cơ chế authorization được tái sử dụng; action/resource, role binding, scope và policy có thể cấu hình theo tenant/lĩnh vực. Khác biệt được cấu hình trong schema/grammar được hỗ trợ; không fork implementation theo khách hàng. Quy tắc bảo mật nền tảng (tenant isolation, deny-by-default, owner enforcement, Engine transition authority) không phải tenant override.

## 1. Mục tiêu và nguyên tắc

Mục tiêu là để mọi service có cùng kết quả khi quyết định một chủ thể được thực hiện hành động nào trên tài nguyên nào, trong phạm vi dữ liệu nào, và được nhận những trường nào.

1. **Deny by default:** không có allow rõ ràng, policy không hợp lệ/không sẵn sàng, hoặc thiếu thuộc tính cần thiết thì từ chối. Missing filter hay kết quả không đầy đủ không bao giờ thành allow.
2. **Tenant isolation là ranh giới bắt buộc:** tenant lấy từ authenticated context đã xác minh, không từ body/query tự khai báo. Mọi row read/write bị giới hạn tenant trước policy nghiệp vụ.
3. **RBAC + ABAC:** RBAC cấp candidate action qua role; ABAC ràng buộc candidate theo subject/resource/action/context như organization, branch, queue, classification, owner, state hoặc purpose.
4. **Tách quyền thao tác và quyền dữ liệu:** quyền gọi action không đồng nghĩa quyền đọc mọi row/field; quyền đọc không đồng nghĩa quyền sửa hoặc transition.
5. **Control plane tập trung, evaluator tại nơi thực thi:** Authz Control Plane quản lý catalog/policy, version, ký và phát hành bundle; không phải runtime hop bắt buộc cho từng command/query. `bpmp-engine` đánh giá transition bằng signed bundle trong process; query owner có PEP và evaluator/filter tại owner hoặc adapter cục bộ theo cùng contract. BFF/UI không phải security boundary cuối.
6. **Least privilege, auditability và versioning:** quyền có scope tối thiểu; thay đổi policy có version bất biến, actor/reason/audit evidence và đường revoke.
7. **Engine giữ transition authority:** Authz không thay thế `decide/evolve`, transition validation, expected version hay idempotency của Engine.

## 2. Thuật ngữ và authority

| Khái niệm | Ý nghĩa |
|---|---|
| Subject | Người dùng hoặc workload identity đã xác thực. Actor proof của người dùng được giữ riêng với workload identity service. |
| Tenant | Boundary sở hữu dữ liệu và policy; lấy từ identity/session/token đã xác minh. |
| Resource | Đối tượng nghiệp vụ có owner rõ, gồm type, ID, tenant và thuộc tính policy cần thiết. |
| Action | Quyền có kiểu, ví dụ `case.read`, `case.update`, `task.complete`, `variable.read`, `policy.publish`. |
| Scope | Phạm vi grant/policy: tenant, organization, branch, queue, resource hoặc scope type được catalog khai báo. |
| Policy authority | Authz Control Plane: quản trị/publish catalog và policy versions, ký bundle, revoke và audit. |
| Evaluator | Component đánh giá policy bundle đã xác minh tại nơi thực thi; BPMP Engine dùng evaluator embedded cho transition. |
| PEP | Policy Enforcement Point trong service owner: xác minh decision rồi áp action/row/field constraint. |
| PIP | Nguồn thuộc tính authoritative: IdP/directory cho subject, owner service cho resource, trusted request context cho request. |
| Data constraint | Ràng buộc có kiểu: row predicate, field operation/masking hoặc giới hạn hành động. |

Authority: external IdP sở hữu credential/MFA; Authz Control Plane sở hữu permission catalog, roles, bindings, grants, policy version/signing/publication/revocation; business service sở hữu resource attributes, query execution và enforcement; Engine sở hữu workflow state/transition; audit evidence gốc ở owner, Audit Search chỉ là projection.

## 3. Mô hình policy và precedence

```text
Decision = evaluate(subject, tenant, action, resource, resource_attributes,
                    trusted_context, active_policy_version)
```

RBAC cấp candidate actions; ABAC giới hạn candidate theo scope/attributes. Role không tự cấp toàn tenant nếu binding không có scope phù hợp.

### 3.1 Quy tắc kết hợp chuẩn

1. Xác thực subject, tenant, actor/workload proof và trạng thái identity. Thiếu/sai → deny.
2. Xác minh resource thuộc tenant context; khác tenant hoặc không chứng minh được → deny/not-visible, không lookup ngoài tenant để phân biệt ID tồn tại.
3. Tập hợp allow candidate từ role bindings và direct grants còn hiệu lực, đúng tenant/action/scope.
4. Đánh giá policy/condition bắt buộc. Thiếu attribute, expression lỗi, policy chưa publish/không hỗ trợ → deny.
5. **Explicit deny thắng mọi allow**, độc lập thứ tự role hoặc service.
6. Không có allow candidate hợp lệ → deny.
7. Kết quả cuối luôn giao với tenant guard, owner invariants và mandatory constraints. Allow không thể nới lỏng các ràng buộc này.

Các vùng allow hợp lệ được hợp nhất bằng **union**; kết quả sau đó giao (**AND**) với tenant guard và mandatory constraints. Explicit deny loại trừ phần bị cấm. Không có quy tắc ngầm “policy mới nhất thắng” hoặc “scope rộng nhất thắng”; conflict phải bị phát hiện khi validate/publish hoặc deny.

### 3.2 Dynamic action/resource catalog

Action/resource catalog được phân phối bằng **dynamic configuration**, nhưng dynamic configuration không được quyền tạo ra khả năng enforcement mà service chưa triển khai.

Tách thành hai lớp:

1. **Owner capability manifest (định nghĩa khả năng tối đa):** mỗi service phát hành manifest có version gắn với software release, khai báo các `resource_type`, `action_code`, field paths/operations, query capabilities, enforcement adapter/version và owner service. Đây là allowlist kỹ thuật về những gì code thực sự biết enforce; không thể được thêm tùy ý qua UI/config. Manifest bất biến trong một release và được xác thực nguồn gốc.
2. **Dynamic catalog/config (bật và cấu hình trong allowlist):** quản trị viên được phép chọn action đã đăng ký, bật/tắt theo tenant/environment, gán risk/audit class, cấu hình role/grant/policy bindings và scope values theo schema. Không thể đổi owner, đổi semantics, thêm field/action chưa có manifest hoặc nới tenant boundary. Tùy chọn label/help text phục vụ UI không có authority bảo mật.

Mỗi catalog entry tối thiểu có: `catalog_entry_id`, `resource_type`, `action_code`, `owner_service`, `manifest_id/version`, `supported_scope_types`, `field_operations`, `query_capabilities`, `mandatory_audit`, `risk_class`, `status`, `schema_version` và digest. Action code ổn định qua version; thay semantics không tương thích cần version/action mới và migration plan, không sửa nghĩa cũ tại chỗ.

Luồng publish:

```text
Service release -> signed capability manifest -> Authz validates owner/action/field support
Admin drafts dynamic catalog/config -> schema + semantic validation -> approval if required
-> immutable publish -> consumers resolve compatible snapshot -> acknowledge epoch -> activate
```

Các owner khai báo capability trong manifest code-defined tại `apps/rust/bpmp-engine/authorization-owner-manifest.json` và `apps/go/projection-service/authorization-owner-manifest.json`. Trước khi Access UI có action để cấu hình, workload owner đăng ký manifest bằng JWT Ed25519 của chính owner qua `apps/go/api-gateway/cmd/authz-owner-register` (Authz xác minh `sub == owner_id` và capability `authz.catalog.owner-register`). Ví dụ chạy từ `apps/go/api-gateway`:

```powershell
go run ./cmd/authz-owner-register --authz-url https://authz.example --audience authz-admin --manifest ../../rust/bpmp-engine/authorization-owner-manifest.json --private-key C:\secrets\bpmp-engine-ed25519.key --key-id bpmp-engine-k1
```

Projection đăng ký manifest của chính service, bằng key có `sub=projection-service`:

```powershell
go run ./cmd/authz-owner-register --authz-url https://authz.example --audience authz-admin --manifest ../projection-service/authorization-owner-manifest.json --private-key C:\secrets\projection-service-ed25519.key --key-id projection-service-k1
```

Đăng ký một lần theo manifest version; khi owner thay đổi capability hoặc field thì release manifest version mới bằng cùng luồng owner-authenticated. UI chọn đúng bộ `owner/resource/action/capability`, tránh gộp các capability khác nhau của cùng operation thành một lựa chọn mơ hồ.

- **Authz Control Plane giữ canonical catalog và policy semantics.** Dùng policy lifecycle, signing và publication của Authz. Configuration Service chung không trở thành authority về quyền; chỉ tích hợp nếu contract cụ thể yêu cầu và Authz vẫn giữ semantic ownership.
- Authz chỉ publish config tham chiếu manifest đã đăng ký và owner đã acknowledge. Entry thiếu owner, manifest, field/query enforcement hoặc consumer compatibility bị reject.
- Service phải fail closed với action không có trong manifest/snapshot hỗ trợ. Config không thể ép một service “enforce” quyền chỉ bằng cách thêm tên action.
- Dynamic catalog dùng draft → edit bằng `draft_revision` CAS → validate → publish immutable version → rollout/acknowledge → rollback/retire; mọi thay đổi audit actor/reason.
- Entry/action mới hoặc đổi owner cần software capability release trước, sau đó mới publish cấu hình bật. Deactivate entry chỉ ngăn cấp quyền mới; revoke binding/policy/session theo luồng revocation riêng.
- Platform/environment/tenant overrides chỉ được phép theo precedence schema đã định nghĩa; tenant không thể override platform security invariants hoặc bật action bị platform disable.
- Tái sử dụng bằng catalog/action semantics và policy primitives có contract chung; không sao chép policy evaluator/query filter vào từng lĩnh vực. Service-specific adapter chỉ chứa mapping tới resource attributes/query shape của owner, không nhân bản quyết định precedence.
- Cấu hình ngành/tenant có thể thêm role, binding, scope values và policy instances dựa trên action/resource/schema đã đăng ký. Thêm một action/resource kind mới cần manifest + owner enforcement release trước khi config có thể bật.
- Tránh “configuration language” tổng quát vô hạn: chỉ hỗ trợ typed operators, bounded expressions, supported attribute sources và explicit version. Unsupported requirement dẫn tới mở rộng capability có review, không nhúng script tùy ý vào policy.

**Bằng chứng hiện tại và giới hạn:** repo đã có Authz Control Plane với PostgreSQL policy/admin state, signed authorization artifact contract và policy bundle store; `bpmp-engine` có embedded evaluator. Authz server cũng mount HTTP decision/filter routes trong `app.rs`, dù README deployable ghi không route các endpoint đó. Configuration Service có profile/version/publish/rollback, nhưng `AuthzControlPlanePolicy` hiện là cấu hình vận hành (timeout, pool, graph memo, inactive-user job), chưa phải action/resource catalog. Catalog/policy authorization đi theo Authz lifecycle + signed bundles, không chuyển authority sang Configuration Service.

### 3.3 Scope, inheritance và grants

- Scope type là enum/catalog có semantics, không phải chuỗi tùy ý mỗi service diễn giải riêng.
- Mỗi policy grant phải chọn scope rõ ràng: `tenant_scope=true` cho toàn tenant, hoặc ít nhất một scope hẹp hơn như branch, queue, owner (`own_scope`) hay resource ID. Hai dạng loại trừ nhau; grant không có scope hoặc vừa chọn toàn tenant vừa chọn scope hẹp bị reject khi validate. Khi kết hợp nhiều scope hẹp, từng trục đã chọn kết hợp bằng AND; nhiều giá trị trên cùng trục kết hợp bằng OR.
- **Cấu trúc tổ chức của mỗi tenant là cây phân cấp parent-child**, trong đó các nút branch có `parent_branch_id` (hoặc quan hệ tương đương) và thuộc cùng tenant. Một root có thể có nhiều child; child có thể có nhiều cấp sâu. Cây không có cycle, mỗi branch tối đa một parent trong cùng tenant. Nếu doanh nghiệp có nhiều root, mỗi root vẫn là cây riêng trong cùng tenant.
- **Quyền branch được kế thừa xuống dưới:** binding ở branch `B` áp dụng cho dữ liệu gắn với `B` và mọi descendant của `B`. Binding ở child chỉ áp dụng child/subtree đó; không kế thừa lên parent, không sang sibling. Binding tenant-wide là scope riêng, rộng hơn mọi branch và chỉ được cấp rõ ràng.
- **Nguồn branch của process:** workflow config/definition khai báo đúng một binding `urn:bpmp:authorization / workflow / branch-id` (giá trị branch ID). Engine đọc binding này khi `StartWorkflow`, xác minh branch và ancestry với Organization owner, rồi ghi `resource_branch_id` vào workflow state/event/snapshot. Đây là nguồn authoritative cho process; không lấy branch từ request body, query parameter, user task form hay queue membership. Binding thiếu khi action bắt buộc branch, trùng binding, sai kiểu/rỗng/quá dài, branch không tồn tại/không active hoặc Organization resolver không sẵn sàng đều phải fail closed. Đổi branch của workflow definition chỉ ảnh hưởng lần start mới; process đang chạy giữ `resource_branch_id` đã gắn lúc start. Muốn chuyển process sang branch khác phải là command nghiệp vụ riêng, có action/policy, audit và cập nhật state nguyên tử.
- Một row nghiệp vụ mang `branch_id` authoritative; với process projection, owner map nó từ `resource_branch_id` do Engine phát hành, không tin một giá trị do client tự gửi. Để đánh giá binding branch `B`, row thuộc phạm vi nếu `row.branch_id` là `B` hoặc descendant của `B` theo cây branch cùng tenant. Không suy ra branch từ payload/client hoặc từ queue membership.
- Ancestry trong event/snapshot là lịch sử tại thời điểm workflow start, không phải nguồn quyết định quan hệ hiện tại. Projection giải quyết batch descendants tại thời điểm query từ Organization owner và lọc theo `resource_branch_id` authoritative: reparent làm quyền subtree đổi theo cây mới ngay khi Authz commit thay đổi; branch inactive hoặc ngoài cây hiện tại không match. Nếu resolver lỗi, proof hết hạn hoặc thiếu branch thì query fail closed; không fallback sang ancestry cũ.
- **Queue là trục scope độc lập với branch.** Queue grant giới hạn task/work item theo queue membership; nó không tự cấp quyền xem mọi dữ liệu của branch chứa queue. Khi action yêu cầu cả branch và queue, row/task phải thỏa **cả hai**: `branch_id ∈ branch subtree AND queue_id ∈ authorized queues`. Nhiều grant trên cùng một trục có thể hợp nhất bằng OR; các trục khác nhau kết hợp bằng AND. Nếu use case chỉ cần một trục thì action contract phải khai báo điều đó rõ.
- Organization/branch relationship là nguồn dữ liệu có owner và phải được xác minh; reject cycle, orphan, cross-tenant parent và concurrent move không hợp lệ. Việc chuyển branch của một node phải CAS/versioned, audit và cập nhật ancestry index/cache/policy attributes theo một cut có thể quan sát; không để hai service tự giữ cây khác nhau.
- Resource binding có thể thu hẹp nhưng không mở rộng qua tenant boundary. Quyền truy cập parent-wide từ child là ngoại lệ riêng, phải được biểu diễn bằng grant rõ ràng tới parent scope, không phải inheritance ngược.
- Role binding/direct grant có validity window, actor, reason và CAS/version. Grant tạm thời phải có expiry.
- Grant theo role so khớp **chính xác role code** trong JWT đã xác minh; không tự suy diễn kế thừa role cha/con. Authz validate role code tồn tại trong tenant. Nếu cần role hierarchy, issuer/evaluator phải mở rộng danh sách role theo một contract versioned trước khi bật semantics đó; không coi grant parent-role là quyền của mọi child-role một cách ngầm định.
- Direct grant là ngoại lệ có kiểm soát, không phải bypass policy; explicit deny chỉ dùng qua grant class được phê duyệt.
- Revoke giữ lịch sử/audit; không âm thầm xóa bằng chứng khi role/grant bị gỡ.

**Ví dụ:** actor có `case.read` tại Branch A; A có child A1 và A2; A1 có child A1a. Actor được đọc record gắn branch A/A1/A2/A1a nếu các điều kiện khác cho phép, nhưng không được đọc parent của A hay branch sibling B. Nếu thêm queue grant Q1, actor chỉ thấy các task vừa thuộc subtree được cấp **và** nằm trong Q1; grant queue không làm lộ toàn bộ case của nhánh.

## 4. Action, row và field authorization

### 4.1 Action authorization

Mỗi API/command ánh xạ tới action code ổn định có resource type, owner, mutability và audit class. Ví dụ `case.read`, `case.update`, `task.complete`, `variable.read`, `report.export.download`, `policy.publish`. Client không tự khai action thay route semantics; owner ánh xạ operation/command type sang catalog. Unknown action/resource → deny và telemetry.

Task completion có thể cần quyền `task.complete` để vào command path, nhưng Engine vẫn xác minh actor proof, transition, version và commit theo authority runtime. Grant không thể buộc Engine chấp nhận transition bất hợp lệ.

### 4.2 Row-level access

Authz trả decision/filter có kiểu theo các thuộc tính như `branch_id`, `owner_subject_id`, `queue_id`, classification. Owner service thực thi trên DB của mình:

- Ghép tenant predicate với filter quyền và filter nghiệp vụ bằng `AND`.
- Nhiều allow scope có thể `OR` trong nhóm an toàn; nhóm đó vẫn `AND` với tenant và mandatory constraints.
- Không nhận SQL, raw query fragment, tên cột tùy ý hoặc expression từ client/admin.
- Compile field/operator/value theo typed allowlist, parameterized query, bounded depth/count/cost/time.
- Áp filter **trước** pagination, count, aggregate, export, cursor và bulk selection; không lọc sau khi đã materialize/đếm/trả page.
- Detail-by-ID cũng chạy row filter; ID khó đoán không phải authorization.
- RLS có thể là defense-in-depth; không thay tenant context xác minh, PEP hay policy test.

```text
visible_rows = tenant_guard
               AND mandatory_owner_constraints
               AND (allow_scope_A OR allow_scope_B OR ...)
               AND NOT explicit_deny_scope
```

Đây là semantics logic, không quy định SQL dialect chung hoặc cho Authz truy cập database owner.

### 4.3 Field-level access

Resource schema phân loại field (ví dụ public/internal/confidential/restricted; taxonomy cần domain/security review). Policy field phân biệt `read`, `write`, `copy/export` khi cần.

- Response DTO chỉ serialize field được phép; UI masking không đủ.
- Mask/omit nhất quán ở detail, search, history, audit view và export.
- Mutation kiểm tra action lẫn từng field thay đổi; field nhạy cảm/reserved không được ghi qua generic patch.
- Filter/sort/group-by/aggregate trên field cũng là truy cập; không cho phép suy luận field cấm qua count/order/error khác biệt.
- Export/copy/share là action riêng hoặc policy tường minh; quyền xem trên UI không tự cấp quyền trích xuất.

## 5. Contract Authz ↔ service owner

Authz server hiện mount các protected routes `check`, `filter`, `query`, `query-revoke-feed`, `explain` trong `authz-server/src/app.rs`; README deployable đang không đồng bộ với router. Theo kiến trúc đích, các route decision/filter không phải runtime dependency bắt buộc cho mọi service. Cần inventory consumers trước khi giữ làm compatibility/admin surface hay chuyển từng consumer sang evaluator cục bộ + signed bundle.

Implementation hiện có signed authorization artifact contract, verified bundle store và embedded evaluator trong `bpmp-engine`; Authz Control Plane quản lý catalog/policy version, tạo/publish bundle qua outbox và Engine nhận bundle qua Kafka. Projection service là query owner đầu tiên dùng bundle/evaluator local; các query/export service khác cần onboard theo cùng contract trước khi xem là parity toàn platform.

**Lát triển khai hiện có:** Authz Control Plane lưu owner manifest và catalog snapshot theo version, có audit và lifecycle `DRAFT → VALIDATED → PUBLISHED → RETIRED`. Owner workload đăng ký manifest; validation giới hạn action/resource/scope/field theo manifest đã đăng ký. Publish yêu cầu reason, ký bundle Ed25519 và ghi bundle bất biến cùng outbox trong một transaction. Publisher phát envelope lên topic cấu hình bởi `AUTHZ_POLICY_BUNDLE_TOPIC`; Engine và Projection xác minh metadata/chữ ký, cài bundle cục bộ, phát acknowledgement lên `AUTHZ_POLICY_BUNDLE_ACK_TOPIC`, rồi mới commit offset. Authz đối chiếu hash với outbox và lưu acknowledgement idempotent theo tenant/bundle/consumer. Consumer group riêng cho từng Engine node giúp mọi node nhận mọi publication. BFF và Access UI hiển thị consumer đã acknowledge; điều này chưa khẳng định đủ mọi consumer bắt buộc nếu chưa cấu hình inventory rollout.

Bundle runtime có TTL 24 giờ. Authz kiểm tra mỗi 5 phút và xử lý tối đa 500 tenant mỗi batch; bundle cuối của tenant được gia hạn khi còn không quá 6 giờ hiệu lực. Nội dung policy/catalog giữ nguyên, còn `bundle_sequence`, `valid_from` và `expires_at` được cập nhật. Sequence của bundle độc lập với `catalog_version` và tăng đơn điệu trên từng tenant, vì vậy nhiều sequence có thể thuộc cùng một catalog version. Renewal ghi outbox bất biến mới, dùng cùng publisher/ACK protocol; tenant lock dùng chung với publish để serialize cấp sequence. Lỗi renewal được cô lập theo tenant, ghi tenant ID và thử lại ở chu kỳ kế tiếp để một bundle lỗi không ngăn các tenant khác được gia hạn. Renewal chỉ chạy khi signing key đã cấu hình; evaluator vẫn fail closed nếu bundle thật sự hết hạn. Outbox cũ được migration đánh dấu expiry `0` để được gia hạn. Vận hành phải cảnh báo khi bundle gần hết hạn hoặc ACK của consumer bắt buộc chậm hơn TTL.

### 5.1 Workflow binding cho branch của process

Workflow config là nguồn xác định **branch ID** của instance. Với BPMN hiện tại, khai báo process extension property `namespaceUri="urn:bpmp:authorization"`, `elementName="workflow"`, `name="branch-id"`, `type="string"`, `value="<branch-id>"`. Compiler chuyển giá trị cố định này vào WIR đã ký (`resource_scope_binding.branch_id`); Engine không đọc branch ID từ `variables` do client gửi. Engine xác minh branch đang active thuộc tenant và lấy ancestry/revision hiện hành từ Organization owner trước khi ghi `WorkflowStarted.resource_branch_id`; Projection lưu trường đó thành `resource_branch_id` bất biến cùng identity của process. Nếu mỗi instance cần chọn branch khác nhau, lựa chọn phải được resolve và ký bởi backend từ nguồn assignment tin cậy, không nhận trực tiếp từ browser. Thay đổi branch của process đang chạy phải đi qua một command và policy riêng có audit; không sửa metadata trực tiếp.

Branch ID cố định trong workflow config loại bỏ việc client tự chọn branch lúc start. Authz organization owner expose `GET /api/v1/branch-scopes/{branch_id}` cho người dùng có `organization:read`, `GET /internal/v1/branch-scopes/{branch_id}` cho Engine, và `POST /internal/v1/branch-scopes/descendants` cho Projection trên HTTPS mTLS listener riêng. Internal routes chỉ nhận tenant từ signed workload proof gắn với command ID; batch descendants nhận tối đa 100 branch roots, trả branch IDs hiện hành trong subtree cùng organization revision và giới hạn tổng kết quả. Engine start lấy branch ID từ WIR đã ký theo workflow config; command tiếp theo lấy branch bất biến từ event/snapshot. Engine kiểm tra ancestry/revision canonical trước resource evaluation. Projection tự ký proof workload ngắn hạn cho truy vấn descendants bằng tenant từ authenticated query context, rồi lọc `resource_branch_id IN (...)`; resolver lỗi, proof hết hạn, branch inactive hoặc kết quả thiếu thì fail closed, không fallback sang ancestry cũ. Không service nào lấy branch hay ancestry từ browser làm nguồn phân quyền. Authz chỉ chấp nhận workload/key khai báo trong `BRANCH_SCOPE_WORKLOAD_KEYS_FILE`.

Current ancestry resolution cho Engine hiện bao phủ đường lệnh authoritative qua `EngineCommandService`. `WorkflowStarted` và WorkflowSnapshot v3 lưu branch ID, canonical ancestry và organization revision; authorization audit cũng ghi scope được dùng. Boundary, local-task và remote-task dispatch hiện cũng resolve branch hiện hành từ Organization owner bằng proof do Engine ký cho đúng command; nếu chưa cấu hình resolver hoặc scope không còn active, command bị từ chối trước Engine.handle. Projection query dùng batch descendants hiện hành, không dùng ancestry snapshot làm quyền hiện tại. Không dùng branch ID hay ancestry trình duyệt gửi làm nguồn phân quyền.

Lát này chưa hoàn tất production authorization: owner service manifest registration chưa được expose qua BFF/UI; query service PEP/evaluator và row/field enforcement cần được tích hợp từng service; consumer acknowledgement/lag dashboard, key rotation/revocation SLA, rollback workflow và acceptance evidence vẫn là các gate riêng. Bundle publication hiện retry at-least-once; evaluator cài đặt idempotent theo tenant/sequence/hash. HTTPS mTLS của Authz cho Access BFF được bật bằng cấu hình runtime tùy chọn; triển khai phải provision chứng thư và CA client tin cậy.

**Projection PEP đã được nối local:** `projection-service` nhận publication envelope qua Kafka, xác minh canonical protobuf/hash/Ed25519 bằng public key allowlist, kiểm tra tenant/sequence/catalog metadata, ghi bundle mới nhất theo tenant vào PostgreSQL rồi mới ACK và commit Kafka offset. Startup đọc lại từng bundle từ bảng `projection_authorization_policy_bundle_state`, xác minh chữ ký/canonical form trước khi đưa vào evaluator; stale replay không làm lùi sequence. Query authorization lấy owner/resource/action từ cấu hình runtime và kiểm tra catalog capability + actor/role/capability grants cục bộ; row expression và field allowlist đi vào query preparation trước SQL. Durable revoke store vẫn được kiểm tra. Consumer group cần duy nhất theo từng replica để mọi instance nhận mọi tenant bundle; state bền vững trong PostgreSQL giúp restart không phụ thuộc Kafka retention/offset rewind. Bootstrap bundle legacy dùng sequence 1; Authz bắt đầu catalog publication ở sequence 2 để nâng cấp đơn điệu. Projection hỗ trợ `own`, `resource` và `branch` scope; nhánh được resolve động từ Organization owner bằng proof do Projection ký theo tenant đã xác thực, rồi áp `resource_branch_id IN (...)` server-side. Queue scope chưa có assignment data authoritative trong read model nên fail closed. UI Access chỉ cho cấu hình scope mà manifest của action khai báo; danh sách writable rỗng biểu thị action không cho sửa field. Projection bắt buộc cấu hình `authorization_branch_scope_resolver.base_url` trỏ tới Authz organization owner HTTPS, CA tin cậy, mTLS client identity, workload ID/key ID/private key và proof TTL bounded.

**Engine resource policy:** workflow start và các lệnh hoàn tất service/user/script task, multi-instance, trigger boundary truyền resource context `bpmp-engine / workflow_instance / <action>` vào local evaluator. Owner manifest v1.1 đăng ký từng action với capability riêng. Nếu signed bundle đã catalog một action, resource grants là authority cho action đó; deny không fallback sang transition grant cũ. Action chưa có trong bundle tiếp tục dùng transition evaluator để hỗ trợ rollout theo owner. Engine xác minh actor/workload proof và revoke floor trước khi tải state. Bundle được xác minh trước, ghi đồng bộ vào column family local `authorization_policy_bundles` của RocksDB rồi mới ACK/commit Kafka; khi khởi động Engine xác minh và cài lại bundle từ column family này trước khi mở command listener. Column family là state local theo node, không nằm trong replicated workflow/Raft state. Mỗi Engine node dùng consumer group có node ID riêng để Kafka phát mọi publication tới mọi evaluator; ACK consumer ID cũng định danh node để rollout không gộp các replica. `WorkflowStarted` ghi branch leaf, canonical ancestry và `organization_version`; Engine evolve chúng vào `InstanceState` và WorkflowSnapshot schema v3. Snapshot v0–v2 vẫn decode; dữ liệu cũ thiếu ancestry/revision không đủ điều kiện cho branch-scoped query filter. Authoritative command handler resolve current ancestry từ Authz trước resource evaluation; endpoint hoặc cấu hình resolver không sẵn sàng thì từ chối command có branch. Projection lưu branch ID/revision authoritative và resolve descendant branch IDs hiện tại ở query time để lọc SQL, nên có thể áp cây mới sau reparent mà không rewrite read model. Boundary/local/remote worker dispatcher resolve ancestry hiện hành trước khi Engine.handle; thất bại thì command bị từ chối, không fallback snapshot. Queue chưa được đưa vào resource context vì Engine chưa có queue identity từ nguồn assignment authoritative. Case lifecycle commands vẫn dùng transition evaluator do chưa có resource context/manifest thuộc owner của Case.

**Vị trí UI Access:** màn hình quản trị catalog/policy theo yêu cầu sản phẩm nằm trong `bpmp-prototype` → Admin Console → Access. Access trong prototype gọi API Gateway cho owner catalog, tenant role codes, draft/version, audit, queue, validate, publish và acknowledgement; gọi Organization owner để dựng branch picker từ toàn bộ cây hiện hành. Role code được lấy từ Authz theo tenant và Authz kiểm tra lại khi validate/publish; actor ID tách riêng, không suy loại principal theo chuỗi. Field đọc được chọn rõ theo từng grant; khi mở policy cũ có `readable_fields` rỗng (nghĩa là toàn bộ field owner cho phép), UI hiển thị toàn bộ field như đang được cấp và yêu cầu ít nhất một field nếu owner có field đọc. Field ghi để trống nghĩa là không cho sửa field nào. Rollout view đọc bundle sequence/expiry mới nhất của catalog version và phân biệt ACK đúng sequence hiện hành với ACK lịch sử; chưa thể kết luận đủ coverage nếu chưa đối chiếu deployment inventory. JWT tenant claim chỉ dùng để tạo request context ở UI; Gateway/Authz vẫn xác minh token và tenant. Token nhập qua kết nối Access chỉ được giữ trong `sessionStorage`, không lưu vào bundle build hoặc localStorage; dùng access token thời hạn ngắn và triển khai CSP/XSS controls phù hợp. Không hiển thị dữ liệu mẫu như policy đã publish. `cockpit-web` vẫn có một màn hình Access riêng; hai màn hình chưa dùng chung frontend/API client. Owner manifest registration vẫn chạy qua workload CLI, chưa có thao tác đăng ký manifest trong UI.

Cockpit Access hỗ trợ khởi tạo draft mới từ snapshot đang publish để người quản trị chỉnh sửa theo thay đổi tăng dần; snapshot/version đang chạy không bị sửa. Branch preview lấy cây Organization hiện hành, tính branch descendants và hiển thị revision của cây dùng để xem trước. Authz validate/publish kiểm tra branch scope IDs tồn tại, active và thuộc đúng tenant trong Organization store; cấu hình có branch đã xóa/vô hiệu hóa bị từ chối. Preview vẫn chỉ là UX aid, không phải PDP decision; Authz validation và owner evaluator là authority.

Access UI lấy assignment group IDs từ Human Runtime (distinct `candidate_group` trong assignment policies còn hoạt động) thay vì nhận ID tự nhập. Đây là nguồn tham chiếu cho queue/assignment-group scope; mỗi owner vẫn chỉ hỗ trợ và enforce khi manifest/action của chính owner khai báo scope `queue`.

Runtime evaluation contract đích tối thiểu (in-process/SDK, không bắt buộc gọi mạng Authz):

```text
request: request_id, subject_proof, tenant_id, action, resource_type,
         resource_ref/attributes, trusted_context, requested_fields
response: decision_id, allow|deny, reason_code, policy_epoch,
          valid_until, row_constraints, field_permissions,
          obligations, audit_required
```

- Tenant, actor proof và workload identity đến từ trusted context; mTLS/workload proof không thay actor proof.
- Evaluator chỉ nhận verified bundle và trusted actor/resource/context do owner cung cấp; tenant, policy version/epoch và bundle validity phải được kiểm tra.
- Bundle thiếu/sai tenant/action/resource, hết hạn, sai chữ ký/schema hoặc key không được trust → deny/fail closed.
- Reason code phục vụ audit/explain; không lộ policy internals cho caller không có quyền explain.
- Owner ghi decision reference/policy epoch vào audit phù hợp; không gửi toàn bộ dataset cho Authz để lọc.
- Bulk command authorize từng item; per-item outcome không được làm lộ item ngoài visibility.

Mọi query family phải giữ semantics tương đương: list, detail, search, count, aggregate, history, saved query, export, bulk, download và stream. Mỗi endpoint khai báo action, resource, owner, tenant source, row-filter, field set, export/cache/revoke behavior. Nếu owner không thể filter an toàn thì deny hoặc đổi thiết kế; không bỏ filter lặng lẽ.

## 6. Identity, session và trusted context

- JWT/session xác minh subject và tenant memberships; tenant header chỉ được chấp nhận nếu nằm trong authenticated scope.
- UI không được khẳng định `actorId`, `role`, organization, assignee, audit actor hoặc policy epoch trong body.
- BFF chuyển actor proof tới owner. Workload identity xác định service caller, không chứng minh user đã được authorize.
- Tenant switch kiểm tra membership/policy mới và invalidate/refetch browser/server cache theo tenant key.
- User bị khóa/deprovision phải kích hoạt revoke; token chưa hết hạn không chứng minh policy vẫn còn hiệu lực.

## 7. Audit và explainability

Evidence cho policy/grant admin và quyết định nhạy cảm gồm actor, workload, tenant, action, resource reference, decision/reason code, policy epoch, scope summary, command/correlation ID, timestamp và outcome. Ghi tại owner boundary; append-only theo retention. Chỉ lưu digest/selected fields khi đủ, tránh raw PII/secret.

`explain` chỉ cho persona được phép, nêu matched policy/version, grant source, scope path, deny reason, missing attributes và enforcement owner; không tiết lộ subject/resource ngoài quyền người xem. Explain hỗ trợ điều tra, không thay decision runtime.

Mandatory audit không thể bị tắt qua permission setting. Policy publish/revoke, role/direct grant, tenant admin, service principal và privileged export luôn cần audit.

## 8. Publish, cache và revocation

### Lifecycle

Policy draft → schema/semantic validation → approval khi action yêu cầu → immutable publish/version/epoch → distribute → consumer acknowledgement/health → revoke hoặc supersede. Chỉ version publish và consumer-supported mới được dùng. Lát triển khai catalog dùng `draft_revision` CAS khi cập nhật draft; cập nhật conflict phải được tải lại trước khi tiếp tục. Validate phát hiện action/resource không owner, scope sai, attribute không có authoritative source, conflict/unreachable rule, expression vượt quota hoặc consumer không tương thích. Không publish best-effort cho consumer bắt buộc.

### Cache và revoke

- Cache key tối thiểu gồm tenant, subject, action, resource/scope fingerprint, policy epoch và attribute version cần thiết; không dùng chung allow cache cho mọi resource.
- Decision có `valid_until`; cache không được sống quá validity hoặc epoch revoke.
- Signed revoke feed hiện có là distribution path; consumer ghi nhận applied epoch, lag, reconnect và gap. Gap/unknown epoch chặn decision phụ thuộc policy mới đến khi recovery.
- **Write/transition, policy/admin mutation, export/download và secret/restricted data:** decision còn hiệu lực tại commit/serve boundary; không stale-allow sau khi nhận revoke.
- **Ordinary read:** cache ngắn hạn chỉ khi security/domain owner phê duyệt maximum-staleness budget đo được. Prototype chưa có giá trị thời gian; phải chốt trước rollout, không để mỗi service tự chọn.
- Authz unavailable, feed stale quá budget hoặc decision expired → fail closed cho protected data/command; trả dependency error khi không thể ra business decision.
- Form đang mở không giữ quyền; reauthorize khi submit. Job/export revalidate lúc status/result/download; revoke có thể làm artifact không còn tải được theo classification/policy.
- Invalidate server cache, cursor/query authorization context, signed links và session entitlements qua owner adapters. Browser cache chỉ là UX cache.

Trước production, lập budget/recovery theo consumer: max policy age, revoke objective, alert threshold, fail mode, snapshot/replay và evidence. Không tuyên bố “immediate revoke” nếu chưa đo.

## 9. Mapping vào logical data model

| Logical entity hiện có | Semantics cần áp dụng |
|---|---|
| `subjects`, `memberships`, `organizations` | Identity/directory và tenant membership; không sở hữu credential/MFA. Relationship có version/effective period nếu policy cần lịch sử. |
| `roles`, `permissions`, `role_permissions` | Role chứa action code typed; catalog có owner/resource/audit class/lifecycle. |
| `role_bindings` | Role + subject + tenant + typed scope + validity/status/CAS/audit. |
| `direct_grants` | Subject/action/scope/effect/reason/expiry; deny precedence và grant class được kiểm soát. |
| `policy_versions` | Draft/published immutable document với schema version, digest, validator compatibility, status/epoch. |
| `policy_bindings` | Binding version vào resource type/scope; unique/conflict validation lúc publish. |
| `audit_evidence` | Evidence gốc tại authority owner; search index là projection. |
| `outbox`, `inbox` | Reliable publication/revocation và dedup; applied epoch/checkpoint thuộc consumer. |

Không lưu permission/rule JSON song song trên role. Versioned policy document có schema được phép cho typed expressions, không thay bảng grant/binding. FK/unique nội owner ghép tenant; external refs không phải cross-database FK. Row policy cần owner-specific query/index mapping. Chưa thêm physical columns/migration chỉ từ logical model.

Dynamic catalog cần logical records cho owner manifests, catalog entries, catalog versions, config bindings, published snapshots và consumer acknowledgements. Authz Control Plane là authority và lưu catalog lifecycle/version; Configuration Service chung không sở hữu action/resource catalog hay policy authorization. Consumer acknowledgement/lag records cần được hoàn thiện để UI có thể xác nhận rollout thực tế. Không nhét capability manifest vào một JSON profile tenant như nguồn duy nhất; giữ manifest provenance/version riêng.

## 10. Projection, reporting và exports

- Projection/Report là data copy, không phải authorization authority. Authz là source policy/revoke.
- Consumer/report theo dõi policy epoch hoặc contract tương đương; không dùng role dimension eventual trong analytics để quyết định access.
- Áp row policy trước aggregate để không tiết lộ record ngoài scope; field policy giới hạn dimensions/measures/filter/group-by.
- Export lưu query/policy epoch, requester, scope digest, source cut, expiry/classification. Download revalidate actor/current policy; job cũ không tự đổi scope theo quyền người khác.
- Saved query không mang quyền creator; mỗi lần execute áp policy actor hiện tại.
- Reporting policy parity là rollout gate; không đọc trực tiếp DB owner.

## 11. Failure behavior

| Tình huống | Kết quả |
|---|---|
| Subject/tenant proof thiếu/sai | Deny; không query ngoài tenant |
| Không allow, explicit deny, action unknown | Deny; audit theo risk class |
| Bundle/evaluator unavailable, invalid hoặc hết hạn | Fail closed; không fallback allow; phát dependency/security telemetry |
| Filter thiếu/sai version/không compile | Deny query; không bỏ điều kiện |
| Resource không tồn tại/không visible | Có thể trả 404 theo API contract để tránh ID enumeration |
| Revoke feed gap/quá staleness budget | Ngừng decision bị ảnh hưởng đến khi snapshot/feed recovery |
| Owner attribute thiếu/không tin cậy | Deny policy cần thuộc tính đó; không dùng giá trị client thay thế |
| Partial bulk authorization | Per-item result; không lộ item ngoài visibility |
| Projection/report lag | Trả freshness/asOf đúng; không nới quyền để bù lag |

## 12. Test matrix và acceptance gates

Test theo owner/action/resource family:

1. Cross-tenant, forged tenant/actor/role, workload-only call, expired session.
2. Role allow, direct allow, explicit deny conflict, multi-allow union, mandatory tenant intersection.
3. Organization/branch/queue/resource scope, inheritance, expiry, cycle/cross-tenant relationship.
4. Branch parent → descendants allow; child → parent/sibling deny; multi-level tree move/reparent race; queue ∩ branch semantics; missing/mismatched row branch assignment.
5. List/detail/search/count/aggregate/sort/filter/group-by/history/saved query/export/download/bulk/stream.
6. Field read/write/mask/copy/export; không suy luận hidden data qua aggregate/error.
7. Publish race, stale version, conflict, unknown action/attribute, malformed expression, revoke giữa form và submit.
8. Authz outage, expiry, revoke duplicate/out-of-order/gap, restart, snapshot recovery, clock skew, epoch mismatch.
9. Bypass paths: direct route, internal RPC, scheduled job, worker, retry, receipt lookup, admin endpoint.

Release gates:

- Owner ký nhận action/resource catalog; mọi production route/command map tới action, unknown action không bypass.
- Cùng semantics được chứng minh trên ít nhất một command owner và một query owner; tenant/row/field negative tests pass.
- Report/export chứng minh authorization parity và revoke behavior trước canary.
- Revoke/stale-read budget được phê duyệt, đo, có dashboard/alert/runbook.
- Audit completeness, explain, last-admin, backup/recovery và policy rollback có evidence.
- Không thêm migration/public route chỉ vì tài liệu; review contract theo owner trước.

## 13. Quyết định cần domain/security review

- Action/resource catalog toàn platform và field/PII taxonomy.
- Organization/branch/queue graph, temporal membership và phân quyền customer/officer.
- DSL/schema typed ABAC và row constraints; compile/cost limits.
- Maximum policy staleness/revocation objective theo data class/workload.
- Quyền cần approval hai người/step-up authentication; retention audit.
- Authz HTTP/gRPC contract, signature/key rotation, consumer protocol/compatibility.
- RLS strategy, owner indexes/query profiles, tenant cardinality/performance budgets.

Các mục này là gate trước mở rộng policy surface, không ngăn vertical slice có phạm vi rõ.

## 14. UI Access lấy từ `bpmp-prototype`

Nguồn interaction là `bpmp-prototype/src/components/identity/IdentityModule.tsx`, `task-management/TaskManagementModule.tsx` và `task-management/AccessAuditLog.tsx`. Chuyển các use case sang UI BPMP thật, không bê nguyên mock arrays/toast/local mutations hoặc vai trò, từ ngữ và quy trình đặc thù NCLOS/ngân hàng thành mặc định platform.

| Prototype area | UI target | Contract/behavior bắt buộc |
|---|---|---|
| Identity → Roles | Danh sách/chi tiết role, create/edit draft, grants và assignment | Role metadata và permission bindings tải từ Authz; role inheritance nếu bật phải thể hiện rõ; mutation versioned, có validate/publish và không lưu local-only. |
| Identity → Permissions | Permission/action catalog | Đọc dynamic catalog từ Authz; hiển thị resource/action, owner service, mô tả, audit/risk class, supported scopes và manifest version. Catalog capability do service đăng ký là read-only với tenant admin; họ có thể bật/tắt trong config cho phép, không tự tạo action chưa được code enforce. |
| Identity → Users | Membership và role assignment | User/directory data lấy từ Identity owner; gán role scope bằng branch tree, expiry/reason; bulk action bounded, item-level outcome; lock/deprovision đi qua IdP adapter và revoke session. |
| Task Management → Process/Element | Policy binding theo capability/resource cụ thể | Dùng resource/action catalog động, không hardcode toggle set trong frontend; lưu draft, validate, show impact và publish version. Quyền runtime transition vẫn được Engine đánh giá. |
| Task Management → Queue | Queue policy/assignment relation | Queue do Human Runtime sở hữu; UI tham chiếu queue qua contract, không copy queue aggregate vào Authz. Khi action yêu cầu branch và queue, hiển thị phạm vi AND và preview hậu duệ được áp dụng. |
| Access Audit | Audit search/export | Query owner trả dữ liệu đã row-authorize; filter/search server-side, export có quyền riêng, expiry và revalidation; CSV formula sanitization. |
| Resources/service accounts | Workload identity registry/lifecycle | Tách khỏi user role UI khi owner/API khác; secret/private key không bao giờ hiển thị/export. |

Luồng thao tác quyền phải là **edit draft → validate (kèm lỗi theo field/rule) → xem effective-access/impact preview → publish version (và approval nếu cần) → theo dõi bundle/consumer acknowledgement → revoke/rollback**. Toggle trong prototype chỉ là input UX cho draft, không có nghĩa policy lập tức có hiệu lực. Sau publish, UI hiển thị `policy_version`, bundle sequence/epoch và trạng thái consumer sync; nếu owner chưa acknowledge thì không báo đã áp dụng.

Branch scope selector hiển thị cây parent-child; khi chọn branch cha, UI ghi rõ “bao gồm branch này và descendants” và cho xem danh sách/tổng phạm vi hiệu lực. Child không được preview như có quyền trên parent/sibling. Queue filter là chiều độc lập; preview phải mô tả giao branch-subtree ∩ queue set. Các filter rule dùng schema/field/operator do owner manifest hỗ trợ, không nhập arbitrary expression/SQL.

UI phải có loading/empty/403/409/validation/dependency-unavailable/partial-bulk/publish-pending states; giữ form draft sau lỗi có thể sửa, nhưng không giữ quyết định quyền cũ. Sau tenant/branch context change phải clear selection/cached results và refetch.

## 15. Căn cứ

- [API Specification](api-spec.md), [Architecture](architecture.md), [Data model](data-model.md), [Delivery plan](delivery-plan.md).
- [NIST SP 800-162, ABAC](https://csrc.nist.gov/pubs/sp/800/162/upd2/final).
- [OWASP Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html).
- [OWASP Authorization Patterns](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Patterns_Cheat_Sheet.html).
- [OWASP Authorization Decisions and Output Handling](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Decisions_and_Output_Handling_Cheat_Sheet.html).
