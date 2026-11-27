# Notification: đánh giá prototype UI và đối chiếu API

Trạng thái: **đánh giá ngày 2026-11-27**, nguồn `bpmp-prototype` commit `b0f9f56` ("feat(notification): implement notification service module", `origin/main`; working tree prototype cục bộ có thay đổi chưa commit ở `App.tsx`, `Sidebar.tsx` nên chưa merge — chỉ đọc bằng `git show`). Đối chiếu với [ADR-013](../../adr/ADR-013-notification-service.md), [thiết kế Notification](notification-service.md) và [api-catalog.json](../api-catalog.json). Prototype dùng dữ liệu mock trong `mockNotificationData.ts`, **không gọi API nào**: mọi thao tác dưới đây là state cục bộ.

## 1. Prototype có gì

Điều hướng 3 nhóm, 9 màn: **Management** (Templates, Variable Mapping, Applications, Service Providers), **Monitoring** (Delivery Monitoring, Metrics Dashboard, Audit Logs, Circuit Breaker), **Operations** (Send Notification). Templates có split/edit/preview, autocomplete biến, chẩn đoán cú pháp Handlebars, lịch sử phiên bản + rollback, thao tác hàng loạt (đổi trạng thái, tag, xóa), preview desktop/mobile/source. Variable Mapping là đồ thị phụ thuộc Process Context → Template → biến, đánh dấu OK/WARN/CRITICAL.

## 2. Quyết định cần chốt (xung đột với thiết kế hiện tại)

| # | Prototype | Thiết kế ADR-013 | Quyết định đề xuất |
|---|---|---|---|
| D1 | Cú pháp Handlebars (`{{customerName}}`, `{{#if}}`, `{{#each}}`, `{{{raw}}}`, helper) | `gotmpl/v1`, biến `.data.*` | Thêm `syntax = handlebars/v1`: **tác giả viết Handlebars, backend biên dịch (transpile) sang AST `gotmpl/v1` rồi áp allowlist hiện có**. Không thêm dependency, không đổi renderer, version cũ không bị ảnh hưởng (đúng mục đích của trường `syntax`). `{{x}}`→`.data.x`; `recipient.*`, `tenant.*`, `brand.*`, `system.*` giữ tiền tố; `{{{x}}}` chỉ hợp lệ với biến `html_fragment`. Cần sửa ADR-013 mục 4 (đã ghi chú bên dưới). |
| D2 | Một template = một kênh; mỗi lần Save tăng `version` và là bản đang chạy | Version bất biến; variant = kênh × locale; release pointer | UI giữ trải nghiệm của prototype nhưng ánh xạ: danh sách một dòng cho mỗi `template_key` (chip kênh); trình soạn sửa **variant** của kênh đang chọn. **Save = lưu draft**; **Publish & Activate** tạo version bất biến + release. Rollback = release mới (`ordinal+1`) trỏ về version cũ, không sao chép nội dung như prototype. Lịch sử hiển thị cả version và release ordinal. |
| D3 | Xóa template là xóa thật (kể cả hàng loạt) | Soft delete + restore, audit | Xóa = soft delete (`is_deleted`), có `restore`; bắt buộc template không còn release active. Hàng loạt trả kết quả từng mục. |
| D4 | `priority` NORMAL/HIGH/URGENT khi gửi | `lane` + `category` | `priority` thành thứ tự ưu tiên **trong lane** (1..3, River priority), `lane` vẫn do category/ứng dụng quyết định. URGENT không tự chuyển lane. |
| D5 | Người dùng sửa chủ đề/nội dung khi gửi (`subjectOrTitle`) | Producer không chọn nội dung | Bỏ ghi đè nội dung; chỉ nhập dữ liệu biến. |
| D6 | Provider sửa là có hiệu lực ngay (priority, enabled, timeout) | Draft → validate → revision bất biến → release | UI: "Lưu revision & kích hoạt"; `enabled` = `operational_state` ACTIVE/DISABLED (override có audit, không cần revision mới). |
| D7 | Retry cập nhật chính bản ghi thành DELIVERED | Resend tạo delivery mới có `resend_of` | Giữ Resend; UI hiển thị liên kết bản gốc ↔ bản gửi lại. |
| D8 | Nút "Simulate Spike" trên mọi breaker | Không có API tạo lỗi giả trên provider thật | Chỉ bật cho provider driver `mock` ngoài production; đổi tỷ lệ lỗi của mock profile. |
| D9 | Preview SMS: `ceil(chars/160)` | GSM-7 160/153, UCS-2 70/67 | Dùng kết quả server (`sms_encoding`, `sms_segments`). Cách tính 160 sai với tiếng Việt có dấu. |

## 3. Đối chiếu API theo màn hình

Ký hiệu: ✅ đã có trong catalog, 🟡 có nhưng thiếu trường/hành vi, ❌ chưa có.

### Templates
| Nhu cầu UI | API | Tình trạng / thay đổi |
|---|---|---|
| Danh sách lọc kênh, tag, trạng thái, tìm kiếm; số lần dùng 24h | `GET /v1/notifications/templates` | 🟡 thêm query `channel`, `tag`, `status`, `q`; thêm vào DTO `tags[]`, `channels[]`, `active_release`, `usage_24h`. |
| Danh mục tag | `GET /v1/notifications/templates/tags` | ❌ mới. Cột `templates.tags` (jsonb). |
| Tạo, lưu draft, publish, release, rollback | templates, `versions/draft`, `publish`, `releases`, `releases/rollback` | ✅ |
| Lịch sử phiên bản (ghi chú thay đổi, người lưu, thời điểm) | `GET .../versions` | 🟡 thêm `change_note` (cột mới `template_versions.change_note`), `created_by`, `release_ordinal`. |
| Chẩn đoán cú pháp thời gian thực | `POST /v1/notifications/templates/validate` | ❌ mới. Nhận draft không lưu; trả `diagnostics[]` `{code, severity, target: subject|html|text, line, column, message, quick_fix?}`. Client giữ bản lint nhẹ (prototype `handlebarsDiagnostics.ts`) để phản hồi tức thì; server là nguồn quyết định khi publish. Mã lỗi giữ nguyên danh sách của prototype (`UNCLOSED_BLOCK`, `INVALID_HELPER`, …) cộng `UNDECLARED_VARIABLE`, `FORBIDDEN_CONSTRUCT`. |
| Autocomplete biến | — | UI dùng `variable_schema` của draft; không cần API. |
| Bảng biến (key, label, mẫu, bắt buộc, kiểu) | `versions/draft` | 🟡 UI sinh `variable_schema`: `label→title`, `sampleValue→example`, `number→decimal`, `currency→money`. Giữ chế độ xem JSON nâng cao. |
| Preview 3 chế độ, desktop/mobile/source | `POST /v1/notifications/preview` | ✅ (SMS theo D9). |
| Thao tác hàng loạt (trạng thái, tag, xóa) | `POST /v1/admin/notification-templates/batch-actions` | 🟡 đường dẫn cũ nằm ngoài `/v1/notifications`. Chuyển thành `POST /v1/notifications/templates/batch-actions` `{action: SET_STATUS|TAGS|DELETE, ids[], status?, tag_mode?: add|remove|replace, tags?[]}`, trả `results[{id, ok, error}]`; xóa nhóm `/v1/admin/notification-templates/*` cũ khỏi catalog (trùng chức năng). |
| Test send từ editor | `POST /v1/notifications/test-send` | ✅ |

### Variable Mapping
| Nhu cầu UI | API | Tình trạng |
|---|---|---|
| Đồ thị Process Context → Template → biến, mức OK/WARN/CRITICAL, lọc rủi ro, biến không dùng | `GET /v1/notifications/variable-mapping` | ❌ mới. Server tính từ `trigger_rule_revisions` + `variable_schema` + danh mục biến process. Trả `links[{process_key, trigger_kind, template_key, schema_major, variable_key, severity, reason}]` với `reason ∈ OK, MISSING_IN_CONTEXT, TYPE_MISMATCH, UNDECLARED_IN_TEMPLATE, UNUSED_EXPORT, UNBOUND_PROCESS`; kèm `processes[]`. Đây chính là kiểm tra type-check CEL lúc publish rule nhưng chạy trên rule đã publish để cảnh báo trước runtime. |
| Nguồn "Process Context fields" | Authoring (đã có `workflow_variables` ở gateway) | 🟡 Notification gọi API Authoring có cache, không đọc DB của Authoring. `sourcePath` hiển thị `vars.<tên>` (không còn `camunda.variables.*`). |
| Liên kết template ↔ process; thêm trường xuất vào context; khai báo biến vào template; ánh xạ lại biến | trigger rules | ❌ catalog chưa có trigger rule: thêm `GET/POST /v1/notifications/trigger-rules`, `PUT .../{id}/revisions` (draft), `POST .../revisions/{rev}/activate`, `POST .../{id}/simulate`. Mỗi hành động của đồ thị tạo **draft** (revision rule hoặc version template), không publish ngầm. |

### Applications
| UI | API | Tình trạng |
|---|---|---|
| appCode, tên, mô tả, ownerTeam, kênh cho phép, rate/phút, hạn mức ngày, đã dùng, webhook, sender mặc định, trạng thái | `GET/POST/PATCH /applications` | 🟡 thêm `owner_team`, `default_sender_identity` (cột mới), `usage_today` (từ rollup/Redis, chỉ đọc), `quota {per_minute, per_day}`; đổi trạng thái ACTIVE/SUSPENDED qua `PATCH status`. |
| Xoay khóa, sao chép khóa | `keys/rotate`, `keys/{id}/revoke` | ✅ Khóa chỉ hiển thị **một lần**; UI không lưu/hiển thị lại khóa sau đó (prototype hiển thị `apiKeyMasked` mãi). `auth_mode` WORKLOAD_MTLS/OIDC_CLIENT không có khóa. |

### Service Providers
| UI | API | Tình trạng |
|---|---|---|
| Vendor (SES, SendGrid, SMTP, FCM, APNs, VNPT, Viettel, Slack) | `POST /providers` + preset | 🟡 vendor = preset của driver. Thiếu preset VNPT SMS, Viettel Brandname, Slack, APNs. **APNs cần xác thực JWT ES256 qua HTTP/2: driver `http` chưa có `apns_jwt`** — đưa vào backlog Slice 2 hoặc dùng FCM. |
| Priority 1/2 (primary/fallback) | `routing-policies` | 🟡 priority là bậc (`tier`) trong routing policy, không phải thuộc tính provider. UI chỉnh trong màn Providers nhưng ghi routing policy. |
| enabled, ping, timeout, maxRetries | `test-connection`, revision config | ✅ `enabled` theo D6. |
| healthStatus, avgLatency, successRate, sent24h | `GET /providers/health` | 🟡 thêm `avg_latency_ms`, `success_rate_bps`, `sent_24h` (rollup); `healthStatus` suy ra: breaker OPEN→DOWN, HALF_OPEN→DEGRADED. |

### Delivery Monitoring
| UI | API | Tình trạng |
|---|---|---|
| Lọc kênh/trạng thái/tìm kiếm | `GET /deliveries` | 🟡 trạng thái hiển thị nhóm: DELIVERED, FAILED, BOUNCED, RETRYING (= QUEUED có `attempt_count>0` hoặc DEFERRED), QUEUED. Tìm kiếm chỉ theo message id, correlation id, template, hoặc **địa chỉ nhận đầy đủ** (băm phía server); không `ILIKE` trên PII. |
| Trace pipeline (bước, trạng thái OK/WARN/ERROR, ms, chi tiết) | `GET /deliveries/{id}` | 🟡 thêm `trace[]` dẫn xuất từ `delivery_events` + `delivery_attempts`. |
| Nội dung đã render, biến đã dùng | `POST /preview` (`DELIVERY_REPLAY`) | ✅ Không lưu nội dung render (chỉ `content_hash`); replay ghi audit, che `pii`/`secret`, hết hạn thì dữ liệu mẫu. |
| Retry | `POST /deliveries/{id}/resend` | ✅ theo D7. |

### Metrics Dashboard
`POST /v1/notifications/metrics/query` ✅ nhưng cần: khung thời gian 1h/24h/7d, `group_by` channel|lane|provider|status_class, thước đo `accepted/sent/delivered/failed`, độ trễ p50/p95/p99. **Lỗ hổng:** DBML cũ lưu `latency_p95_ms` trong rollup theo giờ — không gộp được nhiều bucket (trung bình các p95 sai). Đổi sang `latency_histogram` (jsonb, bucket cố định) để tính phân vị đúng; đã cập nhật DBML.

### Audit Logs
`GET /v1/notifications/audit` 🟡: prototype cần `actor`, `actorRole`, `ipAddress`, `severity`, `resourceType`, `details`. `audit_evidence` chưa có → thêm cột `resource_type`, `severity` (INFO/WARN/CRITICAL), `actor_display`, `actor_role`, `client_ip`, `details` (đã cập nhật DBML). Actor lấy từ actor proof đã xác thực — prototype hard-code `tambv` / `172.16.40.89`. Bộ lọc: resource_type, severity, tìm kiếm theo action/resource.

### Circuit Breaker
| UI | API | Tình trạng |
|---|---|---|
| State, failure rate, slow-call rate, ngưỡng, cửa sổ, bufferedCalls, failedCalls, cooldown, fallback | `GET /providers/health` | 🟡 **thiếu slow-call**: `NotificationBreakerPolicy` và `go/platform/circuitbreaker` chỉ có tỷ lệ lỗi. Thêm `slow_call_threshold_ms`, `slow_call_rate_threshold_bps`; trả `buffered_calls`, `failed_calls`, `slow_calls`, `cooldown_remaining_ms`, `fallback_provider`. Cửa sổ theo thời gian (`rolling_window_ms`) thay vì theo số cuộc gọi của prototype; UI hiển thị thời gian. |
| Lịch sử chuyển trạng thái | `GET /providers/{id}/circuit-breaker/transitions` | ❌ mới; **thiết kế cũ nói có lịch sử nhưng không có bảng** → thêm `breaker_transitions` (đã vào DBML). |
| Chuyển trạng thái thủ công + lý do | `.../override` | 🟡 CLOSED/OPEN = `FORCE_CLOSE`/`FORCE_OPEN` kèm TTL; "HALF_OPEN" = hành động **probe** `POST .../circuit-breaker/probe` (xóa override, cho breaker vào half-open). Lý do và TTL bắt buộc. |
| Đổi ngưỡng từng provider | `providers/{id}/revisions` | 🟡 `config.breaker` trong revision; mặc định lấy `NotificationPolicy.default_breaker`. |
| Simulate Spike | `POST .../simulate-failure` | ❌ chỉ driver `mock`, ngoài production (D8). |

### Send Notification
| UI | API | Tình trạng |
|---|---|---|
| Ứng dụng, template, người nhận, correlationId, biến | `POST /notifications/send` | ✅ kênh suy ra từ variant của template; người nhận `subject_ref` hoặc địa chỉ ngoài. |
| Dry-Run: xác thực quota/kênh/payload, quyết định định tuyến + failover | `POST /notifications/send` với `dry_run=true` | ❌ thêm `dry_run` (không ghi DB, không enqueue). Trả `plan {template_version_id, release_ordinal, variant, schema_errors[], quota{remaining}, routing{candidates[{provider, breaker_state}], selected, failover_reason}, warnings[]}`. Không thêm `dry_run` vào gRPC `SubmitNotificationRequest` mà dùng RPC riêng `PlanNotification` để tránh nhầm. |
| `priority` | `send` | 🟡 theo D4: thêm `priority` (1..3). |

## 4. Thay đổi hợp đồng và dữ liệu

- **DBML** (35 bảng, 41 quan hệ — đã cập nhật): `applications.owner_team`, `.default_sender_identity`; `templates.tags`; `template_versions.change_note`; `notification_requests.priority`; `delivery_metrics_hourly.latency_histogram` (thay `latency_p95_ms`); `audit_evidence` + `resource_type`, `severity`, `actor_display`, `actor_role`, `client_ip`, `details`; bảng mới `breaker_transitions`.
- **Protobuf** (additive, trước khi chốt Slice 1): `NotificationBreakerPolicy` + 2 trường slow-call; `PlanNotification`, `ValidateTemplate`, `BatchTemplateActions`, `ListBreakerTransitions`, `ProbeBreaker`, `QueryVariableMapping`, các RPC trigger rule; `priority` trong `SubmitNotificationRequest` và `NotificationRequestCommand`.
- **Catalog** (`tools/build-api-design.py`): thêm các route mới ở trên, chuyển/xóa nhóm `/v1/admin/notification-templates*`.
- **Capability** (Authz): thêm `notification.template.validate`, `notification.template.batch`, `notification.variable_mapping.read`, `notification.rule.read|write|activate`, `notification.breaker.probe`, `notification.operations.plan`.

## 5. Việc MFE phải sửa so với bản Slice 1 hiện có

Bản MFE hiện tại dùng 4 tab; prototype yêu cầu rail điều hướng 3 nhóm/9 màn, tách Metrics, Audit, Circuit Breaker thành màn riêng, thêm Variable Mapping, bảng biến, chẩn đoán cú pháp + autocomplete, thao tác hàng loạt + tag, sidebar lịch sử phiên bản, viewport desktop/mobile/source, trace inspector, dry-run + quyết định định tuyến, thông số breaker đầy đủ và lịch sử chuyển trạng thái. Ràng buộc giữ nguyên: chỉ `@ods/shared-ui`, preview trong `<iframe sandbox="" srcdoc>`, `Idempotency-Key` + `X-Command-ID` cho mọi lệnh ghi, không hiển thị lại API key, không hard-code actor.

## 6. Thay đổi ADR-013

Mục 4 (Rendering) được bổ sung: tác giả có thể chọn `syntax = handlebars/v1`, được biên dịch sang AST `gotmpl/v1` và đi qua cùng allowlist/validator; `gotmpl/v1` vẫn hợp lệ. Mục "Open conditions" thêm: xác nhận D1–D9 với product owner của prototype và quyết định hỗ trợ APNs.
