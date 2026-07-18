# Requirements Document

## Introduction

Tài liệu này mô tả yêu cầu cho **BPMP Platform** — một nền tảng BPM (Business Process Management) thế hệ mới với deterministic core viết bằng Rust và các microservice ngoại vi có thể dùng Rust hoặc Go theo bounded context, kết hợp trải nghiệm trực quan "BA-first" của Camunda với năng lực thực thi bền vững, hướng sự kiện (durable, event-driven) của Temporal.

Định vị sản phẩm: **"AI-Native Event-Driven BPM Platform"** dành cho doanh nghiệp trong lĩnh vực được quản lý chặt (regulated enterprises) như ngân hàng, bảo hiểm, logistics. Nền tảng tuân thủ chuẩn OMG (BPMN 2.0, CMMN, DMN), đạt throughput cỡ Temporal/Zeebe, hỗ trợ event sourcing/audit nguyên bản, tích hợp AI process intelligence, runtime Rust nhẹ, triển khai được cả cloud-native lẫn on-prem.

Đây là tài liệu yêu cầu **toàn cảnh (full platform vision)** — bao phủ rộng và ở mức cao (high-level) toàn bộ các nhóm năng lực chính. Một phần **MVP đề xuất cho Phase 1** được nêu ở cuối tài liệu để định hướng ưu tiên triển khai, nhưng toàn bộ user story vẫn được ghi nhận đầy đủ.

> **Lưu ý ngôn ngữ:** Tài liệu viết bằng tiếng Việt; các thuật ngữ kỹ thuật (BPMN, WIR, saga, backpressure, ...) được giữ nguyên tiếng Anh để tránh sai lệch nghĩa.

## Glossary

- **BPM_Platform**: Toàn bộ hệ thống BPMP Platform; deterministic workflow core dùng Rust, các deployable service ngoài core có thể dùng Rust hoặc Go nhưng không được tái hiện thực WIR interpreter/`decide()`/`evolve()`.
- **BPMN_Compiler**: Thành phần CLI/compiler biên dịch trước (AOT) các file `.bpmn`, `.dmn`, `.cmmn` thành WIR.
- **WIR (Workflow Intermediate Representation)**: Biểu diễn trung gian đã tối ưu của workflow, tương đương state machine Rust, được sinh ra ở compile-time (không parse XML lúc runtime).
- **Human_Runtime**: Runtime phục vụ tác vụ con người (User Task, Approval, SLA, Escalation, CMMN Case), backend PostgreSQL, mục tiêu governance.
- **Stream_Runtime**: Runtime orchestrator hướng sự kiện (Service Task, Message Event, Timer, Retry, Compensation, Saga), backend append-only event log, mục tiêu throughput.
- **Event_Store**: Kho lưu event log append-only, immutable, hỗ trợ snapshot định kỳ; production adapter mặc định dùng RocksDB, adapter khác chỉ được chấp nhận sau durability/migration/benchmark gates tương đương.
- **Snapshot**: Ảnh chụp trạng thái workflow instance sau mỗi N event để tăng tốc replay.
- **Worker**: Thành phần thực thi task. Gồm **WASM_Worker** (chạy in-process qua wasmtime) và **Remote_Worker** (gRPC qua tonic).
- **Dispatcher**: Thành phần phân phối task tới worker qua gRPC bidirectional streaming với credit-based flow control.
- **Cockpit**: Giao diện giám sát thời gian thực (SSE/WebSocket) cho BA và quản lý.
- **Time_Travel_Cockpit**: Chế độ Cockpit cho phép tua lại lịch sử instance bằng timeline slider.
- **Process_Copilot**: Lớp AI cung cấp dự đoán SLA, gợi ý route, sinh BPMN từ ngôn ngữ tự nhiên, và mining nút thắt cổ chai.
- **Identity_Provider**: Hệ thống định danh (Keycloak) cung cấp JWT và claim/role.
- **Cluster_Node**: Một node trong cụm HA, tham gia consensus Raft (openraft).
- **BA_View**: Góc nhìn nghiệp vụ của workflow (Progressive BPMN).
- **Engineer_View**: Góc nhìn kỹ thuật của cùng workflow (retry, compensation, saga, ...).
- **Correlation_ID**: Định danh tương quan xuyên suốt một luồng xử lý để phục vụ tracing/logging.
- **Idempotency_Key**: Khóa đảm bảo một thao tác chỉ có hiệu lực một lần dù nhận nhiều lần.
- **TenantId**: Định danh tenant dùng để cô lập dữ liệu, quyền, quota/rate-limit, event stream và read model trong môi trường multi-tenant.
- **Data_Subject**: Chủ thể dữ liệu cá nhân/nhạy cảm, dùng cho retention, masking, export và crypto-shredding.
- **Crypto-shredding**: Cơ chế xóa khả năng đọc dữ liệu bằng cách hủy khóa mã hóa theo subject/tenant/key-scope thay vì sửa/xóa event immutable.
- **Terminal_Instance**: Instance không còn bất kỳ đường thực thi tiếp nào, gồm dispatch, timer, retry, dead-letter replay hoặc migration; trạng thái `Failed` chỉ được coi là terminal khi policy không cho phép resume/replay.
- **Terminated_For_Compliance**: Trạng thái terminal tường minh được ghi bền vững trước khi crypto-shredding một instance đang chạy theo nghĩa vụ pháp lý khẩn cấp.
- **Compensation_Ledger**: Sổ theo dõi phi-PII cho side-effect ngoài hệ thống và compensation tương ứng, gồm loại side-effect, hệ thống đích, opaque operation reference, handler và trạng thái; được mã hóa bằng key scope vận hành tách khỏi key của Data_Subject.
- **Reconciliation_Work_Item**: Work item vận hành phi-PII được tạo trước emergency erasure để theo dõi các side-effect chưa compensation xong cho tới khi được đối soát hoặc xử lý thủ công.
- **SLA**: Service Level Agreement — cam kết thời hạn xử lý của một task/case.
- **WorkflowVersion**: Định danh phiên bản bất biến của một WIR; mỗi instance được "ghim" (pin) vào version tại thời điểm khởi tạo.
- **Event_Upcaster**: Thành phần nâng cấp (upcast) event/snapshot của version cũ lên schema mới khi cấu trúc thay đổi, để replay không lỗi.
- **Read_Model (Projection)**: Mô hình đọc được dẫn xuất từ event log, tối ưu cho truy vấn/lọc/thống kê (CQRS query side).
- **Projection_Engine**: Thành phần cập nhật Read_Model từ event log và hỗ trợ rebuild.
- **Dead_Letter_Queue (DLQ)**: Hàng đợi chứa task thất bại vĩnh viễn để cô lập và replay sau khi hot-fix.
- **Safe_Point**: Điểm trong vòng đời instance không có token đang di chuyển giữa các transition, an toàn để migrate version.
- **Configuration_Profile**: Bộ cấu hình versioned theo tenant/environment/workflow scope, chứa policy, quota, timeout, retry, SLA, feature flag, routing và các tham số vận hành có thể thay đổi mà không sửa mã nguồn.
- **Configuration_Store**: Kho lưu cấu hình có schema/version/audit; có thể là PostgreSQL, object storage versioned, GitOps-backed store hoặc service cấu hình chuyên biệt tùy bounded context, nhưng phải có hợp đồng đọc nhất quán và cơ chế rollback.

## Requirements

### Requirement 1: BPMN AOT Compiler (BPMN-as-IR)

**User Story:** As a Business Analyst, I want to compile BPMN/DMN/CMMN models ahead of time into an optimized intermediate representation, so that workflows run type-safe and với hiệu năng cao mà không cần parse XML lúc runtime.

#### Acceptance Criteria

1. WHEN một file BPMN 2.0 hợp lệ được cung cấp cho BPMN_Compiler, THE BPMN_Compiler SHALL biên dịch file đó thành một WIR tương ứng.
2. WHERE file DMN hoặc CMMN được cung cấp cùng model, THE BPMN_Compiler SHALL biên dịch các file đó thành WIR tích hợp với BPMN.
3. WHEN quá trình biên dịch hoàn tất thành công, THE BPMN_Compiler SHALL sinh ra state machine Rust từ WIR mà không thực hiện parse XML tại runtime.
4. IF một gateway không vét cạn (non-exhaustive) toàn bộ nhánh điều kiện, THEN THE BPMN_Compiler SHALL báo lỗi biên dịch kèm vị trí (line và column) của phần tử vi phạm.
5. IF tồn tại đường đi chết hoặc không thể tới được (dead/unreachable path), THEN THE BPMN_Compiler SHALL báo lỗi biên dịch kèm vị trí phần tử vi phạm.
6. IF một hoạt động có phạm vi bù trừ nhưng thiếu compensation handler, THEN THE BPMN_Compiler SHALL báo lỗi biên dịch kèm vị trí phần tử vi phạm.
7. IF phát hiện xung đột SLA giữa các phần tử, THEN THE BPMN_Compiler SHALL báo lỗi biên dịch kèm mô tả xung đột và vị trí phần tử.
8. IF hợp đồng dữ liệu (data contract) giữa hai phần tử không khớp kiểu, THEN THE BPMN_Compiler SHALL báo lỗi biên dịch kèm vị trí phần tử vi phạm.
9. WHEN BPMN_Compiler được chạy trong pipeline CI/CD và phát hiện bất kỳ lỗi biên dịch nào, THE BPMN_Compiler SHALL trả về exit code khác 0 để pipeline dừng lại.
10. WHEN một WIR được sinh ra, THE BPMN_Compiler SHALL xuất WIR ra định dạng tuần tự hóa (serialized) để runtime nạp trực tiếp.
11. FOR ALL model BPMN hợp lệ, việc biên dịch thành WIR rồi in ngược (pretty-print) WIR ra biểu diễn chuẩn hóa rồi biên dịch lại SHALL cho ra WIR tương đương (round-trip property).
12. THE BPMN_Compiler và BPMP Engine SHALL dùng cùng một canonical, versioned WIR schema; WIR artifact SHALL dùng durable serialization contract có schema evolution và integrity signature, không phụ thuộc Rust memory layout.

### Requirement 2: Human Runtime (BA-first governance)

**User Story:** As a Business Analyst, I want a human-centric runtime for user tasks and approvals, so that governance, audit và SLA được đảm bảo cho các quy trình có sự tham gia của con người.

#### Acceptance Criteria

1. WHEN một User Task được kích hoạt trong Human_Runtime, THE Human_Runtime SHALL tạo một work item và gán cho người dùng hoặc nhóm được chỉ định.
2. WHEN một người dùng hoàn thành một Approval task, THE Human_Runtime SHALL ghi nhận kết quả phê duyệt và chuyển instance sang trạng thái kế tiếp theo WIR.
3. IF một task vượt quá thời hạn SLA đã cấu hình, THEN THE Human_Runtime SHALL kích hoạt cơ chế Escalation tương ứng.
4. WHEN một người dùng ủy quyền (delegate) một task cho người khác, THE Human_Runtime SHALL chuyển quyền xử lý task và ghi lại hành động ủy quyền vào audit log.
5. WHERE model chứa CMMN Case, THE Human_Runtime SHALL quản lý vòng đời case bao gồm các stage và milestone theo định nghĩa CMMN.
6. THE Human_Runtime SHALL lưu trạng thái các work item và case trong PostgreSQL.
7. WHEN Human_Runtime xử lý một thao tác của người dùng dưới tải bình thường, THE Human_Runtime SHALL hoàn tất phản hồi API ở P95 trong tối đa 500ms, đo từ lúc API nhận request hợp lệ đến lúc trả response, không đặt lower bound cho latency.
8. WHEN bất kỳ thay đổi trạng thái nào của một human task xảy ra, THE Human_Runtime SHALL ghi một bản ghi audit bất biến gồm actor, hành động và timestamp.
9. WHEN Human_Runtime gửi command thay mặt một actor tới BPMP Engine, THE Human_Runtime SHALL forward original signed token hoặc short-lived signed actor context của chính actor đó; workload identity của Human Runtime SHALL chỉ xác thực service caller và SHALL NOT thay thế danh tính/quyền của actor.

### Requirement 3: Stream Runtime (event-driven orchestration)

**User Story:** As a solution engineer, I want a high-throughput event-driven orchestration runtime, so that các Service Task, message, timer và saga được thực thi với độ trễ thấp và độ tin cậy cao.

#### Acceptance Criteria

1. WHEN một Service Task được kích hoạt trong Stream_Runtime, THE Stream_Runtime SHALL điều phối task tới một Worker phù hợp để thực thi.
2. WHEN một Message Event được nhận, THE Stream_Runtime SHALL tương quan (correlate) message tới đúng workflow instance dựa trên correlation key.
3. WHEN một Timer đến hạn, THE Stream_Runtime SHALL kích hoạt chuyển tiếp tương ứng trong WIR.
4. IF việc thực thi một Service Task thất bại, THEN THE Stream_Runtime SHALL áp dụng chính sách Retry đã cấu hình.
5. IF một saga cần rollback, THEN THE Stream_Runtime SHALL thực thi các Compensation handler theo thứ tự nghịch đảo với thứ tự các bước đã hoàn thành.
6. THE Stream_Runtime SHALL lưu mọi thay đổi trạng thái vào Event_Store dạng append-only.
7. WHEN Stream_Runtime điều phối một task dưới tải mục tiêu, THE Stream_Runtime SHALL đạt độ trễ quyết định-đến-điều-phối dưới 10ms, đo từ lúc task sẵn sàng trong state machine đến lúc task được enqueue/hand-off cho Dispatcher, không bao gồm thời gian worker thực thi hoặc commit quorum Raft.
8. WHERE một batch workflow được định nghĩa, THE Stream_Runtime SHALL xử lý các phần tử của batch theo cơ chế chunk để giới hạn bộ nhớ sử dụng.
9. WHEN một side-effect ngoài hệ thống hoàn tất hoặc một compensation handler được thực thi, THE Stream_Runtime SHALL cập nhật Compensation_Ledger một cách append-only và idempotent để tiến độ rollback có thể resume sau lỗi mà không chạy lại handler đã thành công.
10. THE BPM_Platform SHALL duy trì đúng một authoritative implementation của WIR interpreter, `decide()` và `evolve()` trong Rust BPMP Engine; mọi service ngoài engine SHALL gửi command qua versioned contract và SHALL NOT tự diễn giải WIR hoặc tự áp workflow transition.

### Requirement 4: Event Sourcing Core

**User Story:** As a compliance officer, I want an append-only immutable event log with deterministic replay, so that mọi thay đổi đều được audit đầy đủ và có thể tái hiện chính xác để điều tra.

#### Acceptance Criteria

1. WHEN một thay đổi trạng thái xảy ra, THE Event_Store SHALL ghi một event bất biến (immutable) vào cuối log (append-only).
2. THE Event_Store SHALL tạo một Snapshot sau mỗi N event theo cấu hình.
3. WHEN yêu cầu runtime replay một instance và toàn bộ payload vận hành cần thiết còn đọc được, THE Event_Store SHALL tái dựng trạng thái từ snapshot gần nhất cộng các event tiếp theo một cách xác định; nếu payload đã bị xóa hợp lệ theo Requirement 22, replay SHALL trả cùng một lỗi compliance tường minh thay vì dựng partial state.
4. FOR ALL chuỗi event đọc được của một instance, việc replay chuỗi event đó SHALL luôn cho ra cùng một trạng thái cuối; FOR ALL chuỗi có payload vận hành đã bị crypto-shred hợp lệ, runtime replay SHALL luôn cho ra cùng một typed error tại cùng ranh giới event (deterministic replay result property).
5. WHEN người dùng dùng Time_Travel_Cockpit kéo timeline slider tới một thời điểm, THE Time_Travel_Cockpit SHALL hiển thị trạng thái instance tại đúng thời điểm đó, ngoại trừ trường đã bị xóa hợp lệ thì SHALL hiển thị marker không chứa PII thay vì giá trị gốc.
6. THE Event_Store SHALL đảm bảo các event đã ghi không bị sửa đổi hoặc xóa (immutable audit).
7. WHERE tích hợp CDC/Kafka được bật, THE Event_Store SHALL phát các event ra kênh CDC/Kafka theo đúng thứ tự append.
8. WHEN một local WAL append được thực hiện ở chế độ single-node hoặc trong state machine cục bộ sau Raft commit, THE Event_Store SHALL hoàn tất thao tác ghi bền vững cục bộ của ciphertext đã chuẩn bị trong dưới 1ms; tiêu chí này không bao gồm bước chuẩn bị payload, network round-trip, KMS call hoặc quorum replication.
9. WHEN một event được ghi rồi đọc lại từ Event_Store, THE Event_Store SHALL trả về event có nội dung tương đương với event đã ghi (serialization round-trip property).
10. WHERE quy trình hot-fix được thực hiện, THE BPM_Platform SHALL cho phép rewind tới thời điểm trước lỗi, nạp flow đã sửa và replay lại các event tiếp theo.

### Requirement 5: Hybrid Worker Model — Local WASM Workers

**User Story:** As a developer, I want to run script tasks as sandboxed WASM in-process, so that logic tùy biến chạy nhanh, an toàn và không thể làm sập engine.

#### Acceptance Criteria

1. WHERE một script task được viết bằng TypeScript, Python hoặc Go và biên dịch sang WASM, THE WASM_Worker SHALL thực thi module WASM đó in-process qua wasmtime.
2. THE WASM_Worker SHALL áp đặt hạn mức bộ nhớ (memory quota) nghiêm ngặt cho mỗi lần thực thi module WASM.
3. THE WASM_Worker SHALL áp dụng CPU fuel metering cho mỗi lần thực thi module WASM.
4. IF một module WASM tiêu thụ hết CPU fuel (ví dụ vòng lặp vô hạn), THEN THE WASM_Worker SHALL trap (dừng) việc thực thi mà không làm sập engine hay worker thread.
5. IF một script task thất bại hoặc panic, THEN THE WASM_Worker SHALL cô lập lỗi và báo kết quả thất bại về Stream_Runtime mà không làm sập engine.
6. IF một module WASM vượt hạn mức bộ nhớ, THEN THE WASM_Worker SHALL chấm dứt thực thi module đó và báo lỗi tài nguyên.
7. WHEN dữ liệu kích thước lớn được truyền giữa host (engine) và guest (WASM module), THE WASM_Worker SHALL truyền dữ liệu qua shared linear memory theo cơ chế zero-copy ở nơi khả thi, thay vì sao chép toàn bộ payload nhiều lần.
8. FOR ALL dữ liệu được truyền vào rồi lấy ra khỏi một module WASM qua giao diện host, giá trị lấy ra SHALL tương đương với giá trị truyền vào (host-guest data round-trip).

### Requirement 6: Hybrid Worker Model — Remote gRPC Workers & Dispatch

**User Story:** As a platform operator, I want remote workers connected via backpressure-aware streaming, so that hệ thống chịu được 100k+ CCU mà worker không bao giờ quá tải.

#### Acceptance Criteria

1. WHERE một task cần I/O nặng hoặc gọi API bên ngoài, THE Dispatcher SHALL điều phối task tới một Remote_Worker qua gRPC (tonic).
2. THE Dispatcher SHALL phân phối task qua gRPC bidirectional streaming thay vì polling.
3. THE Dispatcher SHALL áp dụng credit-based flow control để một Remote_Worker chỉ nhận số task nằm trong hạn mức credit mà nó công bố.
4. IF một Remote_Worker giảm credit về 0, THEN THE Dispatcher SHALL tạm dừng gửi task mới cho worker đó cho tới khi credit được bổ sung (backpressure).
5. WHEN hệ thống ở tải mục tiêu và có worker còn credit phù hợp, THE Dispatcher SHALL đạt độ trễ P99 dưới 5ms, đo từ lúc nhận task đã sẵn sàng đến lúc gửi `Assign` frame thành công trên gRPC stream; tiêu chí này là một đoạn con của Requirement 3.7.
6. THE BPM_Platform SHALL hỗ trợ tối thiểu 100.000 kết nối worker đồng thời (CCU).
7. WHEN có 1.000.000 workflow đang "ngủ" (sleeping/passivated), THE BPM_Platform SHALL duy trì working-set RAM cho runtime index trong khoảng 2GB đến 4GB, với full InstanceState đã được passivate xuống Event_Store/Snapshot; con số này không bao gồm block cache của storage engine hoặc full state của instance active.
8. IF một Remote_Worker mất kết nối trong khi giữ task, THEN THE Dispatcher SHALL điều phối lại (re-dispatch) task đó cho worker khác một cách idempotent.

### Requirement 7: Identity-Aware Workflow

**User Story:** As a security officer, I want security enforced at the state-machine level, so that mọi chuyển trạng thái nhạy cảm đều được xác thực danh tính và ghi audit hợp pháp.

#### Acceptance Criteria

1. WHEN một state transition được yêu cầu (ví dụ hoàn thành human task hoặc gửi message), THE BPM_Platform SHALL yêu cầu một JWT/cryptographic token đi kèm yêu cầu đó.
2. WHEN một token đi kèm một transition, THE BPM_Platform SHALL xác minh claim và role của token tại lõi graph TRƯỚC KHI cho phép transition xảy ra.
3. IF token không hợp lệ, hết hạn, hoặc thiếu quyền yêu cầu, THEN THE BPM_Platform SHALL từ chối transition và giữ nguyên trạng thái instance.
4. WHEN một transition được cho phép, THE BPM_Platform SHALL ghi vào audit log định danh actor đã ký transition, role của họ và timestamp.
5. THE BPM_Platform SHALL tích hợp với Identity_Provider (Keycloak) để lấy và xác thực claim/role.
6. THE BPM_Platform SHALL hỗ trợ cả RBAC và ABAC khi đánh giá quyền cho một transition.
7. FOR ALL transition đã được thực thi thành công, audit log SHALL chứa một bản ghi tương ứng gồm actor, role và timestamp (audit completeness property).
8. WHEN một trusted microservice gửi transition thay mặt end-user, THE BPM_Platform SHALL phân biệt workload identity và actor identity; service credential SHALL NOT tự cấp quyền transition cho actor, và engine SHALL tái xác thực actor context theo cùng RBAC/ABAC policy như direct command.

### Requirement 8: Reactive Push Cockpit

**User Story:** As a manager, I want real-time dashboards pushed to my browser, so that tôi theo dõi quy trình trực tiếp mà không gây tải lên database hay core API.

#### Acceptance Criteria

1. WHEN một event được append vào Event_Store, THE Cockpit SHALL phát tín hiệu tương ứng qua kênh in-memory pub/sub.
2. WHEN một trình duyệt kết nối tới Cockpit, THE Cockpit SHALL thiết lập kênh push qua SSE hoặc WebSocket trên tokio.
3. WHEN một tín hiệu pub/sub được phát, THE Cockpit SHALL đẩy cập nhật tới các client đang đăng ký sự kiện liên quan.
4. THE Cockpit SHALL hỗ trợ hàng chục nghìn kết nối trình duyệt đồng thời.
5. WHEN Cockpit đẩy cập nhật thời gian thực, THE Cockpit SHALL lấy dữ liệu từ tín hiệu in-memory thay vì truy vấn trực tiếp database cho mỗi cập nhật.
6. IF một client ngắt kết nối, THEN THE Cockpit SHALL giải phóng tài nguyên đăng ký của client đó.

### Requirement 9: AI-Native Process Intelligence (Process Copilot)

**User Story:** As a process owner, I want an in-engine AI copilot, so that tôi dự đoán vi phạm SLA, tối ưu route và sinh BPMN từ mô tả nghiệp vụ mà không cần công cụ ngoài.

#### Acceptance Criteria

1. THE BPM_Platform SHALL thu thập telemetry theo từng instance và cung cấp cho Process_Copilot.
2. WHEN Process_Copilot phân tích tập instance đang chạy, THE Process_Copilot SHALL dự đoán các case có nguy cơ vi phạm SLA trong 24 giờ tới.
3. WHEN một gateway có nhiều nhánh khả dụng, THE Process_Copilot SHALL gợi ý route tối ưu nhằm giảm thời gian hoàn tất (turnaround time).
4. WHEN người dùng cung cấp một mô tả nghiệp vụ bằng ngôn ngữ tự nhiên, THE Process_Copilot SHALL sinh ra một mô hình BPMN nháp tương ứng.
5. WHERE mô tả nghiệp vụ được viết bằng tiếng Việt, THE Process_Copilot SHALL hỗ trợ xử lý đầu vào tiếng Việt.
6. WHEN Process_Copilot phân tích lịch sử event, THE Process_Copilot SHALL xác định các nút thắt cổ chai (bottleneck) và nguyên nhân gốc (root cause).
7. THE Process_Copilot SHALL đưa ra khuyến nghị tối ưu quy trình dựa trên dữ liệu telemetry và event log.

### Requirement 10: Progressive BPMN (dual synchronized views)

**User Story:** As a Business Analyst, I want a business view kept in sync with an engineering view of the same workflow, so that mô hình nghiệp vụ không bị lẫn chi tiết kỹ thuật trong khi kỹ sư vẫn có đầy đủ năng lực orchestration.

#### Acceptance Criteria

1. THE BPM_Platform SHALL biểu diễn cùng một workflow dưới hai góc nhìn: BA_View và Engineer_View.
2. THE BA_View SHALL chỉ hiển thị các bước nghiệp vụ, ẩn các chi tiết kỹ thuật.
3. THE Engineer_View SHALL hiển thị các chi tiết kỹ thuật gồm retry, compensation, timeout, circuit breaker, idempotency, message correlation và saga.
4. WHEN một thay đổi được thực hiện ở một view, THE BPM_Platform SHALL đồng bộ thay đổi tương ứng sang view còn lại.
5. FOR ALL workflow, BA_View và Engineer_View SHALL luôn tham chiếu cùng một WIR nền (consistency property).

### Requirement 11: Clustering / High Availability

**User Story:** As a platform operator, I want Raft-based clustering, so that hệ thống tự động failover và nhân bản event log mà không cần database trung tâm.

#### Acceptance Criteria

1. THE BPM_Platform SHALL vận hành như một cụm gồm nhiều Cluster_Node sử dụng consensus Raft (openraft).
2. WHEN một event được commit, THE BPM_Platform SHALL nhân bản event đó tới đa số (quorum) các Cluster_Node trước khi xác nhận.
3. IF node leader gặp sự cố, THEN THE BPM_Platform SHALL bầu chọn một leader mới và tiếp tục xử lý.
4. THE BPM_Platform SHALL nhân bản event log giữa các node mà không phụ thuộc vào một database trung tâm.
5. IF một node bị phân vùng mạng (network partition) khỏi quorum, THEN THE BPM_Platform SHALL ngăn node đó commit event mới cho tới khi tái gia nhập quorum.
6. WHEN hệ thống chạy ở chế độ cluster, THE BPM_Platform SHALL đo và báo cáo latency commit end-to-end riêng cho Raft quorum commit, tách biệt với latency local WAL append của Requirement 4.8.

### Requirement 12: Reliability & Fault Tolerance (Non-blocking Business Flow)

**User Story:** As a platform operator, I want non-blocking, fault-tolerant execution, so that luồng nghiệp vụ không dừng do lỗi cục bộ hoặc phụ thuộc bên ngoài.

#### Acceptance Criteria

1. IF một dependency bên ngoài không phản hồi trong thời hạn timeout, THEN THE BPM_Platform SHALL áp dụng timeout protection và tiếp tục theo chiến lược fallback đã cấu hình.
2. WHERE một circuit breaker được cấu hình cho một tích hợp, THE BPM_Platform SHALL mở circuit khi tỷ lệ lỗi vượt ngưỡng và ngừng gọi tạm thời.
3. IF một thao tác lỗi tạm thời (transient), THEN THE BPM_Platform SHALL retry theo chính sách backoff đã cấu hình.
4. WHERE bulkhead isolation được cấu hình, THE BPM_Platform SHALL cô lập tài nguyên giữa các nhóm workload để lỗi ở một nhóm không lan sang nhóm khác.
5. IF một task thất bại vĩnh viễn sau khi hết số lần retry, THEN THE BPM_Platform SHALL chuyển task vào dead-letter queue để xử lý sau.

### Requirement 13: Observability

**User Story:** As an SRE, I want full observability, so that tôi giám sát latency, error-rate và audit toàn hệ thống.

#### Acceptance Criteria

1. WHEN một thao tác quan trọng bắt đầu, THE BPM_Platform SHALL gán một Correlation_ID và đính kèm vào toàn bộ log của luồng xử lý đó.
2. THE BPM_Platform SHALL phát trace phân tán theo chuẩn OpenTelemetry cho các thao tác quan trọng.
3. THE BPM_Platform SHALL phát metrics gồm latency, error-rate và mức sử dụng tài nguyên.
4. WHEN một lỗi xảy ra, THE BPM_Platform SHALL ghi structured log chứa Correlation_ID và nguyên nhân gốc mà không làm lộ dữ liệu nhạy cảm.
5. IF một chỉ số SLA vượt ngưỡng cảnh báo, THEN THE BPM_Platform SHALL phát cảnh báo giám sát tương ứng.

### Requirement 14: API Standards

**User Story:** As an integration developer, I want consistent, versioned APIs, so that tôi tích hợp an toàn và ổn định với nền tảng.

#### Acceptance Criteria

1. THE BPM_Platform SHALL cung cấp API có version rõ ràng.
2. WHEN một API request được nhận, THE BPM_Platform SHALL xác thực (validate) request theo DTO đã định nghĩa trước khi xử lý.
3. IF một request không hợp lệ, THEN THE BPM_Platform SHALL trả về lỗi có cấu trúc mà không làm lộ internal entity hay stack trace.
4. WHEN một endpoint trả về tập dữ liệu lớn, THE BPM_Platform SHALL hỗ trợ pagination để giới hạn kích thước phản hồi.
5. WHEN một client vượt quá hạn mức rate limit, THE BPM_Platform SHALL từ chối request vượt hạn mức với mã lỗi phù hợp.
6. THE BPM_Platform SHALL định nghĩa inter-service API/event bằng versioned Protocol Buffers và SHALL chạy automated backward-compatibility/breaking-change check trong CI trước khi merge schema change.
7. THE BPM_Platform SHALL cấm service truy cập trực tiếp database thuộc bounded context khác; dữ liệu liên service SHALL đi qua versioned API hoặc committed integration event.

### Requirement 15: Security & Data Protection

**User Story:** As a security officer, I want strong security defaults, so that dữ liệu nhạy cảm và bí mật hệ thống được bảo vệ.

#### Acceptance Criteria

1. THE BPM_Platform SHALL áp dụng nguyên tắc least privilege cho mọi truy cập tài nguyên.
2. THE BPM_Platform SHALL mã hóa dữ liệu khi truyền (encryption in transit).
3. THE BPM_Platform SHALL mã hóa dữ liệu nhạy cảm khi lưu trữ (encryption at rest) cho Event_Store, Snapshot, Read_Model, DLQ và HumanTaskStore.
4. THE BPM_Platform SHALL quản lý secrets và khóa mã hóa qua cơ chế secrets/key management, không hardcode trong mã nguồn hay cấu hình.
5. WHEN một đầu vào từ client được nhận, THE BPM_Platform SHALL validate đầu vào trước khi sử dụng.
6. IF một lỗi xảy ra trong quá trình xử lý, THEN THE BPM_Platform SHALL trả về thông điệp lỗi an toàn không chứa dữ liệu nhạy cảm hay stack trace.
7. THE BPM_Platform SHALL dùng envelope encryption với DEK được unwrap và cache theo KeyScope trong bộ nhớ tiến trình với TTL/rotation policy; KMS bên ngoài SHALL NOT được gọi cho mỗi event append khi cache còn hợp lệ.
8. WHEN một key bị rotate hoặc crypto-shred, THE BPM_Platform SHALL vô hiệu hóa DEK cache liên quan và không cho phép cache đã hết hạn hoặc bị thu hồi tiếp tục mã hóa/giải mã dữ liệu mới.
9. IF cần mã hóa dữ liệu nhưng không có DEK hợp lệ và KMS không khả dụng, THEN THE BPM_Platform SHALL fail-closed, không ghi plaintext, không append event và trả lỗi tường minh mà không thay đổi state.

### Requirement 16: Concurrency & Distributed Correctness

**User Story:** As a platform architect, I want correct behavior under concurrency and duplication, so that dữ liệu nhất quán trong môi trường phân tán.

#### Acceptance Criteria

1. WHEN một request mang một Idempotency_Key đã được xử lý trước đó, THE BPM_Platform SHALL xác thực và phân quyền lại actor hiện tại trước khi trả về kết quả đã lưu của lần xử lý trước, và không thực thi lại tác dụng phụ.
2. IF hai cập nhật đồng thời tác động lên cùng một aggregate, THEN THE BPM_Platform SHALL dùng optimistic locking (versioning) để phát hiện xung đột và từ chối cập nhật lỗi thời.
3. WHEN các event của một instance được xử lý, THE BPM_Platform SHALL giữ đúng thứ tự event (event ordering) cho instance đó.
4. IF một event trùng lặp được nhận, THEN THE BPM_Platform SHALL loại bỏ trùng lặp (deduplication) dựa trên định danh event.
5. FOR ALL thao tác có Idempotency_Key, việc áp dụng thao tác đó nhiều lần SHALL cho kết quả trạng thái giống như áp dụng đúng một lần (idempotence property).

### Requirement 17: Performance & Memory Efficiency

**User Story:** As a platform operator, I want predictable performance and bounded memory, so that hệ thống ổn định dưới tải lớn và dữ liệu lớn.

#### Acceptance Criteria

1. WHEN xử lý một tập dữ liệu lớn, THE BPM_Platform SHALL xử lý theo stream/chunk thay vì nạp toàn bộ vào bộ nhớ.
2. THE BPM_Platform SHALL sử dụng bounded collection để tránh tăng trưởng bộ nhớ không giới hạn.
3. WHEN truy vấn tập kết quả lớn từ storage, THE BPM_Platform SHALL dùng cursor/pagination để giới hạn bộ nhớ.
4. THE BPM_Platform SHALL sử dụng connection pooling cho truy cập database.
5. IF một truy vấn có nguy cơ full table scan trên trường được lọc thường xuyên, THEN THE BPM_Platform SHALL yêu cầu index tương ứng để tránh table scan.

### Requirement 18: Comprehensive Testing & Quality Gates

**User Story:** As a quality engineer, I want comprehensive automated testing, so that tính đúng đắn được đảm bảo qua nhiều lớp kiểm thử.

#### Acceptance Criteria

1. THE BPM_Platform SHALL có unit test cho các thành phần logic nghiệp vụ.
2. THE BPM_Platform SHALL có integration test cho các điểm tích hợp giữa các thành phần.
3. THE BPM_Platform SHALL có property-based test cho các thuộc tính đúng đắn phổ quát (bao gồm round-trip của BPMN_Compiler và Event_Store).
4. THE BPM_Platform SHALL có contract test cho các API công khai.
5. THE BPM_Platform SHALL có failure/chaos test mô phỏng lỗi dependency và network partition.

### Requirement 19: Workflow Schema Evolution & Versioning

**User Story:** As a platform operator, I want workflows to evolve across versions safely, so that các instance đang chạy trên version cũ vẫn replay và hoàn tất đúng khi version mới được deploy.

#### Acceptance Criteria

1. WHEN một workflow instance được khởi tạo, THE BPM_Platform SHALL gán cho instance một WorkflowVersion bất biến tại thời điểm khởi tạo.
2. WHEN một WIR version mới được deploy, THE BPM_Platform SHALL tiếp tục thực thi các instance đang chạy bằng đúng WorkflowVersion mà chúng được khởi tạo (version pinning).
3. WHEN một instance mới được khởi tạo sau khi deploy version mới, THE BPM_Platform SHALL sử dụng WorkflowVersion mới nhất theo migration policy đã cấu hình.
4. THE BPM_Platform SHALL lưu WorkflowVersion trong mỗi event và mỗi snapshot.
5. IF cấu trúc event hoặc state của một version thay đổi so với version cũ, THEN THE BPM_Platform SHALL dùng Event_Upcaster để nâng cấp event/snapshot cũ lên schema hiện tại trước khi replay, mà không phát sinh lỗi.
6. FOR ALL event của một version cũ, việc upcast rồi replay SHALL cho ra state hợp lệ tương đương ngữ nghĩa với version gốc (upcast round-trip/consistency).
7. WHERE migration policy cho phép chuyển instance đang chạy sang version mới, THE BPM_Platform SHALL chỉ áp dụng migration tại một Safe_Point.
8. WHEN không còn active instance, snapshot, replay job hoặc retention policy nào tham chiếu tới một WIR version cũ, THE BPM_Platform SHALL cho phép retire/unload version đó khỏi registry runtime mà không ảnh hưởng replay hợp lệ.

### Requirement 20: Read Models & Projections (CQRS Query Side)

**User Story:** As a Business Analyst, I want fast queries over hundreds of thousands of instances, so that tôi tìm/lọc/thống kê nhanh mà không phải scan event log.

#### Acceptance Criteria

1. THE Projection_Engine SHALL duy trì các Read_Model được dẫn xuất từ event log để phục vụ truy vấn.
2. WHEN một event được append, THE Projection_Engine SHALL cập nhật các Read_Model liên quan.
3. WHEN một truy vấn danh sách hoặc lọc được thực hiện (ví dụ "các task chờ duyệt của actor A"), THE BPM_Platform SHALL trả kết quả từ Read_Model thay vì scan event log.
4. THE Projection_Engine SHALL lưu checkpoint (sequence đã xử lý) để cập nhật tăng dần và tiếp tục đúng vị trí sau khi khởi động lại.
5. WHERE một Read_Model bị lỗi thời hoặc hỏng, THE Projection_Engine SHALL cho phép rebuild Read_Model bằng cách replay event log mà không ảnh hưởng write side.
6. FOR ALL Read_Model, việc rebuild bằng replay toàn bộ event log SHALL cho ra cùng trạng thái Read_Model như quá trình cập nhật tăng dần (projection determinism).

### Requirement 21: Dead-Letter Replay & Failure Replayability

**User Story:** As an SRE, I want to replay a single failed task after a hot-fix, so that tôi khôi phục instance mà không cần chạy lại toàn bộ workflow từ start event.

#### Acceptance Criteria

1. WHEN một task được đưa vào Dead_Letter_Queue, THE BPM_Platform SHALL lưu đủ ngữ cảnh (input, số attempt, correlation id, instance state ref) để có thể replay lại chính task đó.
2. WHEN một SRE kích hoạt replay một dead-letter task sau hot-fix, THE BPM_Platform SHALL thực thi lại chỉ task đó dựa trên trạng thái instance hiện tại, không replay lại toàn bộ workflow từ start event.
3. WHEN một dead-letter task được replay thành công, THE BPM_Platform SHALL tiếp tục instance từ điểm đó theo WIR.
4. FOR ALL dead-letter task, việc replay task đó SHALL là idempotent, không tạo double-effect cho các bước đã hoàn thành trước đó.
5. WHEN một hành động dead-letter replay được thực hiện, THE BPM_Platform SHALL ghi audit gồm actor, task và timestamp.

### Requirement 22: Multi-Tenancy, Retention & Data Governance

**User Story:** As a compliance officer, I want tenant isolation and regulated data lifecycle controls, so that immutable audit vẫn tồn tại nhưng dữ liệu nhạy cảm được bảo vệ theo nghĩa vụ pháp lý.

#### Acceptance Criteria

1. WHEN bất kỳ event, snapshot, work item, read model, DLQ entry hoặc audit record nào được tạo, THE BPM_Platform SHALL gắn TenantId và scope mọi truy cập theo TenantId.
2. IF một actor hoặc worker thuộc tenant A truy cập tài nguyên của tenant B, THEN THE BPM_Platform SHALL từ chối truy cập và giữ nguyên dữ liệu.
3. THE BPM_Platform SHALL áp dụng quota, rate-limit, worker pool và projection index theo tenant để một tenant không làm cạn tài nguyên tenant khác.
4. THE BPM_Platform SHALL hỗ trợ retention policy theo tenant, workflow type và data classification.
5. WHEN retention policy hết hạn hoặc yêu cầu xóa dữ liệu cá nhân hợp lệ được phê duyệt, THE BPM_Platform SHALL loại bỏ khả năng đọc dữ liệu nhạy cảm bằng masking/anonymization hoặc crypto-shredding, đồng thời vẫn giữ audit tombstone không chứa PII để bảo toàn tính bất biến của event log.
6. WHERE data residency được cấu hình, THE BPM_Platform SHALL giới hạn lưu trữ và xử lý dữ liệu của tenant trong vùng/cluster được phép.
7. FOR ALL thao tác masking/anonymization/crypto-shredding, THE BPM_Platform SHALL ghi audit record gồm actor, chính sách áp dụng, phạm vi dữ liệu và timestamp.
8. WHEN crypto-shredding dữ liệu có thể tham gia vào `evolve()` hoặc `decide()` của một instance, THE BPM_Platform SHALL chỉ hủy khóa sau khi instance là Terminal_Instance.
9. IF nghĩa vụ pháp lý yêu cầu crypto-shredding khi instance chưa terminal, THEN THE BPM_Platform SHALL fence mọi execution path nghiệp vụ thông thường và chọn policy theo criteria 22.11–22.15. Key SHALL chỉ bị hủy sau khi instance đã terminal và mọi governance prerequisite của policy đã commit; compensation được policy cho phép SHALL không bị hủy như một dispatch thông thường.
10. IF runtime rehydrate một instance và phát hiện payload vận hành không thể giải mã do retention/crypto-shredding, THEN THE BPM_Platform SHALL trả lỗi compliance tường minh, không thay thế bằng giá trị rỗng/default và không gọi `decide()` hoặc `evolve()` tiếp theo trên state thiếu dữ liệu.
11. WHEN erasure được yêu cầu cho instance đang `Compensating` hoặc Compensation_Ledger còn side-effect chưa hoàn tác, THE BPM_Platform SHALL mặc định áp dụng policy `CompensationBeforeErasure`: ghi trạng thái erasure-pending, bắt đầu hoặc tiếp tục compensation và chỉ crypto-shred sau khi mọi side-effect pending đã được ledger xác nhận `Compensated`.
12. THE BPM_Platform SHALL cấu hình và giám sát `compensation_erasure_deadline` không vượt quá legal erasure deadline; IF compensation có nguy cơ vượt deadline, THEN THE BPM_Platform SHALL escalation cho compliance và operations trước deadline.
13. WHERE legal policy cho phép `AbortAndReconcile` và bắt buộc xóa trước khi compensation hoàn tất, THE BPM_Platform SHALL, trước khi crypto-shred, commit atomically `Terminated_For_Compliance`, trạng thái ledger `ReconciliationRequired` và đúng một Reconciliation_Work_Item cho từng side-effect chưa được hoàn tác; mỗi work item SHALL chỉ chứa metadata phi-PII từ Compensation_Ledger và SHALL tồn tại độc lập với key của Data_Subject.
14. IF Compensation_Ledger thiếu opaque operation reference đủ để đối soát một side-effect pending, THEN policy `AbortAndReconcile` SHALL bị từ chối và instance SHALL tiếp tục `CompensationBeforeErasure`; THE BPM_Platform SHALL NOT âm thầm bỏ qua side-effect chưa hoàn tác.
15. FOR ALL Reconciliation_Work_Item, THE BPM_Platform SHALL theo dõi SLA, audit mọi thay đổi và chỉ đóng item khi side-effect đã được xác nhận hoàn tác, được chấp nhận bằng một quyết định nghiệp vụ có thẩm quyền, hoặc được chuyển sang quy trình incident tường minh.
16. THE BPM_Platform SHALL giới hạn `AbortAndReconcile` bằng capability chuyên biệt `governance.abort_and_reconcile`, chỉ được gán qua policy cho các role quản trị compliance/legal được phê duyệt; quyền transition chung trên workflow SHALL NOT đủ để thực hiện hành động này.
17. WHEN `AbortAndReconcile` được yêu cầu, THE BPM_Platform SHALL áp dụng dual-control với hai actor khác nhau trong cùng tenant: một requester và một approver; một actor SHALL NOT tự phê duyệt yêu cầu của chính mình.
18. EACH approval SHALL dùng xác thực mới đạt authentication assurance policy, có thời hạn ngắn và được bind bằng chữ ký tới `tenant_id`, `policy_id`, `legal_deadline`, `key_epoch` và digest của tập Compensation_Ledger pending. IF bất kỳ giá trị bind nào thay đổi hoặc approval hết hạn, THEN approval SHALL mất hiệu lực và phải được thực hiện lại.
19. BEFORE commit atomic transition của criterion 22.13, THE BPM_Platform SHALL tái kiểm capability, trạng thái actor và tính hợp lệ của cả hai approval; IF một kiểm tra thất bại, THEN transition và crypto-shredding SHALL bị từ chối, state SHALL không đổi. Audit SHALL lưu requester, approver, lý do, approval timestamps và request digest.

### Requirement 23: Dynamic Configuration & No Hardcoded Policy

**User Story:** As a platform operator, I want business policy and operational parameters to be configurable and versioned, so that hệ thống có thể thay đổi theo tenant, môi trường và quy định vận hành mà không cần sửa code hoặc redeploy toàn bộ.

#### Acceptance Criteria

1. THE BPM_Platform SHALL NOT hardcode business policy, tenant-specific value, SLA threshold, retry/backoff policy, timeout, rate limit, quota, worker routing rule, escalation rule, retention rule, data residency rule, feature flag hoặc integration endpoint trong domain/application logic.
2. THE BPM_Platform SHALL đọc các giá trị cấu hình thay đổi được từ Configuration_Store hoặc Configuration_Profile có schema rõ ràng, version, scope (`environment`, `tenant_id`, `workflow_type`, `workflow_version`) và giá trị mặc định được khai báo tường minh.
3. WHEN một cấu hình được thay đổi, THE BPM_Platform SHALL validate schema, type, range, dependency và security constraint trước khi cho phép publish cấu hình đó.
4. WHEN runtime áp dụng cấu hình mới cho command path, THE BPM_Platform SHALL gắn `config_version` hoặc `policy_version` vào event/audit/decision metadata để replay, điều tra và rollback có thể tái hiện đúng quyết định đã xảy ra.
5. IF cấu hình mới không hợp lệ, thiếu giá trị bắt buộc hoặc vi phạm invariant an toàn, THEN THE BPM_Platform SHALL từ chối cấu hình đó và tiếp tục dùng version cấu hình hợp lệ gần nhất.
6. WHERE cấu hình ảnh hưởng tới deterministic `decide()`/`evolve()`, THE BPM_Platform SHALL truyền cấu hình vào core như input đã version hóa; domain-core SHALL NOT đọc environment variable, database, file, clock hoặc remote config trực tiếp.
7. THE BPM_Platform SHALL hỗ trợ cấu hình phân cấp với thứ tự override tường minh: platform default → environment → tenant → workflow type → workflow version → instance/policy override được phê duyệt.
8. WHEN một cấu hình được tạo, sửa, publish, rollback hoặc retire, THE BPM_Platform SHALL ghi audit gồm actor/service, scope, version, thời điểm, diff/hash và lý do thay đổi.
9. THE BPM_Platform SHALL phân biệt rõ configurable runtime value với compile-time constant bất biến như Protobuf field number, WIR schema version, enum stable tag và protocol compatibility guard; các hằng số bất biến này SHALL được tài liệu hóa và kiểm bằng schema/contract gate, không được trộn với policy động.
10. THE BPM_Platform SHALL cung cấp cơ chế fallback/rollback an toàn cho cấu hình đã publish; rollback SHALL tạo một version mới trỏ lại nội dung đã biết hợp lệ thay vì sửa/xóa lịch sử cấu hình.

## Đề xuất MVP cho Phase 1

Do phạm vi nền tảng rất lớn, đề xuất phạm vi MVP cho Phase 1 tập trung vào lõi tạo giá trị và giảm rủi ro kỹ thuật sớm nhất:

- **P0 — BPMN AOT Compiler + BPMP Engine** (Requirements 1, 3, 4, 5 và 6 tối thiểu): WIR, deterministic core, Event Store/replay, local WASM và remote worker protocol. Compiler và engine SHALL dùng cùng canonical WIR schema và đều được viết bằng Rust.
- **P0.5 — định dạng không thể vá muộn:** TenantId, WorkflowVersion, key scope, encryption metadata, `config_version`/`policy_version`, authoritative authz/idempotency và outbox event envelope phải có trong schema v1 dù KMS/governance production adapter chưa hoàn tất.
- **P1 — Human Runtime + API Gateway** (Requirements 2, 7 và 14 tối thiểu): Human Runtime và API Gateway có thể dùng Go nhưng mọi workflow transition SHALL gọi Rust BPMP Engine qua versioned gRPC contract, không tự diễn giải WIR.
- **P1 — Read Models & Projections tối thiểu** (Requirement 20): consumer độc lập để không scan event log cho query; hỗ trợ rebuild từ committed event.
- **P1 — Dynamic Configuration tối thiểu** (Requirement 23): schema cấu hình versioned, validation, audit và lookup theo tenant/workflow cho SLA, retry, timeout, rate-limit, quota và feature flag; domain-core chỉ nhận cấu hình qua input đã version hóa.

Trước khi go-live với dữ liệu PII thật, Phase 2 SHALL hoàn tất clustering/HA, KMS production adapter, Data Governance/dual-control (Requirements 11, 15 và 22), Dead-Letter Replay (Requirement 21), model checking và failure/chaos gates liên quan. Functional MVP P0/P1 chỉ được dùng dữ liệu giả lập nếu Phase 2 chưa hoàn tất.

Reactive Push Cockpit/Progressive BPMN được ưu tiên sau production correctness; **Process Copilot (Requirement 9) triển khai sau cùng** và không được là dependency bắt buộc của command path. Schema evolution nâng cao có thể triển khai sau, nhưng version pinning cơ bản (Requirement 19.1, 19.2, 19.4) phải có từ P0 vì ảnh hưởng trực tiếp tới event format.

Riêng các quyết định **TenantId trong định dạng event/stream**, encryption at rest, retention marker và Compensation_Ledger phi-PII của Requirement 22 nên được chốt ngay từ Phase 1 vì thay đổi muộn sẽ phá định dạng log và migration sẽ rất tốn kém.

## Notes

- Các nhóm 12–18, 22 và 23 là **cross-cutting non-functional/data-governance/configuration requirements** áp dụng xuyên suốt mọi thành phần chức năng (nhóm 1–11).
- BPMN_Compiler và Event_Store là các thành phần parse/serialize; do đó đã bổ sung **round-trip acceptance criteria** (1.11, 4.9) — đây là điểm bắt buộc để phát hiện lỗi sớm.
- Tài liệu này cố ý giữ ở mức cao (high-level) và bao phủ rộng; chi tiết kỹ thuật sẽ được cụ thể hóa ở giai đoạn Design.
