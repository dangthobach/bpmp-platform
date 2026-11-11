# Phân quyền và truy cập dữ liệu

**Trạng thái: đặc tả thiết kế đề xuất để review; chưa xác nhận behavior đã triển khai.** Tài liệu này chuẩn hóa cách dùng Authz hiện có trong prototype design. Không tạo authority RBAC/policy thứ hai, không thay quyền chuyển trạng thái của BPMP Engine và không phải migration vật lý.

Đặc tả này phục vụ định hướng BPMP đa lĩnh vực: các cơ chế authorization được tái sử dụng; action/resource, role binding, scope và policy có thể cấu hình theo tenant/lĩnh vực. Khác biệt được cấu hình trong schema/grammar được hỗ trợ; không fork implementation theo khách hàng. Quy tắc bảo mật nền tảng (tenant isolation, deny-by-default, owner enforcement, Engine transition authority) không phải tenant override.

## 1. Mục tiêu và nguyên tắc

Mục tiêu là để mọi service có cùng kết quả khi quyết định một chủ thể được thực hiện hành động nào trên tài nguyên nào, trong phạm vi dữ liệu nào, và được nhận những trường nào.

1. **Deny by default:** không có allow rõ ràng, policy không hợp lệ/không sẵn sàng, hoặc thiếu thuộc tính cần thiết thì từ chối. Timeout, missing filter hay kết quả không đầy đủ không bao giờ thành allow.
2. **Tenant isolation là ranh giới bắt buộc:** tenant lấy từ authenticated context đã xác minh, không từ body/query tự khai báo. Mọi row read/write bị giới hạn tenant trước policy nghiệp vụ.
3. **RBAC + ABAC:** RBAC cấp candidate action qua role; ABAC ràng buộc candidate theo subject/resource/action/context như organization, branch, queue, classification, owner, state hoặc purpose.
4. **Tách quyền thao tác và quyền dữ liệu:** quyền gọi action không đồng nghĩa quyền đọc mọi row/field; quyền đọc không đồng nghĩa quyền sửa hoặc transition.
5. **Policy decision tập trung, enforcement tại owner:** Authz Control Plane là policy authority/PDP; service sở hữu resource là PEP và bắt buộc áp decision vào query/command. BFF/UI không phải security boundary cuối.
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
| PDP | Policy Decision Point ở Authz: đánh giá subject/action/resource/context và trả decision có version. |
| PEP | Policy Enforcement Point trong service owner: xác minh decision rồi áp action/row/field constraint. |
| PIP | Nguồn thuộc tính authoritative: IdP/directory cho subject, owner service cho resource, trusted request context cho request. |
| Data constraint | Ràng buộc có kiểu: row predicate, field operation/masking hoặc giới hạn hành động. |

Authority: external IdP sở hữu credential/MFA; Authz sở hữu permission catalog, roles, bindings, grants, policy publication/revocation; business service sở hữu resource attributes, query execution và enforcement; Engine sở hữu workflow state/transition; audit evidence gốc ở owner, Audit Search chỉ là projection.

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

- **Authz giữ canonical catalog và policy semantics.** Configuration Service có thể cung cấp lifecycle/version/scope/publication của config theo contract hiện có; nó không tự trở thành authority về quyền.
- Authz chỉ publish config tham chiếu manifest đã đăng ký và owner đã acknowledge. Entry thiếu owner, manifest, field/query enforcement hoặc consumer compatibility bị reject.
- Service phải fail closed với action không có trong manifest/snapshot hỗ trợ. Config không thể ép một service “enforce” quyền chỉ bằng cách thêm tên action.
- Dynamic config dùng draft → validate → publish immutable version → rollout/acknowledge → rollback/retire; CAS theo base version và audit actor/reason.
- Entry/action mới hoặc đổi owner cần software capability release trước, sau đó mới publish cấu hình bật. Deactivate entry chỉ ngăn cấp quyền mới; revoke binding/policy/session theo luồng revocation riêng.
- Platform/environment/tenant overrides chỉ được phép theo precedence schema đã định nghĩa; tenant không thể override platform security invariants hoặc bật action bị platform disable.
- Tái sử dụng bằng catalog/action semantics và policy primitives có contract chung; không sao chép policy evaluator/query filter vào từng lĩnh vực. Service-specific adapter chỉ chứa mapping tới resource attributes/query shape của owner, không nhân bản quyết định precedence.
- Cấu hình ngành/tenant có thể thêm role, binding, scope values và policy instances dựa trên action/resource/schema đã đăng ký. Thêm một action/resource kind mới cần manifest + owner enforcement release trước khi config có thể bật.
- Tránh “configuration language” tổng quát vô hạn: chỉ hỗ trợ typed operators, bounded expressions, supported attribute sources và explicit version. Unsupported requirement dẫn tới mở rộng capability có review, không nhúng script tùy ý vào policy.

**Bằng chứng hiện tại và giới hạn:** repo đã có Configuration Service với profile/version/publish/rollback và resolved snapshots. `contracts/proto/bpmp/configuration/v1/configuration.proto` định nghĩa `AuthzControlPlanePolicy`, nhưng các trường hiện có là cấu hình vận hành (timeout, pool, graph memo, inactive-user job), chưa phải dynamic action/resource catalog hay policy registry. Vì vậy việc đặt catalog vào dynamic configuration cần contract/schema integration riêng và review owner; không được coi là capability hiện có chỉ vì đã có Configuration Service.

### 3.3 Scope, inheritance và grants

- Scope type là enum/catalog có semantics, không phải chuỗi tùy ý mỗi service diễn giải riêng.
- Organization/branch/queue inheritance chỉ tồn tại khi relationship graph và hướng kế thừa được khai báo rõ. Chỉ đi theo cạnh đã xác minh, trong cùng tenant; reject cycle và cross-tenant relation.
- Resource binding có thể thu hẹp nhưng không mở rộng qua tenant boundary.
- Role binding/direct grant có validity window, actor, reason và CAS/version. Grant tạm thời phải có expiry.
- Direct grant là ngoại lệ có kiểm soát, không phải bypass policy; explicit deny chỉ dùng qua grant class được phê duyệt.
- Revoke giữ lịch sử/audit; không âm thầm xóa bằng chứng khi role/grant bị gỡ.

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

Authz routes hiện được inventory hóa gồm `check`, `filter`, `query`, `query-revoke-feed`, `explain`. Schema/semantics/version/authentication cần được xác nhận từ implementation và DTO/Protobuf trước khi coi là contract ổn định.

Decision contract đích tối thiểu:

```text
request: request_id, subject_proof, tenant_id, action, resource_type,
         resource_ref/attributes, trusted_context, requested_fields
response: decision_id, allow|deny, reason_code, policy_epoch,
          valid_until, row_constraints, field_permissions,
          obligations, audit_required
```

- Tenant, actor proof và workload identity đến từ trusted context; mTLS/workload proof không thay actor proof.
- PDP response phải xác thực nguồn, gắn request/tenant/action/resource, có policy epoch/expiry; không dùng lại cho request khác.
- Decision thiếu/sai tenant/action/resource, hết hạn, sai chữ ký/schema hoặc không xác minh được → deny.
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

Policy draft → schema/semantic validation → approval khi action yêu cầu → immutable publish/version/epoch → distribute → consumer acknowledgement/health → revoke hoặc supersede. Chỉ version publish và consumer-supported mới được dùng. Update dùng CAS theo base version. Validate phát hiện action/resource không owner, scope sai, attribute không có authoritative source, conflict/unreachable rule, expression vượt quota hoặc consumer không tương thích. Không publish best-effort cho consumer bắt buộc.

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

Dynamic catalog cần logical records cho owner manifests, catalog entries, config bindings, published catalog snapshots và consumer acknowledgements. Đây là **yêu cầu mô hình cần refine**, chưa có bảng trong schema hiện tại. Không nhét capability manifest vào một JSON profile tenant như nguồn duy nhất; giữ manifest provenance/version riêng, còn Config Service quản lý snapshot lifecycle theo contract đã mở rộng.

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
| PDP timeout/unavailable | Fail closed; dependency error, không fallback allow |
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
4. List/detail/search/count/aggregate/sort/filter/group-by/history/saved query/export/download/bulk/stream.
5. Field read/write/mask/copy/export; không suy luận hidden data qua aggregate/error.
6. Publish race, stale version, conflict, unknown action/attribute, malformed expression, revoke giữa form và submit.
7. Authz outage, expiry, revoke duplicate/out-of-order/gap, restart, snapshot recovery, clock skew, epoch mismatch.
8. Bypass paths: direct route, internal RPC, scheduled job, worker, retry, receipt lookup, admin endpoint.

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

## 14. Căn cứ

- [API Specification](api-spec.md), [Architecture](architecture.md), [Data model](data-model.md), [Delivery plan](delivery-plan.md).
- [NIST SP 800-162, ABAC](https://csrc.nist.gov/pubs/sp/800/162/upd2/final).
- [OWASP Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html).
- [OWASP Authorization Patterns](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Patterns_Cheat_Sheet.html).
- [OWASP Authorization Decisions and Output Handling](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Decisions_and_Output_Handling_Cheat_Sheet.html).
