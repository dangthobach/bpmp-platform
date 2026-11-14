# Backend implementation standards: UUID, health, metrics và cleanup

Trạng thái: **yêu cầu triển khai đích**, bổ sung 2026-11-10; chưa xác nhận toàn bộ backend hiện tại đã đáp ứng. Áp dụng cho mọi backend deployable gồm HTTP/gRPC services, Engine host và worker chạy lâu dài. Library/CLI chạy một lần không tự mở HTTP listener; health của job được theo dõi qua job status/exit code.

## 1. Entity IDs dùng UUID

- Mọi entity ID mới do platform sở hữu dùng UUID; PostgreSQL dùng kiểu native `uuid`, PK/FK tenant-scoped dùng cùng kiểu. API JSON/Protobuf string là biểu diễn wire của UUID, không có nghĩa entity ID được phép là chuỗi bất kỳ.
- UUID sinh một lần ở trusted application boundary bằng thư viện đã pin; retry giữ nguyên ID/command intent. Deterministic core nhận UUID như input, không đọc clock/random để tự sinh ID trong `decide`/`evolve`. Phiên bản UUID được chốt trong implementation slice theo supported library và workload; không trộn generator tùy ý trong mỗi service.
- Business code/key, BPMN element key, external provider ID, Kafka offset, digest và idempotency key không phải entity ID. Chúng là fields riêng, không ép thành UUID hoặc dùng thay surrogate UUID. `createdBy` có thể là external workload/IdP subject reference; internal actor entity vẫn có UUID.
- Existing Engine/HTTP contract đang nhận opaque string IDs cần compatibility mapping/migration có alias/lineage; không thay historical IDs hoặc invalidate commands đang pending bằng một regex UUID mới. Entity mới UUID là quy tắc bắt buộc, legacy string support là migration concern có danh mục và cutover gate.
- Validate UUID tại ingress và storage adapter; không tự coi UUID như secret hoặc authorization proof. New resource UUID vẫn phải kiểm tenant/actor scope. ID uniqueness không thay thế business-key uniqueness hoặc optimistic locking.

## 2. Health endpoints trên mọi backend service

Mỗi long-running deployable có management HTTP listener dù business API chỉ là gRPC. Target routes:

| Endpoint | Ý nghĩa và status |
|---|---|
| `GET /healthz` | Liveness của process/event loop: 200 khi sống, 503 khi không thể hoạt động; không gọi mọi dependency qua mạng mỗi probe |
| `GET /readyz` | Readiness theo role: 200 khi có thể nhận đúng loại traffic; 503 khi bootstrap/migration/config/security freshness/required local store chưa sẵn sàng |
| `GET /livez` | Alias tương thích nếu deployable đã dùng route này; không bỏ probe cũ trước rollout manifest tương thích |
| `GET /metrics` | Telemetry riêng cho monitoring; không thay health/readiness |

`/healthz` là endpoint bắt buộc mới, không chỉ đổi tên tài liệu của `/livez`. Giữ gRPC health protocol khi có nhưng vẫn cung cấp HTTP management surface. Startup/readiness policy có deadline phù hợp startup/replay; liveness không restart hàng loạt vì Report/MDM/Kafka tạm down nếu service vẫn có thể vận hành an toàn.

Dependency matrix được khai báo theo service: Engine follower có thể ready cho peer/read/forward role khi policy cho phép; không yêu cầu mọi node là leader. Kafka outage sau durable outbox commit không tự làm command API unready nếu backlog còn trong safety budget. Report có thể phục vụ stale query theo report policy, nhưng không được gọi fresh. MDM cached consumers tuân freshness/authorization lease, không readiness giả khi cache không đủ.

Probe bounded/time-limited, không SQL full scan hoặc API fan-out; quản lý dependency status qua worker/supervisor và bounded checks. Response tối thiểu `{status, service, version}`; diagnostic dependency details chỉ nội bộ/authorized, không secret/PII/DSN. Liveness/readiness cho orchestrator không phụ thuộc browser JWT; management network policy/private bind kiểm soát reachability. Port/path đều cấu hình bootstrap, không expose qua public business BFF mặc định.

**Current evidence:** `go/platform/health/handler.go` có `/livez` và `/readyz`; một số services tự đăng ký listener; Rust Governance có health routes tương tự. Đây chưa phải bằng chứng mọi deployable có `/healthz`. Cần service-by-service wiring/probe tests trước đóng gate.

## 3. Metrics trên mọi backend service

Mọi service có `/metrics` trên management listener hoặc telemetry listener riêng với scrape configuration rõ. Chuẩn format backend thống nhất tại telemetry contract; nếu target scraper cần Prometheus/OpenMetrics thì phải có exporter/adapter và content negotiation phù hợp, không gọi JSON payload hiện hữu là Prometheus exposition.

Baseline `go/platform/telemetry/metrics.go` hiện có JSON metrics handler và listener; tái sử dụng accounting/instrumentation phù hợp, nhưng còn cần qualification về format/coverage cho toàn platform. Rust và gRPC paths cũng phải có metrics, không chỉ Gateway.

| Nhóm | Metrics bắt buộc theo service role |
|---|---|
| Request | Count/error và latency distribution theo route template/RPC method + status; in-flight, timeout, rate-limit |
| Runtime | Process uptime, CPU/memory/GC hoặc allocator/runtime theo ngôn ngữ, pool/queue saturation |
| DB | Pool utilization/wait, query/transaction duration/error; lock/CAS conflicts; không export SQL/bind values |
| Kafka/outbox | Consumer lag/checkpoint age, publish failures, pending count/bytes, oldest pending age, delivery retries/DLQ |
| Worker | Job success/failure, lease lost, retry exhaustion, duration, active/queued jobs |
| MDM/Report | Cache hit/miss, snapshot/sync lag, source freshness, ingest/query/export throughput/latency |
| Cleanup | Last successful run, duration, rows/bytes deleted or archived, eligible/backlog count, failure/skip reason |

Metric names/units và histogram buckets phải được review thống nhất; không labels theo UUID/entityId, raw URL, commandId, arbitrary tenant, email hoặc business key. High-cardinality/debug per-entity nằm trong scoped logs/traces. Metrics exporter lỗi không được retry business command hoặc làm thay đổi kết quả domain.

## 4. Cleanup định kỳ: owner-local scheduled jobs

Mỗi service sở hữu bảng outbox/log chịu trách nhiệm một **cleanup job định kỳ được cấu hình và bật khi rollout**. Không chỉ có câu SQL chạy tay. Mỗi table family cần retention policy, eligibility predicate, schedule/timezone, batch size, runtime budget, lease/lock timeout, retry bounds, metrics và runbook.

Có thể dùng scheduler nội service với durable distributed lease/fencing hoặc orchestrator CronJob với concurrency guard. Chọn đúng một cơ chế cho cùng job; process restart/multiple replicas không tạo delete storm. Policy từ validated Configuration snapshot; bootstrap khai báo scheduler storage/credential. Không hardcode lịch/retention trong handler. Runbook/env config phải điền giá trị cụ thể trước rollout; không chọn một số ngày chung cho mọi loại log.

| Dữ liệu | Điều kiện đủ để cleanup | Điều kiện phải giữ |
|---|---|---|
| Published outbox | Durable broker ACK + persisted publication checkpoint, qua delivery retention và recovery/republish obligations; sequence dưới safe cutoff của mọi local reader liên quan | Pending/in-flight/leased, retry/DLQ unresolved, chưa chắc ACK, local consumers/checkpoints hoặc restore procedure còn cần |
| Technical request/job logs | Terminal state, quá retention, không incident/hold đang mở; archive trước nếu policy yêu cầu | Active job/lease, incident chưa đóng, retry evidence chưa đủ |
| Immutable audit/security logs | Archive/purge theo approved retention, evidence integrity và legal-hold/Governance | Không áp TTL “log thường” hoặc mutable soft delete; hold/evidence obligations chưa hết |
| Inbox/dedup/command receipts | Policy riêng, vượt max redelivery + replay/backfill + client retry window, có safe cut và durable dedup semantics thay thế khi cần | Không xóa cùng outbox mặc định; TTL sai có thể thực thi command/aggregate lại |
| Report raw/derived logs | Retention/correction/rebuild window và verified archive/checkpoint | Source data cần reconcile, replay hoặc report đã chốt/hold |
| Application stdout/file logs | Log-agent/backend retention hoặc log rotation được cấu hình | Không để service tự xóa đường dẫn tùy ý; log rotation không thay DB cleanup |

Outbox giữ delivery state; audit evidence là dữ liệu khác. Không giả mọi Kafka consumer đã xử lý chỉ vì producer nhận broker ACK. Giữ raw event archive/snapshot hoặc owner export cut đủ cho rebuild sau Kafka retention. Nếu xóa published outbox mà archive/broker retention không đủ recovery, phải block cleanup hoặc thay recovery contract trước.

Engine outbox đặc biệt có local boundary/task/worker readers ngoài Kafka publisher. Safe cutoff phải xét tất cả checkpoint/reference liên quan và Raft snapshot/restore/replay obligations. Mutation/pruning qua authoritative storage/Raft mechanism; **không cron trực tiếp xóa key/file RocksDB**. PostgreSQL outbox của từng context có local eligibility riêng, không suy từ Engine.

### Thuật toán vận hành

1. Đọc retention policy version; acquire job lease/fencing token; chốt trusted `cutoffAt` và safe sequence/checkpoint watermark cho run.
2. Chọn bounded batch có indexed predicate, ưu tiên tenant/time/sequence stable cursor. Kiểm terminal/ACK/hold/lease state ngay trong transaction khi claim/delete, không dùng danh sách ID cũ rồi xóa blind.
3. Archive trước nếu required; chỉ coi archive thành công khi durable acknowledgement/checksum/manifest đã xác nhận. Retry không tạo archive duplicate hoặc delete dữ liệu chưa archive.
4. Physical purge technical rows theo owner protocol trong transaction ngắn; batch commit, record cursor/run summary và metrics. Soft-delete business rows không tự cho phép purge.
5. Yield khi chạm time/row/IO budget; release lease hoặc resume lần sau. Nếu lease lost thì stop/fence trước effect mới; lỗi không advance checkpoint giả.
6. Với table lớn, cân nhắc time partition drop chỉ khi **mọi row trong partition** đủ điều kiện và mọi hold được xử lý; không DROP partition chỉ vì partition cũ. Schema/index/vacuum/storage reclaim do owner vận hành, không hứa DELETE lập tức trả disk cho OS.

Run metadata có runId UUID, owner/job name, policy version, cutoff/watermark, start/end, counts, cursor, status và error code. Metadata không chứa payload PII/secret; run logs có retention riêng để không tăng vô hạn. Có dry-run và operator-triggered run **cùng eligibility rules**, không public “delete all logs” API. Cleanup không nằm trên request path.

## 5. Rollout checklist và acceptance

- Entity schema/API mapping: UUID cho new IDs và FK, audit/soft-delete/version; explicit legacy alias plan; UUID creation ngoài deterministic core.
- Mỗi backend deployment có `/healthz`, `/readyz`, `/metrics`; preserve old health aliases trong migration; probe/management network/format config render đúng.
- Tests chứng minh process alive vs dependencies unready khác nhau; probe timeout bounded; metrics không leak PII/high-cardinality IDs và lỗi telemetry không đổi command outcome.
- Cleanup schedule enabled + policy validated; hai replicas không double-execute; pending/leased/held outbox rows không bị xóa; checkpoint floor và archive verified; crash/resume/delete-vs-publish race an toàn.
- Load test cleanup không làm business latency/DB lock/WAL tăng quá budget; dashboard/alerts cho cleanup overdue/failure và outbox oldest pending age.
- Backup/restore rehearsal khôi phục đủ outbox/dedup/checkpoints; expired Kafka/archive retention có documented resnapshot path. Không đóng cleanup gate chỉ vì row count giảm.

Đợt này chỉ cập nhật yêu cầu thiết kế. Việc wire endpoint/exporter, UUID migrations và scheduled cleanup workers phải được triển khai và có evidence từng service trước khi đánh dấu completed.
