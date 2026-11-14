# Phân tích chức năng prototype

## Phương pháp

Bằng chứng là source `bpmp-prototype/src/`, type definitions, mock data và handler; inventory ghi từng control/event. Các API, capability và event dưới đây là **đề xuất**, không khẳng định endpoint hiện hữu. Phân biệt UI interaction (mở modal, chọn dòng), query (lọc dữ liệu), command (thay đổi nghiệp vụ). Mỗi command phải có owner, audit và kết quả bền vững. Capability là tên logic; cần map vào typed policy/action hiện có, không đưa string tùy ý vào Engine.

## Ma trận chức năng → backend

| Nhóm / source | Button, event, input | Hiện tại / khoảng trống | Contract đích; dữ liệu; kiểm tra |
|---|---|---|---|
| App / Header / Sidebar | Chọn module/workspace, collapse, dark mode | React state | Shell route và preference; deep link/back/forward; không gọi business command |
| Header | Đổi tenant | Đổi tên hiển thị | Session/BFF xác thực membership; hủy request cũ, xóa cache/selection/SSE của tenant trước; cấm dữ liệu cũ xuất hiện |
| Portal: persona | Chọn client/officer | Đổi persona cục bộ | Chỉ fixture dev; production lấy actor từ session, không cho client tự chọn vai trò |
| ModelerModule | Chọn process, palette add, append node, nối edge, drag, pan, zoom | Sửa graph state | Authoring draft với optimistic revision; local editor history; save qua `model.edit` |
| ModelerPropertiesPanel | Assignee/candidates, SLA, mapping, timer/message/error, multi-instance, listener | Sửa thuộc tính node | Validate schema và compiler capability; chặn unsupported engine extension; node reference thuộc đúng version |
| Modeler | Xóa node, save/check-in, xem/export XML | Chủ yếu state hoặc browser export | Xóa edge tham chiếu trong draft; check-in tạo immutable version; XML escape/validation; dirty navigation guard |
| App.handleDeployToEngine | Deploy Camunda/Flowable | Toast + tăng version | Deployment job, compiler validation và signed WIR; `model.deploy`; chỉ thành công khi receipt xác nhận. Engine adapter ngoài BPMP là gate riêng |
| DmnTableModule | Add/duplicate/delete rule, sửa cell, thêm input/output, đổi hit policy | State, validator/evaluation cục bộ | Decision draft + clauses/rules/cells; reorder bằng ordinal, stable IDs; validate trước publish |
| DmnVisualLogicEditor | Edit/create/save/duplicate/delete rule | Modal sửa cùng decision | Dùng cùng domain reducer/validator với table, không giữ bản copy độc lập |
| DMN history/compare/inspect | Snapshot, compare, revert, chọn version, copy/export | Mock snapshots + state | Immutable snapshot; compare pure; revert tạo draft mới từ version cũ, không sửa lịch sử |
| DMN evaluation | Run evaluation, inject/remove conflict mẫu | Simulator/validator trong browser | Sandbox evaluation có quota/time limit, version/hash cụ thể; demo conflict chỉ dev; parity FEEL cần conformance suite |
| OperationModule | Search/filter, select rows/all, diagram highlight | Filter local arrays | Authorized paginated projection; selected IDs/version ổn định; select-all mặc định chỉ trang hiện tại |
| Operation | Cancel selected | Đổi trạng thái local | Batch các `instance.cancel`, từng item có expectedVersion/receipt; `instance.cancel` capability |
| Operation | Migrate instances | Chỉ thay version | Dry-run mapping token/variables + compatibility, approval khi policy yêu cầu; Engine mới được commit migration |
| Operation | Move tokens | Đổi active node/current step | Dry-run transition plan; chỉ token sống hợp lệ, không chỉnh projection; audit lý do, state version và approval |
| TasklistModule | My/group/completed tabs, sort, priority/search, chọn task | Mock list/filter/sort | Human Runtime/query policy + cursor; URL lưu filter; không lọc quyền bằng browser |
| Tasklist | Claim/unclaim | Gán assignee local | Engine-authorized command qua Human/BFF; version conflict khi hai actor claim, không retry mới tự động |
| Tasklist | Approve/reject/sendback, form inputs | Hoàn tất task và đổi instance trong App | `task.complete` + typed outcome/form revision; Engine xác định next step, không mặc định approve = instance complete |
| Tasklist | Bulk approve/reject | Array update và loop callback | Bounded batch, item-level authorization/result/retry; không toast “tất cả thành công” khi partial failure |
| Task Mgmt | Process/element/queue permission, role action toggles, filters, drawer save | Local RBAC structures | Authz policy draft + validate/publish/revoke; `policy.edit/publish`; granular actions có typed catalog |
| Task Mgmt | Create queue | Thêm local RBACItem | Human queue aggregate; role grants là Authz refs, không copy policy authority vào Human |
| AccessAuditLog | Filters, export CSV | Mock audit/export browser | Audit Search query/export; row-level authorization; sanitize CSV formula; async export có expiry |
| Identity: users | Create, select/invert/active/locked, bulk lock/unlock/delete | State | IdP/directory lifecycle orchestration; preflight impacted ownership, soft-delete trước; revoke session/quyền |
| Identity: users | Bulk role, department, direct permission, security | State/toast | Membership/Authz publication; security settings qua IdP adapter. Không tự tạo credential tại UI |
| Identity: roles | Grant/revoke, change type, assignments, delete, export | State | Typed role binding/policy command; chặn xóa role còn tham chiếu, không xóa quyền cuối của tenant admin |
| Identity: permissions | Assign/revoke role/user, audit logging toggle | State | Permission catalog + policy; bắt buộc audit cho privileged changes, không cho tắt evidence nghiệp vụ |
| Identity: resources | Status, ping, export | State mô phỏng | Integration health job + authoritative resource registry; không đánh dấu ONLINE trước probe |
| Identity: service accounts | Rotate/renew cert, revoke, export | Mock certificate state | Workload identity/KMS integration; renewal job, overlap/revoke proof; không export private key |
| FoundationModule | Domain preset, step names/count, target engine, generate/open modeler | Dựng graph mẫu | Template immutable version → authoring draft; không auto deploy; không giả định template hợp lệ compiler |
| Foundation allocation | Strategy/group/limit/escalation/active | Mock policies | Human assignment policy version; round-robin fairness và concurrent limit trong owner transaction |
| Foundation notification/doc tabs | Cấu hình và controls ở UI | Chưa có delivery/storage lifecycle bền vững | Configuration + Notification/Document owners, không tạo Common Service sở hữu tất cả |
| EndpointsModal | Create/edit/delete, method/auth/URL, active | React endpoint array | Integration registry + secret reference; validate destination/egress, probe job, policy audit; không trả secret |
| OptimizeModule | Filters, heatmap, metric/diagram selection | Mock aggregates | Projection/analytics query scoped tenant; định nghĩa wait/process duration và freshness |
| ProcessThroughputDashboard | Refresh, filters, export | Timer/mock export | Query snapshot với watermark; export async; refresh không giả thành live telemetry |
| ProcessHealthReport | Retry incident, CSV | Local retry/status simulation | Engine/worker retry command, incident version/lease, max attempts; event outcome mới đóng incident |
| ClientPortalModule | Service select, wizard steps, process/business key, custom variables | State + mock schema | Service offering version/form definition; server validate reserved variables, typed values; business key unique theo scope |
| Portal submit | Gửi yêu cầu/tạo instance | Tạo cả request/instance local bằng Date.now | Case submit + start saga; receipt Pending, reconcile không tạo instance trùng |
| Portal documents | Upload/add supplementary docs, file/category/notes | Metadata giả, có document được set VERIFIED ngay | Real upload session, object checksum/scan; verify riêng; chỉ attach CLEAN document; pending không xem là đã nhận hợp lệ |
| Portal officer | Approve/reject/request docs/reason | Đổi request status và steps theo index | Cùng task command của Task MFE; Case status projection từ workflow outcome; không có đường duyệt thứ hai |
| Portal comments | Send message | Thêm comment local | Case comment với visibility INTERNAL/EXTERNAL; server quyết định actor; internal note không leak cho client |
| Portal lookup | Tracking code, instance ID, tax ID | Search toàn bộ mock requests | Query theo entitlement; tracking code không phải credential; chống enumeration, giới hạn dữ liệu trả về |
| EmbeddedProcessViewer | Node select, tabs, zoom, variables/JSON copy | Diagram và mock variables | Read-only diagram package; masked variable projection; quyền riêng cho export/copy payload |
| PortalMetricsDashboard | Filter/refresh, chart drill-down | Mock chart/timer | Analytics scoped requester/branch; aggregate không vượt visibility của actor |

## State và tính nhất quán bắt buộc

UI command: `editing → validating → submitting → accepted → reconciling → confirmed`. Nhánh lỗi: validation, forbidden, conflict, retryable transport, terminal failure. `accepted` không đồng nghĩa projection đã cập nhật. Giữ nguyên command ID/idempotency key và payload cho retry cùng intent; đổi payload là intent mới. Conflict phải refetch và yêu cầu xem lại, không tự ghi đè.

Case: `DRAFT → START_PENDING → ACTIVE → CLOSED`; lỗi start đã xác định có thể chuyển `START_FAILED`, kết quả chưa rõ ở `RECONCILING`. Trạng thái nghiệp vụ (SUBMITTED, IN_REVIEW, ADDITIONAL_DOCS_REQUIRED, APPROVED, DISBURSED, REJECTED) tách khỏi orchestration state. APPROVED không tự suy ra DISBURSED. Task `UNASSIGNED/ASSIGNED/COMPLETED` không thay thế runtime token state.

## Nội dung cần review từng control trước implementation

Dùng CSV tạo ticket có: file/dòng/hash; nhãn hoặc accessible name; persona/capability; precondition; input/schema; event và propagation; query/command; owner; loading/disabled; validation; success receipt; empty/403/409/timeout; audit; test ID. Những dòng chưa map vào use case ở bảng trên phải được review, không mặc nhiên coi đã bao phủ.

Các control icon-only, event `onMouse*`, `onKey*`, `onChange`, file input, form submit và callback props đều có trong inventory, nhưng scanner không chứng minh keyboard parity hoặc runtime reachability. Cần walkthrough từng tab/modal với client, officer, designer, operator và admin, ghi video/screenshot hoặc test evidence; hiện chưa thực hiện bước này.
