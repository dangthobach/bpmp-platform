# BPMP refactor prototype

Tài liệu này mô tả thiết kế và trạng thái triển khai rút ra từ `bpmp-prototype`. Đây không phải cam kết rằng mọi API hay luồng UI đã chạy production.

## Đọc theo chủ đề

1. [Kiến trúc](architecture.md): service ownership, luồng dữ liệu và nguyên tắc nền tảng đa lĩnh vực.
2. [API](api-spec.md): quy ước API, trạng thái current/proposed và các gate trước khi mở UI action.
3. [Phân quyền](authorization-and-data-access.md): nguồn chuẩn về catalog động, policy, branch scope, row/field filtering, revoke và PEP.
4. [Data model](data-model.md) và [DBML](schema.dbml): mô hình logic; DBML không thay migration.
5. [Authoring](authoring-service.md): contract và trạng thái hiện thực BPMN/DMN/forms/deployment.
6. [Delivery plan](delivery-plan.md): thứ tự triển khai và acceptance gates.
7. [UI–API mapping](ui-api-mapping.md): tóm tắt ánh xạ prototype; chi tiết nằm ở CSV/JSON.

`appendix/` chứa proposal chuyên biệt và hướng dẫn nền tảng bổ trợ. Dùng khi cần triển khai chủ đề đó; các trang chính phía trên giữ phần tóm tắt và điều hướng.

## Nguồn dữ liệu và tái tạo

- [API design OpenAPI](api-spec.openapi.json), [API catalog](api-catalog.json), [operation summary](api-operation-reference.md).
- [UI action mapping CSV](ui-api-mapping.csv), [prototype interaction CSV](interaction-inventory.csv), [interaction JSON và file hashes](interaction-inventory.json).
- Tạo lại từ repo root bằng `python tools/build-api-design.py`, `python tools/inventory-prototype.py --source bpmp-prototype/src` và `python tools/map-prototype-api.py`.

Các inventory được sinh bằng lexical scan; chúng không chứng minh behavior đã hoạt động. API `proposed` cũng không được xem là API đã triển khai.
