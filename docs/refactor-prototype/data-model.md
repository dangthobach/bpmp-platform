# Data model

Trang này tóm lược ownership và invariant. [DBML](schema.dbml) là mô hình quan hệ chi tiết; migration của từng service mới là nguồn triển khai vật lý.

## Invariant

- Business data thuộc một bounded context và tenant; không tạo foreign key xuyên service/database.
- Entity mutable dùng UUID, audit fields, soft delete khi semantics cho phép, và row version cho compare-and-swap. Row version khác workflow/model/release version.
- Event, immutable version, authorization evidence và dedup/checkpoint có retention bất biến riêng; không soft-delete hoặc sửa nội dung tùy tiện.
- Branch là cây tenant-local; scope được kế thừa từ branch xuống descendant. Queue là trục độc lập và được kết hợp bằng AND khi action yêu cầu cả hai.
- Policy/filter enforcement ở owner service. Xem [Authz](authorization-and-data-access.md) và migration đang có trước khi thêm cột branch hoặc row filter.

## Ownership

| Context | Nguồn dữ liệu chính | Ví dụ |
|---|---|---|
| Engine | RocksDB/Raft | workflow events, snapshots, tasks |
| Authoring | PostgreSQL | drafts, immutable model versions, forms, deployment jobs |
| Human Runtime | PostgreSQL | assignments, claims, task completion boundary |
| Projection | PostgreSQL/read models | inbox, searchable workflow state |
| Access/Authz | PostgreSQL + signed bundles | identities/grants/catalog/policy lifecycle |
| Case/Document/Integration | context-owned stores | case state, files/metadata, connector execution |

Mô hình logic chi tiết được giữ trong [data-model reference](appendix/data-model-reference.md). Tuân [entity conventions](appendix/entity-conventions.md) khi thêm entity.
