# API contract

Đây là trang điều hướng ngắn cho API. OpenAPI và Protobuf là nguồn schema; tài liệu thiết kế không chứng minh operation đã được triển khai hoặc deploy.

## Nguồn chuẩn

- API đang có: Gateway routes và [OpenAPI nhúng](../../apps/go/api-gateway/internal/apidocs/openapi.json).
- RPC nội bộ: `contracts/proto/bpmp/*/v1`.
- Operation đề xuất và trạng thái current/proposed: [design OpenAPI](api-spec.openapi.json), [catalog](api-catalog.json).
- Ánh xạ prototype: [tóm tắt](ui-api-mapping.md), [CSV chi tiết](ui-api-mapping.csv).

## Quy tắc bắt buộc

- Xác thực actor tại Gateway và owner service; service-to-service dùng mTLS/workload proof. Tenant lấy từ identity đã xác thực.
- Owner service kiểm tra quyền, tenant, row/field scope và trạng thái tại điểm đọc/ghi. UI visibility không phải enforcement.
- Command dùng command ID, idempotency key và payload digest; mutation có optimistic concurrency. Receipt `accepted` không đồng nghĩa đã hoàn tất.
- Query pagination/filter/export áp dụng authorization trước khi tính page, aggregate hoặc serialize.
- API đề xuất không được nối vào UI như thao tác hoạt động cho tới khi contract, owner enforcement, persistence, failure behavior và observability cùng hoàn tất.

Đặc tả chi tiết lịch sử nằm trong [appendix](appendix/api-design-notes.md); các operation và schema machine-readable nằm trong OpenAPI.
