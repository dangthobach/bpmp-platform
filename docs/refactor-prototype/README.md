# BPMP: thiết kế refactor từ prototype

Ngày khảo sát: 2026-11-10. **Trạng thái: đề xuất để review; chưa phải ADR được chấp thuận hoặc tính năng đã triển khai.**

Baseline prototype: `c5e8ee14f87c23b1d6633ced8c46b95f947e2061`.
Baseline platform: `760a3a21a9d05202adfb1c79a0b2dd88d84f8fe1`.

## Mục tiêu và phạm vi

Lấy trải nghiệm trong `bpmp-prototype` làm đầu vào thiết kế sản phẩm; giữ các invariant về deterministic engine, tenant isolation và quyền chuyển trạng thái của platform. Không sao chép các component nguyên khối hoặc cơ chế cập nhật state giả lập sang production.

Bộ tài liệu này cung cấp thiết kế logic cho toàn bộ nhóm chức năng thấy trong source prototype. Chưa phải physical schema, migration sản xuất, hợp đồng API đã phát hành, hay xác nhận rằng mọi luồng đều chạy trên trình duyệt. Những điểm cần khảo sát runtime hoặc quyết định nghiệp vụ được giữ thành gate cụ thể.

## Định hướng nền tảng doanh nghiệp đa lĩnh vực

BPMP Platform hướng tới tái sử dụng tối đa các cơ chế nền tảng dùng chung, tránh duplicate code và hỗ trợ cấu hình để thích nghi với quy trình, policy, dữ liệu tham chiếu, biểu mẫu, assignment và tích hợp của nhiều lĩnh vực doanh nghiệp. Phần khác biệt theo ngành/khách hàng nên được biểu diễn bằng versioned configuration, schema và policy có kiểu trong phạm vi capability mà owner service hỗ trợ; chỉ mở rộng code/domain adapter khi semantics thực sự khác hoặc cần invariant mới. “Tối đa configuration” không có nghĩa là cho phép script/SQL tùy ý hay cấu hình tạo capability mà runtime chưa triển khai.

Các bounded context vẫn sở hữu logic và dữ liệu của mình. Tái sử dụng tập trung ở contract ổn định, runtime, policy/config lifecycle, SDK và UI primitives; không gom policy nghiệp vụ khác nhau vào một shared module không rõ owner. Mỗi dynamic configuration cần schema/version, validation, publish/rollback, audit, compatibility và owner rõ ràng.

## Tài liệu và cách đọc

1. [Khảo sát chức năng và command](functional-analysis.md): hành vi hiện tại, khoảng trống và yêu cầu backend.
2. [Danh mục control/event](interaction-inventory.md): 615 native controls và 711 event props trong 25 TSX files; có file và dòng nguồn. Hai loại có thể chỉ cùng một thao tác.
3. [CSV](interaction-inventory.csv), [JSON](interaction-inventory.json): biểu thức đầy đủ, SHA-256 từng file để truy vết snapshot; không dùng số lượng event như số lượng use case.
4. [Kiến trúc đích](architecture.md): service ownership, MFE, command/event, failure và consistency.
5. [Mô hình dữ liệu và ERD](data-model.md): bảng logic, thuộc tính, quan hệ, index và mapping hiện tại. [DBML](schema.dbml) là bản mô hình có thể xuất diagram, không phải migration.
6. [Component architecture](component-system.md): phân tầng, contract và tiêu chí tái sử dụng.
7. [Lộ trình và acceptance gates](delivery-plan.md): thứ tự triển khai, dữ liệu chuyển đổi và kiểm thử.

8. [MDM Service](mdm-service.md): model/data admin, immutable publications, consumer-local replica và batched resolve để giảm round-trip.

9. [Service–database và Reporting](databases-and-reporting.md): storage ownership, source-to-report lineage, giao tiếp cụ thể và tối ưu realtime tải lớn.

10. [Entity conventions](entity-conventions.md): AuditEntity, soft delete và optimistic locking bắt buộc.
11. [API Specification](api-spec.md): core BPMP trước, rồi UI/action traceability; OpenAPI thiết kế phân biệt current/proposed.

12. [Phân quyền và truy cập dữ liệu](authorization-and-data-access.md): RBAC + ABAC, dynamic action/resource catalog, tenant boundary, row/field policy, owner enforcement, revoke/cache và acceptance gates.

13. [Backend implementation standards](backend-operations.md): UUID, healthz/readiness/metrics cho mọi service và scheduled cleanup outbox/log theo retention/safe checkpoint.

## Kết luận khảo sát

- `App.tsx` giữ process/instance state; deploy hiện chỉ tăng version, hiện toast. Approve trong Tasklist có thể đánh dấu instance hoàn thành mà không diễn giải workflow.
- Portal có lifecycle hồ sơ riêng, khác lifecycle instance/task; officer decision đang thay đổi các bước theo index cố định. Cần Case aggregate và saga với Engine.
- Identity khoảng 3.990 dòng, Portal 3.119 dòng, DMN table 1.662 dòng. Cần tách feature/controller/view, không tạo một shared component khổng lồ khác.
- Shell, `task-mfe`, `@ods/mfe-contract`, `@ods/call-api`, `@ods/shared-ui` đã tồn tại. Giữ các contract và cơ chế isolation/catalog đã có; mở rộng theo vertical slice.
- `docs/ods-refactor-progress.md` vẫn ghi hosted/staging rehearsal chưa đóng gate. Thiết kế này không xóa gate hoặc nâng trạng thái phase.

## Quy tắc bằng chứng và tái tạo

Chạy từ `bpmp-platform/`:

```sh
python3 tools/inventory-prototype.py --source ../bpmp-prototype/src
```

Scanner lexical thu native controls và callback props; không thay thế JSX AST parser hoặc browser walkthrough. Review nhãn động, event bubbling, disabled state, icon-only buttons và delegated handlers bằng UI trước khi chốt parity. Không sửa `interaction-inventory.*` thủ công. Inventory IDs có thể đổi khi source thay đổi; truy vết bằng file/hash/dòng.

Khi mâu thuẫn, tuân theo [thứ tự ưu tiên kiến trúc](../architecture/README.md), ADR-011 và hợp đồng đang có. Thay đổi boundary cần ADR riêng trước khi implementation phụ thuộc vào nó.
