# Kế hoạch refactor Notification Service

Trạng thái: đề xuất, chờ duyệt (đã đồng bộ điểm giao với [authz-refactor-plan.md](authz-refactor-plan.md)). Ngày cập nhật: 2026-11-27.

Phạm vi: `apps/go/notification-service`, `db/notification-service`, `contracts/proto/bpmp/notification/v1`, `apps/web/notification-mfe`, notification proxy của `apps/go/api-gateway`, CI và tài liệu liên quan.

## 1. Mục tiêu và nguyên tắc

Mục tiêu:

- Áp dụng các quyết định đã chốt: River thay hàng đợi tự viết, `sony/gobreaker/v2` thay breaker tự viết, `wneessen/go-mail` thay `net/smtp` và bộ dựng MIME tự viết, Go 1.27.4 cho toàn workspace.
- Xóa code chết, code trùng với thư viện hoặc với `go/platform`, và dữ liệu giả trong MFE.
- Sửa các lỗi nghiêm trọng đã phát hiện khi refactor đi qua đúng vùng code đó.

Nguyên tắc triển khai:

1. Có lưới an toàn trước khi sửa. PR1 thêm test tích hợp PostgreSQL và test đặc tả hành vi (characterization test) trước khi động vào store, transaction, queue, breaker, SMTP.
2. Mỗi PR một mục đích. PR không đổi hành vi tách khỏi PR sửa lỗi. Mỗi lỗi được sửa đi kèm một test tái hiện lỗi, và test đó phải fail trước khi sửa.
3. Migration chỉ thêm file mới (`002_...`, `003_...`), không sửa `001` vì `db/scripts/migrate.sh` kiểm checksum từng file đã chạy. Xóa bảng theo kiểu expand/contract: một PR ngừng dùng bảng, PR sau mới xóa bảng.
4. Mỗi PR revert được độc lập. Nâng dependency không gộp chung với PR đổi logic.
5. Dùng thư viện đã chốt và package có sẵn trong `go/platform`. Chỉ giữ code tự viết khi không có thư viện phù hợp: định dạng số thập phân chính xác (`formatDecimalWithGrouping`), đếm segment SMS (`CalculateSMSSegments`), bỏ dấu tiếng Việt (`StripAccents`).

### Điểm giao cần đồng bộ với kế hoạch Authz (`docs/authz-refactor-plan.md`)

- **PR0 ↔ Authz `WP0.3`:** `PR0` nâng workspace lên Go `1.27.4` và thêm job `notification-quality` trong `.github/workflows/ods-foundation.yml`. Authz `WP0.3` mở rộng cùng workflow cho các module Go còn thiếu để tránh conflict.
- **PR3 (mục 5) ↔ Authz `WP1.5` (S4, mức P0):** Sửa gRPC fail-closed (`grpcapi.NewServer` trả lỗi khi `verifier == nil`) được đẩy lên xử lý ngay tuần đầu trong Authz `WP1.5`. Khi vào `PR3`, giữ nguyên ràng buộc này và bổ sung mTLS + middleware `go/platform`.
- **PR9 (mục 1) ↔ Authz `WP3.2`, `WP3.5`, `WP4.1`, `WP4.3`:** Gom capability `notification.*`, đăng ký `authorization-owner-manifest.json` vào Authz catalog và chuyển sang `go/platform/authz/actorproof` + Gateway Route Registry.

## 2. Hiện trạng đo được

Đo trên bản sao tạm với Go 1.26.5, vì máy local không tải được toolchain 1.27.3 (`GOSUMDB=off` chặn xác minh `golang.org/toolchain`).

| Hạng mục | Kết quả |
|---|---|
| build, vet, test | Pass |
| Coverage | notifyctl 56.4%, application 58.6%, crypto 74.0%, grpcapi 23.0%, httpapi 18.8%, provider 53.4%, queue 50.3%, renderer 62.1%, schema 61.4%. postgres, bootstrap, cmd: 0% |
| `deadcode` (chỉ tính code production) | Toàn bộ `MemoryStore` (`application/memory_store.go`, 1.170 dòng), `MemoryQueue` (`queue/queue.go:77-241`), `Handler.SetClock`, `StaticSecretResolver.ResolveSecret`, `EnqueueTx`, `CompiledSchema.Properties` |
| `deadcode -test` (chết thật) | `StaticSecretResolver.ResolveSecret` (`provider/driver.go:34`), `EnqueueTx` (`queue/queue.go:269`), `CompiledSchema.Properties` (`schema/schema.go:120`) |
| `staticcheck` v0.8.2 | 3 lỗi ST1005: `cmd/notification-intake/main.go:165`, `provider/inapp_mock.go:167`, `renderer/sanitizer.go:193` |
| Lỗi bị nuốt bằng `_ =` | 28 chỗ cần xử lý: `AppendAudit` 11, `queue.Fail` 4, `UpdateDeliveryOutcome` 4, `SaveCommandReceipt` 4, `AppendOutbox` 3, `AppendBreakerTransition` 1, `AppendDeliveryEvent` 1. Thêm 1 chỗ `client.Quit` chấp nhận được |
| `Store` interface | 68 method. 6 method không có caller production: `GetActiveBreakerOverride`, `ListAudit`, `SaveLayoutVersion`, `SaveRoutingPolicy`, `SaveSubjectContact`, `SaveSuppression` |
| Gọi thẳng `Store()` bỏ qua service | 12 chỗ: httpapi 8, grpcapi 2, cmd 2 |
| HTTP | 45 route. Trùng: `GET /circuit-breakers` và `GET /providers/health` dùng cùng handler (MFE chỉ gọi `providers/health`); `POST` và `PUT /preferences` (không web app nào gọi) |
| gRPC | 3 service đăng ký (`grpcapi/server.go:47-49`). Không có client nào trong repo (Go, Rust, TS) |
| `go/platform` | Notification dùng 3/14 package (actorcontext, kafkaconfig, requestmeta). Năm service Go khác đều dùng telemetry, health, servermiddleware, MTLSAuthorizer |
| `go/platform/circuitbreaker` | Breaker tự viết thứ hai (17,6 KB code, 20,7 KB test), không nơi nào import |
| `go.mod` | Chưa có `go.sum`, chưa có khối indirect (chưa từng `go mod tidy`). `go-redis` không dùng. `jsonschema/v6 v6.0.2` |
| CI | `.github/workflows/ods-foundation.yml` không có job nào build hoặc test notification-service |
| Image | Không có Dockerfile cho notification-service trong `platform/e2e` hay `platform/ods` |
| MFE | 11 hằng seed (`types.ts:636-1344`, khoảng 700 dòng) được dùng thay dữ liệu thật khi API rỗng hoặc lỗi, ở 9 file. `SendNotificationWorkspace` chỉ dùng seed. Badge ở `route.tsx:67,70` tính từ seed. 13 type export không dùng. `dist/` không có trong `.gitignore` gốc |

Ảnh hưởng dependency:

| Thư viện | Bản | Yêu cầu | Ảnh hưởng workspace | License |
|---|---|---|---|---|
| `riverqueue/river`, `riverdriver/riverpgxv5` | v0.51.2 | Go ≥ 1.26.0, pgx v5.11.0 | pgx v5.9.2 lên v5.11.0 ở 6 module khi build bằng `go.work` gốc | MPL-2.0, cần duyệt |
| `sony/gobreaker/v2` | v2.4.0 | Go ≥ 1.22, không có dependency runtime | Không | MIT |
| `wneessen/go-mail` | v0.8.2 (v0.8.1 có lỗi CRLF injection ở helper List-Unsubscribe) | Go ≥ 1.26.10, x/crypto v0.58.0, x/text v0.43.0 | x/crypto v0.53.0 lên v0.58.0, x/text v0.39.0 lên v0.43.0 | MIT |
| `microcosm-cc/bluemonday` | v1.0.27 | | Nhỏ | BSD-3-Clause |
| `santhosh-tekuri/jsonschema/v6` | v6.0.3 | | Không | Apache-2.0 |

## 3. Quyết định cần chốt

| # | Vấn đề | Đề xuất | Chặn PR |
|---|---|---|---|
| D1 | Cú pháp Handlebars | Bỏ, chỉ giữ `gotmpl/v1`, giữ chẩn đoán lỗi (`Diagnose`) cho Go template | PR8 |
| D2 | `NotificationAdminService` gRPC (21 RPC) trùng HTTP admin mà MFE đang dùng, không có client | Xóa service Admin khỏi proto và grpcapi. Giữ Dispatch và Inbox cho gọi giữa service | PR9 |
| D3 | Migration `001` đã chạy ở môi trường dùng chung (dev cluster, staging) chưa? | Nếu đã chạy: chỉ thêm `002+`. Nếu chưa: được sửa thẳng `001` trong từng PR, dev local reset volume `notification-postgres-data` | PR2 |
| D4 | 6 bảng chưa dùng | Xem mục 6 | PR2, PR5, PR9 |
| D5 | Layout, routing policy, contact, suppression có đọc nhưng không có đường ghi | Làm routing policy (MFE cần) và suppression qua unsubscribe ở PR9. Layout và contact vào backlog | PR9 |
| D6 | `go/platform/circuitbreaker` không ai dùng | Xóa ở PR riêng, owner platform duyệt | PR-P |
| D7 | Định danh application khi gọi gRPC và Kafka | gRPC: map fingerprint chứng chỉ mTLS sang application. Kafka: bắt buộc actor proof ký trong header (dùng `actorcontext` có sẵn), hoặc mỗi application một topic có ACL | PR4 |
| D8 | License MPL-2.0 (River, và `hashicorp/golang-lru/v2` nếu dùng) | Xin duyệt trước PR5 | PR5, PR8 |
| D9 | Toolchain Go 1.27.4 | Cài sẵn trong image CI và máy dev. Muốn tự tải thì proxy nội bộ phải phục vụ `golang.org/toolchain` | PR0 |

## 4. Lộ trình PR

| PR | Nội dung | Đổi hành vi | Phụ thuộc |
|---|---|---|---|
| PR0 | Go 1.27.4, dependency nền, CI job, Dockerfile | Không | D9 |
| PR1 | Test harness PostgreSQL, test đặc tả, golden test | Không (chỉ thêm test) | PR0 |
| PR2 | Xóa code chết, dọn dẹp, tách file | Không (trừ 2 route trùng không ai gọi) | PR1, D3 |
| PR3 | Dùng `go/platform`: config, telemetry, health, middleware, mTLS | Có, ở lớp transport | PR2 (fail-closed gRPC ưu tiên làm sớm ở Authz `WP1.5`) |
| PR4 | Unit of work, sửa lỗi đúng đắn và bảo mật | Có | PR3, D7 |
| PR5 | River thay `internal/queue` | Có | PR4, D8 |
| PR6 | gobreaker thay `BreakerRegistry` | Có | PR5 |
| PR7 | go-mail, secret resolver, tái sử dụng kết nối | Có | PR6 |
| PR8 | Renderer, bluemonday, Handlebars | Có | PR2, D1. Làm song song PR5-PR7 được |
| PR9 | API, capability, contract, MFE | Có | PR4, D2, D5, Authz `WP3.2` |
| PR10 | Policy động qua `runtimeconfig` | Có | PR6 |
| PR11 | Tài liệu | Không | Các PR trên |
| PR-P | Xóa `go/platform/circuitbreaker` | Không | D6 |

### PR0. Toolchain và dependency nền

Việc làm:

1. Đổi `go 1.27.3` thành `go 1.27.4` trong `go.work` và 10 `go.mod`.
2. Đổi `go-version: 1.27.3` trong `.github/workflows/ods-foundation.yml` (dòng 74, 104, 132), base image trong 6 file `platform/e2e/Dockerfile.*` và `platform/ods/Dockerfile.authoring-service`.
3. Cập nhật baseline trong `microservices-architecture.md`, `docs/ods-refactor-progress.md`, ADR-013.
4. notification-service: `go mod tidy` (tạo `go.sum`, khối indirect, tự bỏ `go-redis`), nâng `jsonschema/v6` lên v6.0.3.
5. Nâng trước pgx v5.11.0, x/crypto v0.58.0, x/text v0.43.0 ở mọi module đang dùng chúng. Lý do: build bằng `go.work` gốc (CI, dev) và build image bằng `platform/e2e/*.go.work` riêng từng service phải ra cùng phiên bản. Làm vậy thì PR5 và PR7 không mang theo thay đổi dependency ẩn.
6. Thêm job `notification-quality` vào CI theo mẫu `authoring-quality` (dòng 81-107): service `postgres:17.6`, biến DSN, `go vet`, `staticcheck`, `deadcode -test`, `go test -race`. Pin phiên bản tool.
7. Thêm `platform/e2e/notification.go.work` và `platform/e2e/Dockerfile.notification-service` build 4 binary (api, dispatcher, intake, scheduler), theo mẫu `Dockerfile.case-service`.

Kiểm chứng: build và test cả 10 module, build image notification.

Ngoài phạm vi: authoring-service cũng chưa có `go.sum`.

### PR1. Lưới an toàn (chỉ thêm test)

Việc làm:

1. Harness PostgreSQL trong `internal/testsupport`: đọc `NOTIFICATION_TEST_POSTGRES_DSN`, mỗi test tạo một schema riêng, chạy các file trong `db/notification-service/migrations` theo thứ tự và bọc transaction giống `migrate.sh`, xóa schema khi xong. Thiếu DSN thì skip khi chạy local, fail khi chạy CI.
2. Contract test cho 62 method `Store` có caller, chạy trên PostgreSQL. Ưu tiên: idempotency (`FindRequestByIdempotency` và `CreateRequestWithDeliveries`), `UpdateDeliveryOutcome`, `ActivateDueScheduledReleases`, phân trang `QueryDeliveries` và `ListTemplates`, trigger append-only và immutable của migration.
3. Chuyển test của application, httpapi, grpcapi từ `NewMemoryStore()` sang store PostgreSQL. Test nào fail sau khi chuyển cho thấy chỗ `MemoryStore` khác PostgreSQL: ghi thành issue, sửa ở PR4, không sửa logic trong PR này.
4. Golden test:
   - HTTP: 45 route, chụp status và cấu trúc JSON.
   - Renderer: subject, text, html của bộ template mẫu (cả `gotmpl/v1` và `handlebars/v1` hiện tại).
   - SMTP: giữ server giả `serveMinimalSMTP` (`provider_test.go:89`), chụp header (From, To, Subject đã mã hóa, Message-ID, List-Unsubscribe, Content-Type) và cấu trúc multipart để so sánh sau khi chuyển sang go-mail.
   - Router: quyết định chọn provider và failover cho các kịch bản hiện có.
5. Lưu baseline vào `testdata/baseline/`: coverage, kết quả deadcode và staticcheck, danh sách route, RPC, topic, loại outbox event.

Kiểm chứng: toàn bộ test xanh trên CI có PostgreSQL, coverage package postgres lớn hơn 0.

### PR2. Xóa code chết và dọn dẹp

Xóa:

- `StaticSecretResolver` (`provider/driver.go:31-41`), `EnqueueTx` (`queue/queue.go:269`), `CompiledSchema.Properties` (`schema/schema.go:120`).
- `application/memory_store.go` (1.170 dòng), sau khi PR1 đã chuyển test.
- `ListAudit` khỏi `Store`: trùng `ListAuditFiltered`, chỉ test dùng.
- Route `GET /v1/notifications/circuit-breakers` (MFE dùng `providers/health`) và `POST /v1/notifications/preferences` (giữ `PUT`). Đã kiểm: MFE, gateway và các web app khác không gọi hai route này.
- `maxInt` (`renderer/handlebars.go:933`), thay bằng builtin `max`. Sửa 3 lỗi ST1005.
- MFE: 13 type export không dùng trong `types.ts`, thư mục `dist/`. Thêm `/apps/web/notification-mfe/dist/` vào `.gitignore` gốc (file này đã liệt kê dist của 5 MFE khác).

Tách file, chỉ di chuyển code, không sửa logic: `application/service.go` (2.405 dòng), `application/extensions.go` (1.193), `postgres/store.go` (1.931), `httpapi/handler.go` (1.006), `grpcapi/server.go` (955). Tách theo use case: templates, providers, deliveries, dispatch, inbox, triggers, audit. Review bằng `git diff --color-moved`.

Kiểm chứng: golden test của PR1 không đổi (trừ 2 route đã xóa), `deadcode -test` rỗng, `staticcheck` 0 lỗi.

### PR3. Dùng `go/platform` cho config, telemetry, health, middleware, mTLS

Làm theo mẫu `apps/go/configuration-service/cmd/configuration-service/main.go` (telemetry dòng 60, mTLS và interceptor dòng 188-235, health và HTTP middleware dòng 242-275).

1. Config JSON có kiểu, nạp qua `-config` hoặc `NOTIFICATION_SERVICE_CONFIG`, có `Validate()`. Thay các `os.Getenv` rải rác trong `bootstrap/runtime.go` và 4 `cmd/*`. Nối `TestAllowedEmails` (hiện `runtime.go:84-96` chỉ đọc domain). Các PR sau thêm phần config của mình vào struct này, không thêm biến môi trường lẻ.
2. Cả 4 process: `platformtelemetry.Start` kèm JSON logging, `/livez` và `/readyz` bằng `platformhealth.Handler` (ping PostgreSQL, Kafka), metrics server.
3. HTTP: `servermiddleware.NewHTTP` (timeout, giới hạn body, security header, rate limit) và `platformtelemetry.HTTPHandler`. `http.Server` đặt đủ `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `MaxHeaderBytes` (hiện chỉ có `ReadHeaderTimeout`).
4. gRPC: `servermiddleware.NewMTLSAuthorizer` (allowlist fingerprint và method), `servermiddleware.UnaryServerInterceptor`, `platformtelemetry.GRPCServerStatsHandler`, `MaxRecvMsgSize`, `platformgrpcserver.RegisterReflection` theo config.
5. Sửa trong lúc làm:
   - gRPC fail-closed (ưu tiên hoàn thành sớm ở Authz `WP1.5`): `grpcapi/server.go:56` hiện trả về identity "workload" có đủ capability khi verifier là nil. `grpcapi.NewServer` phải trả lỗi khi thiếu verifier.
   - `clientIP` (`httpapi/extensions.go:20-28`) lấy IP đầu tiên trong `X-Forwarded-For` do client gửi, nên IP trong audit giả mạo được. Mặc định dùng `RemoteAddr`, chỉ tin header do gateway đặt khi peer là gateway đã xác thực mTLS.
   - Lỗi HTTP trả theo `requestmeta.WriteProblem` (RFC 7807) như các service khác.
6. Xóa `loadMTLSConfig` tự viết trong `cmd/notification-api/main.go`.

Kiểm chứng: gọi gRPC không có cert hoặc sai fingerprint bị từ chối; thiếu verifier thì server không khởi động; header XFF giả không vào audit; `/readyz` báo lỗi khi PostgreSQL tắt; test validate config.

### PR4. Unit of work và sửa lỗi

1. Unit of work: `Store.InTx(ctx, func(tx TxStore) error) error` dùng `pgx.BeginTxFunc`. Mọi lệnh đổi trạng thái ghi state, audit, command receipt, outbox, delivery event trong cùng một transaction. Xử lý 28 chỗ `_ =`: trong transaction thì trả lỗi; chỗ nào thật sự best-effort thì log có cấu trúc và tăng metric.
2. Gộp logic resolve dùng chung cho `SubmitNotification` và `PlanNotification` (Plan hiện tự làm lại ở `extensions.go:448-593`) trước khi sửa lỗi, để mỗi lỗi chỉ sửa một chỗ. Test đặc tả: Plan và Submit cho cùng quyết định với cùng đầu vào.
3. Sửa lỗi, mỗi lỗi một test tái hiện:

| Lỗi | Vị trí | Cách sửa |
|---|---|---|
| Phiên bản ghim không kiểm status, gửi được bản DRAFT | `service.go:1261` | Chỉ nhận bản PUBLISHED |
| Caller tự chọn category tùy ý | `service.go:1245` | Chỉ nhận category mà application được phép |
| Quota chạy `COUNT(*)` mỗi request | `service.go:1224` | Bảng counter theo cửa sổ phút, `INSERT ... ON CONFLICT DO UPDATE ... RETURNING` trong cùng transaction |
| `UpdateDeliveryOutcome` không kiểm `row_version` hay lease | `postgres/store.go:1551` | Optimistic concurrency theo `row_version` |
| `ClaimPendingOutbox` không khóa dòng | `postgres/store.go:232` | `FOR UPDATE SKIP LOCKED` |
| Intake ghi inbox trước khi submit, retry thì mất message | `cmd/notification-intake/main.go:101` | Inbox và submit chung một transaction. Lỗi vĩnh viễn đẩy sang DLQ. Commit offset sau khi thành công |
| Scheduler đánh dấu đã publish khi biến topic rỗng | `cmd/notification-scheduler/main.go:108` | Kiểm topic khi khởi động, thiếu thì dừng |
| Kích hoạt release hẹn giờ không thay release ACTIVE cũ, không phát invalidation | `ActivateDueScheduledReleases` | Supersede và ghi outbox invalidation trong cùng transaction |
| Một khóa dùng cho cả AES-GCM và HMAC | `bootstrap/runtime.go:55` | Tách hai khóa bằng `crypto/hkdf` của stdlib (Go 1.24+) |
| `SimulateProviderFailure` chặn theo denylist, biến môi trường rỗng vẫn chạy được | `application/extensions.go:646` | Allowlist `local`, `dev`, `test`. Rỗng thì chặn |
| `inAppSeq` đếm trong bộ nhớ, mỗi replica một dãy, restart về 0 | `service.go:48, 175-178` | Lấy từ cột identity của `inapp_messages` (migration mới). Xóa `inAppSeq` và `Service.mu` |
| UUID sai định dạng bị đổi thành UUID SHA1 | `parseUUID`, `mustOrDeterministicUUID` | Validate ở biên, trả 400 hoặc `InvalidArgument` |
| Link unsubscribe dùng host cố định `notifications.bpmp.local` và delivery ID trần, không có endpoint | `service.go:1605, 2285`, `cmd/notifyctl/main.go:127, 219` | Base URL lấy từ config, token ký HMAC. Chưa có endpoint (PR9) thì không phát header List-Unsubscribe |
| Application không gắn với caller đã xác thực | grpcapi, intake | Theo D7 |

Lỗi mất message (commit ở `service.go:1433` rồi mới enqueue ở `service.go:1457`, resend ở `service.go:2105`) được đóng ở PR5 bằng `InsertManyTx` của River trong cùng transaction. Nếu cần đóng sớm hơn, có thể gọi `EnqueueTx` (đang là code chết) bên trong `InTx`, nhưng đề xuất chờ PR5 để không làm hai lần.

Kiểm chứng: lỗi ở bước audit thì không còn state hay outbox nào được ghi; hai goroutine cùng cập nhật một delivery thì chỉ một bên thắng; test cho từng lỗi trong bảng.

### PR5. River thay `internal/queue`

Điều kiện: D8 đã duyệt, PR4 đã có `InTx`.

1. Thêm `riverqueue/river v0.51.2` và `riverdriver/riverpgxv5 v0.51.2`.
2. Migration:
   - Xuất SQL bằng `river migrate-get --line main --version N --up` cho từng version N từ 2 đến version cao nhất có trong v0.51.2, mỗi version một file (`00X_river_vNNN.sql`).
   - Không gộp thành một file. `migrate.sh` bọc mỗi file trong một transaction (dòng 145-156), và tài liệu River ghi rõ migration 4 và 6 không chạy chung một transaction được.
   - Bỏ version 1 (bảng quản lý migration nội bộ của River), theo hướng dẫn của River khi dùng framework migration khác.
   - Thêm `river_job`, `river_leader`, `river_queue` vào danh sách bảng bắt buộc trong `db/scripts/verify-schema.sh` (dòng 130-160), nếu không `notification-migrate` ở compose local sẽ báo lỗi.
   - Đặt `river.Config.Schema` khớp schema mà `migrate.sh` dùng.
   - Mỗi lần nâng River sau này phải xuất thêm file cho version migration mới.
3. Job kind `notification.dispatch`, args chỉ gồm `tenant_id` và `delivery_id`. Payload vẫn nằm mã hóa trong `delivery_payloads`.
4. Insert bằng `client.InsertManyTx(ctx, tx, params)` bên trong `InTx` của Submit và Resend. `InsertOpts`: `Queue` theo lane, `Priority` 1-4, `ScheduledAt` theo `send_at`, `MaxAttempts` theo policy, `UniqueOpts{ByArgs: true}` để không tạo job trùng.
5. Mỗi lane (`transactional`, `normal`, `bulk`) là một queue có `MaxWorkers` lấy từ config. Giới hạn số lần gửi đồng thời theo provider bằng `go/platform/bulkhead`. Thay vòng xử lý tuần tự 25 job trong lease 30 giây (`service.go:1477`).
6. Worker:
   - Quá `expires_at` thì `river.JobCancel`.
   - THROTTLED có Retry-After thì `river.JobSnooze(d)`. Snooze không tăng số attempt, nên luôn kiểm `expires_at` để không snooze vô hạn.
   - RETRYABLE thì trả lỗi và để `RetryPolicy` riêng (exponential, jitter, không vượt `expires_at`) xử lý, thay backoff tự viết ở `service.go:1723`.
   - PERMANENT hoặc AUTH_FAILED thì `JobCancel` và cập nhật delivery.
   - Thành công thì `river.JobCompleteTx` trong cùng transaction với `UpdateDeliveryOutcome` và outbox.
   - Đặt `Timeout()` cho worker, `RescueStuckJobsAfter`, `CompletedJobRetentionPeriod`. Khi tắt process, job đang chạy trả `JobSnooze(0)`.
7. Periodic job do River leader lên lịch: kích hoạt release đến hạn, thay vòng lặp ở `cmd/notification-scheduler/main.go:70`. Outbox relay vẫn là vòng lặp trong `notification-scheduler`, dùng `SKIP LOCKED` từ PR4.
8. Chuyển đổi môi trường đang chạy: dừng dispatcher cũ, chạy migration River, enqueue lại mọi delivery chưa ở trạng thái cuối (`UniqueOpts` chặn trùng), bật dispatcher mới. Ngừng ghi `notification_dispatch_jobs`. Migration xóa bảng này và cập nhật `verify-schema.sh` nằm ở một PR sau, khi River đã chạy ổn.
9. Xóa `internal/queue` (400 dòng và test, gồm `MemoryQueue`), interface `queue.DispatchQueue`, accessor `Service.Queue()`.

Kiểm chứng bằng `rivertest` và test tích hợp:

- Transaction rollback thì không có job.
- Submit trùng idempotency key thì chỉ có một job.
- Kill worker giữa chừng thì job được rescue, trạng thái delivery cuối cùng nhất quán.
- Snooze, cancel, retry đúng theo từng outcome.
- Chạy `migrate.sh` hai lần trên DB trắng (lần hai skip hết), sau đó River client khởi động và xử lý được job.

### PR6. gobreaker thay `BreakerRegistry`

1. Thêm `sony/gobreaker/v2 v2.4.0`. Registry theo khóa (tenant, provider) giữ `*gobreaker.TwoStepCircuitBreaker[struct{}]`. Dùng TwoStep vì cần đo latency và phân loại outcome sau khi gửi xong.
2. Map `BreakerPolicy` sang `gobreaker.Settings`: `MaxRequests` (số probe khi half-open), `Interval` và `BucketPeriod` (cửa sổ trượt), `Timeout` (thời gian OPEN), `ReadyToTrip` (tỉ lệ lỗi vượt ngưỡng khi `Requests` đủ số tối thiểu), `IsExcluded` (lỗi PERMANENT do dữ liệu, `context.Canceled`), `OnStateChange`.
3. Slow call: gobreaker không có chỉ số slow call riêng. Đề xuất coi call chậm hơn ngưỡng là lỗi (`done(errSlowCall)`), còn số liệu slow call để hiển thị lấy từ histogram OTel. MFE đổi nhãn tương ứng.
4. `OnStateChange` ghi `breaker_transitions` và snapshot `provider_health` qua một writer bất đồng bộ có buffer. Ghi lỗi thì log và tăng metric (hiện `service.go:113` nuốt lỗi).
5. Override thủ công dùng bảng `breaker_overrides` có sẵn. `GetActiveBreakerOverride` (hiện không ai gọi) được đọc qua cache và làm mới bằng invalidation event. FORCE_OPEN chặn trước khi gọi `Allow()`, FORCE_CLOSED bỏ qua breaker. Cách này sửa lỗi override và health chỉ có hiệu lực ở replica cục bộ (`service.go:1912, 1945`).
6. Mỗi replica có breaker riêng. Không dùng `DistributedCircuitBreaker` của gobreaker: nó cần một `SharedDataStore` có khóa và thêm một round-trip DB cho mỗi lần gửi. Ghi quyết định này vào ADR.
7. Probe: gobreaker không có API ép sang half-open. Thay "probe" bằng `test-connection` của driver (SMTP: dial, EHLO, QUIT) và một hành động "reset breaker" có audit (tạo lại instance). Khớp với endpoint `test-connection` mà MFE đang gọi.
8. Chỉ gọi `Allow()` cho candidate thực sự được dùng và luôn gọi `done` (qua `defer`). Sửa lỗi rò slot half-open khi chọn candidate (`selectHealthyCandidates`, `breaker_router.go:614`).
9. Xóa phần breaker trong `provider/breaker_router.go` (dòng 10-408: `BreakerRegistry`, `windowStats` và các type đi kèm). Giữ phần Router (dòng 409-656).

Kiểm chứng: test chuyển trạng thái với đồng hồ giả; failover sang provider dự phòng; override đặt ở replica A có hiệu lực ở replica B trong khoảng làm mới đã cấu hình.

### PR7. go-mail, secret resolver, tái sử dụng kết nối

1. Thêm `wneessen/go-mail v0.8.2`. Map TLS policy (`TLSMandatory`, `TLSOpportunistic`, `NoTLS`), kiểu SMTP auth và timeout từ config của provider.
2. Dựng message bằng `NewMsg`, `SetBodyString(TypeTextPlain, ...)` và `AddAlternativeString(TypeTextHTML, ...)`, `SetMessageIDWithValue` (gắn delivery ID để đối soát), `SetListUnsubscribeOneClick` (RFC 8058), `SetBulk` cho lane bulk. Cần DKIM thì dùng `SetDKIM` của thư viện.
3. Phân loại lỗi qua `*mail.SendError`: `IsTemp()`, `ErrorCode()` (4xx/5xx), `EnhancedStatusCode()`, `Reason`. `ErrSMTPDataClose` và `ErrAmbiguous` thành AMBIGUOUS; lỗi xác thực (535) thành AUTH_FAILED; 421, 450, 451, 452 thành THROTTLED hoặc RETRYABLE; 5xx còn lại thành PERMANENT.
4. Secret resolver: `FileSecretResolver` đọc `credentials_ref` dạng `file:<tên>` trong một thư mục mount (Kubernetes Secret hoặc Vault Agent). Kiểm tên bằng regex để chặn path traversal, cache có TTL để nhận secret được xoay vòng. Đây cũng là cách projection-service đọc secret (`cmd/projection-service/main.go:594`). Truyền resolver vào Router, sửa lỗi router nhận resolver nil (`service.go:108`) khiến `smtp.go:107` không bao giờ lấy được mật khẩu.
5. Tái sử dụng kết nối: cache sender theo provider revision (hiện `drv.Open` chạy mỗi lần gửi, `breaker_router.go:504`). Pool có kích thước bằng giới hạn bulkhead, đóng khi revision đổi hoặc idle quá lâu.
6. Xóa `BuildMIMEMessage`, `classifySMTPError`, `rejectCRLF`, các hàm ghi header tự viết, mọi chỗ dùng `net/smtp`.

Kiểm chứng: so với golden header và body của PR1 (khác biệt phải có giải thích, ví dụ boundary hay thứ tự header); giữ test chặn CRLF injection; E2E với Mailpit (subject UTF-8, multipart, List-Unsubscribe).

### PR8. Renderer, sanitizer, Handlebars

1. bluemonday v1.0.27 thay `SanitizeHTMLFragment` (`renderer/sanitizer.go:47-148`) cho hàm `safeHTML` và nội dung in-app: `UGCPolicy()`, `AllowURLSchemes("https", "mailto")`, `RequireParseableURLs(true)`, `RequireNoFollowOnLinks(true)`. Giữ `ValidateEmailHTMLLinks` vì đó là kiểm tra cho tác giả template, không phải sanitize.
2. Hiệu năng render:
   - `Render` hiện clone tới 4 template mỗi lần gọi (`renderer.go:288-330`) để gắn `formatDateTime` theo timezone người nhận. Mỗi bản clone của `html/template` chạy lại bước phân tích escape.
   - Đổi sang cache bản đã gắn func theo khóa (version, variant, timezone), có giới hạn kích thước. `Engine.cache` hiện là map không giới hạn (`renderer.go:101-147`). `time.LoadLocation` cũng được cache theo timezone.
   - Thư viện: `hashicorp/golang-lru/v2` (MPL-2.0, gộp vào lần duyệt license với River). Không được duyệt thì dùng map có trần kích thước.
3. Template func:
   - Xóa `log` (không làm gì).
   - `lookup` trùng builtin `index` của Go template, bỏ sau khi kiểm không template nào dùng.
   - `formatMoney` bỏ nhánh `float64`. Decode dữ liệu bằng `json.Decoder.UseNumber()` để số tiền giữ đúng từng chữ số.
4. Handlebars, nếu D1 chọn bỏ:
   - Go: xóa phần transpiler trong `renderer/handlebars.go`, khoảng 600 dòng (`TranspileHandlebars`, `transpileAndDiagnoseHBS`, `transpileHBSExpr`, `resolveHBSVarPath`, `tokenizeHBSArgs`, `isHBSLiteral`, `normalizeHBSLiteral`). Chuyển `Diagnose`, `diagnoseGoTmpl`, `validateHTMLTags`, `buildLineStarts`, `lineColAt` sang `diagnostics.go`. Mặc định `gotmpl/v1` (`extensions.go:89`), `service.go:419` chỉ nhận `gotmpl/v1`, bỏ nhánh transpile ở `renderer.go:168-185`.
   - DB: migration kiểm và chuyển template đang có, thêm `CHECK (syntax = 'gotmpl/v1')` cho hai cột `syntax` (migration 001 dòng 127 và 187).
   - MFE: xóa `utils/handlebarsDiagnostics.ts` (27 KB) và gọi `POST /v1/notifications/templates/validate` (đã có) với debounce. `TemplateSyntax` chỉ còn một giá trị. Token chèn biến đổi thành `{{ .data.x }}` (`TemplatesWorkspace.tsx:239, 286, 324, 377, 1046`).
   - Nếu D1 chọn giữ: sửa ADR-013 cho khớp và giữ nguyên phần code này.

Kiểm chứng: golden test renderer của PR1 không đổi, trừ phần cố ý; benchmark `Render` trước và sau; fuzz test sanitizer.

### PR9. API, capability, contract, MFE

1. Capability (đồng bộ Authz `WP3.5`, `WP4.1`, `WP4.3`):
   - Gom thành một danh mục `notification.*` duy nhất. Gateway đang yêu cầu 34 capability chi tiết. Backend chủ yếu kiểm capability thô: chuỗi `notification.manage` xuất hiện 63 lần, `notification.read` 40 lần.
   - Backend kiểm đúng capability chi tiết qua `go/platform/authz/actorproof`, khai báo `authorization-owner-manifest.json` để đăng ký vào Authz catalog (hiện chưa có capability `notification.*` nào), thêm `notification.delivery.inspect_payload` (chỉ backend có) vào gateway.
   - Test: tập capability của gateway, backend và catalog phải trùng nhau.
2. 11 endpoint MFE gọi mà backend chưa có: chốt làm hay bỏ từng cái. Gom nhóm route breaker về dạng `providers/{id}/circuit-breaker/...` (override, probe, transitions, simulate-failure).
3. Handler thôi gọi thẳng `Store()` ở 12 chỗ, chuyển sang method của service. Sau đó xóa accessor `Store()`, `Router()`, `Queue()` (`service.go:139-141`).
4. gRPC Admin (D2): xóa `NotificationAdminService` khỏi `admin.proto` và grpcapi. `buf breaking` sẽ báo lỗi, ghi ngoại lệ vì chưa có consumer. Chạy `buf generate` để cập nhật Go và Rust (`gen/rust/bpmp` hiện thiếu `notification` dù `buf.gen.yaml` có plugin prost).
5. Unsubscribe: endpoint công khai qua api-gateway (không mTLS, token ký, rate limit, hỗ trợ POST one-click theo RFC 8058), ghi vào `suppressions` (`SaveSuppression` hiện không có caller).
6. Routing policy API (MFE gọi `routing-policies`, `SaveRoutingPolicy` không có caller), theo D5.
7. Metrics: periodic job của River tổng hợp `delivery_metrics_hourly`, endpoint `metrics/query` đọc từ bảng này.
8. MFE:
   - Bỏ 11 hằng seed (`types.ts:636-1344`) và mọi chỗ dùng seed thay dữ liệu thật. Thêm trạng thái loading, empty, error.
   - `SendNotificationWorkspace` lấy application và template từ API. Badge ở `route.tsx:67,70` lấy từ API.
   - Bỏ `DefaultProcessContextCatalog` demo (`loan.approval`, `account.onboarding`) ở backend (`application/extensions.go:45-66`) và seed variable mapping ở MFE.

Kiểm chứng: contract test MFE với backend (mỗi path MFE gọi phải có route), vitest, E2E giao diện.

### PR10. Policy động qua `runtimeconfig`

`configuration.proto` đã có `CONFIGURATION_OWNER_NOTIFICATION = 9` và `NotificationPolicy notification = 17`. Dùng `runtimeconfig.Cache` và `Reloader` như các service khác để lấy retry, quota, ngưỡng breaker, số worker theo lane cho từng tenant. Thay các giá trị cứng: `maxRetryAttempts` 3 và backoff 5 giây (`service.go:84-89`), `DefaultBreakerPolicy`. Giá trị trong file config làm mặc định khi chưa nhận được snapshot.

### PR11. Tài liệu

- ADR-013: kết quả D1; tên 4 process thật; record Kafka không mang principal đã xác thực (phương án theo D7); breaker theo từng replica; River migration xuất SQL theo từng version; secret đọc từ file mount.
- Đồng bộ `schema.dbml` và `data-model-reference.md` với migration thật (migration có 36 bảng, DBML hiện có 30).
- Cập nhật tài liệu topology và catalog.

### PR-P. Xóa `go/platform/circuitbreaker`

Chỉ làm khi D6 đồng ý. Package không có nơi nào import. Owner platform duyệt.

## 5. Danh sách xóa

| Mục | Vị trí | Kích thước | PR |
|---|---|---|---|
| `StaticSecretResolver` | `provider/driver.go:31-41` | 11 dòng | PR2 |
| `EnqueueTx` | `queue/queue.go:269` | | PR2 |
| `CompiledSchema.Properties` | `schema/schema.go:120` | | PR2 |
| `MemoryStore` | `application/memory_store.go` | 1.170 dòng | PR2 |
| `ListAudit` | `Store` interface và implementation | | PR2 |
| 2 route trùng | `httpapi/handler.go` | | PR2 |
| `maxInt` | `renderer/handlebars.go:933` | | PR2 |
| 13 type không dùng, `dist/` | `notification-mfe` | | PR2 |
| `loadMTLSConfig`, `clientIP` cũ | `cmd/notification-api/main.go`, `httpapi/extensions.go` | | PR3 |
| `inAppSeq`, `Service.mu`, `parseUUID` fallback SHA1, `mustOrDeterministicUUID` | `application`, `postgres` | | PR4 |
| `internal/queue` (gồm `MemoryQueue`) | `internal/queue` | 400 dòng và test | PR5 |
| Bảng `notification_dispatch_jobs` | migration mới | | sau PR5 |
| Breaker tự viết | `provider/breaker_router.go:10-408` | khoảng 400 dòng | PR6 |
| MIME và SMTP tự viết | `provider/smtp.go` | phần lớn trong 460 dòng | PR7 |
| `SanitizeHTMLFragment` | `renderer/sanitizer.go:47-148` | khoảng 100 dòng | PR8 |
| Template func `log`, `lookup` | `renderer/renderer.go` | | PR8 |
| Handlebars transpiler (nếu D1 bỏ) | `renderer/handlebars.go` | khoảng 600 dòng | PR8 |
| `handlebarsDiagnostics.ts` (nếu D1 bỏ) | `notification-mfe/src/utils` | 27 KB | PR8 |
| Seed data MFE | `types.ts:636-1344` | khoảng 700 dòng | PR9 |
| `DefaultProcessContextCatalog` demo | `application/extensions.go:45-66` | | PR9 |
| Accessor `Store()`, `Router()`, `Queue()` | `service.go:139-141` | | PR9 |
| `NotificationAdminService` (nếu D2 đồng ý) | `admin.proto`, `grpcapi` | 21 RPC | PR9 |
| `go/platform/circuitbreaker` (nếu D6 đồng ý) | `go/platform` | 38 KB | PR-P |

## 6. Bảng chưa dùng (D4)

| Bảng | Hiện trạng | Đề xuất |
|---|---|---|
| `notification_dispatch_jobs` | Bảng của hàng đợi tự viết | Xóa sau khi River chạy ổn |
| `publication_releases` | Trùng vai trò với `template_releases` | Xóa |
| `application_api_keys` | Không dùng. MFE có nút `keys/rotate` | Thay bằng ràng buộc fingerprint mTLS với application (D7), bỏ nút rotate ở MFE |
| `provider_health` | Không dùng | Dùng làm snapshot trạng thái breaker theo replica (PR6) |
| `delivery_metrics_hourly` | Không dùng. MFE gọi `metrics/query` | Dùng: periodic job tổng hợp theo giờ, `metrics/query` đọc bảng này (PR9) |
| `provider_callbacks` | Không dùng | Giữ cho tính năng bounce và complaint (backlog) |
| `workflow_instance_contexts` | Không dùng | Giữ cho trigger runtime (backlog), xóa nếu trigger lấy đủ context từ event |

Mỗi lần xóa bảng phải sửa danh sách bảng bắt buộc trong `db/scripts/verify-schema.sh` (dòng 130-160) trong cùng PR.

## 7. Gate cho mỗi PR

| Gate | Điều kiện | Áp dụng từ |
|---|---|---|
| Format | `gofmt -l .` không có file nào | PR0 |
| Vet | `go vet ./...` | PR0 |
| Static analysis | `staticcheck` v0.8.2 không có lỗi | PR2 |
| Dead code | `deadcode -test ./...` (x/tools v0.53.0) không có kết quả | PR2 |
| Lỗi bị nuốt | `errcheck -blank` với danh sách loại trừ | PR4 |
| Test | `go test -race -count=1 ./...` trên PostgreSQL thật | PR1 |
| Workspace | Build và test mọi module trong `go.work` khi đổi dependency | PR0, PR5, PR7 |
| Migration | `migrate.sh` chạy hai lần trên DB trắng, rồi `verify-schema.sh notification-service` | PR4 |
| Proto | `buf lint`, `buf breaking`, `buf generate` rồi `git diff --exit-code` | PR9 |
| MFE | `npm run typecheck`, `npm test`, `npm run build` | PR2 |
| E2E | Compose local (`notification-postgres`, Redpanda, Mailpit): gửi email, kill worker, failover, override | PR5 |
| Tải | 2 lần peak ước tính: p95 lane transactional ≤ 2 s, CPU PostgreSQL < 60% | Trước go-live |

## 8. Ngoài phạm vi (backlog tính năng)

Refactor này không hoàn thiện các tính năng dưới đây. Liệt kê để không bị hiểu nhầm là đã có:

- Driver `http` cho SMS, ZNS, webhook. Hiện chỉ có `smtp`, `inapp`, `mock`.
- Trigger rule chạy thật. Chưa có consumer `bpmp.engine.committed-events`. `SimulateTriggerRule` coi `DataMappingCEL` là đường dẫn `vars.x` và luôn trả `Matched: true` (`application/extensions.go:960-967`). `cel-go` chưa có trong `go.mod`.
- Callback từ provider (bounce, complaint, delivery receipt) ghi `provider_callbacks` và `suppressions`.
- API quản lý layout (`SaveLayoutVersion` không có caller) và contact (`SaveSubjectContact` không có caller).
- Data key theo (tenant, ngày) và Vault Transit cho crypto-shredding. Hiện dùng một khóa tĩnh lấy từ biến môi trường.
- Job xóa dữ liệu theo chính sách retention. Làm bằng periodic job sau PR5.
- Editor Monaco. MFE chưa có dependency này.

## 9. Rủi ro

| Rủi ro | Giảm thiểu |
|---|---|
| Nâng pgx, x/crypto, x/text ảnh hưởng nhiều module | Nâng riêng ở PR0, chạy test toàn workspace |
| Thiếu toolchain 1.27.4 trên máy dev và CI | Cài sẵn trong image (D9) |
| SQL của River chạy chung một transaction bị lỗi | Mỗi version một file, test migrate trên DB trắng |
| `verify-schema.sh` hard-code danh sách bảng | Sửa cùng PR với migration |
| Tách khóa HMAC làm các giá trị đã băm bằng khóa cũ không còn khớp | Chưa có dữ liệu production. Reset DB dev và ghi chú trong PR |
| Bỏ Handlebars làm hỏng template đã lưu | Migration kiểm và chuyển, thêm CHECK constraint |
| go-mail tạo header hoặc encoding khác bản cũ | Golden test và E2E Mailpit |
| Ngữ nghĩa breaker đổi (slow call, breaker theo replica) | Ghi vào ADR, đổi nhãn trên MFE |
| Test chạy trên PostgreSQL chậm hơn MemoryStore | Mỗi test một schema, dùng `t.Parallel()` |
