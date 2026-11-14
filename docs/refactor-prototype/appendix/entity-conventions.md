# Entity conventions: audit, soft delete và optimistic locking

Trạng thái: quy ước thiết kế đích theo yêu cầu 2026-11-10; **chưa backfill/migrate runtime hiện tại**. Áp dụng cho mọi persisted business entity và aggregate mới/refactor trong mọi owner. Không chỉ là field tùy chọn trong DTO.

## AuditEntity và VersionedEntity

Mọi business entity kế thừa về mặt contract `AuditEntity + SoftDeletable + VersionedEntity`. Go dùng embedding, Rust dùng composition; không bắt ngôn ngữ phải có class inheritance. Repository, serializer và migration cùng thực thi contract, không chỉ đặt một base struct rồi bỏ qua khi query/update.

| Field | Storage target | Quy tắc |
|---|---|---|
| id / tenantId | Native UUID cho entity mới; wire string có UUID validation | PK/FK luôn tenant-scoped; legacy opaque IDs cần explicit compatibility mapping, không tự đổi historical IDs |
| createdAt / createdBy | timestamptz / trusted subject reference | Server thiết lập, bất biến; actor có thể human/workload với loại identity được ghi audit |
| updatedAt / updatedBy | timestamptz / trusted subject reference | Server cập nhật mỗi mutation đã commit |
| isDeleted | boolean, default false | Soft delete marker, không đồng nghĩa cancel/terminate runtime |
| deletedAt / deletedBy / deleteReason | nullable timestamptz / subject ref / text | Cùng thay đổi với isDeleted trong một transaction; undelete có privileged audit |
| version | `row_version bigint` cho logical DBML này | CAS counter monotonic; response đề xuất `entityVersion` để không nhầm model/release/workflow version |

`createdBy`/`updatedBy`/`deletedBy` là identity từ trusted actor/workload context, không arbitrary user input. Legacy backfill ghi explicit migration/system principal, không dựng người tạo giả. Row version khác event sequence, workflow version, schema version, publication generation và draft revision. Existing Human `version`/`expected_version` giữ wire contract hiện có; migration map rõ, không tạo hai counter độc lập cho cùng concurrency boundary.

## UUID identity contract

Mọi new platform-owned entity ID dùng UUID, PostgreSQL native `uuid`; ID được sinh một lần ở trusted boundary và giữ nguyên khi retry. Business keys/external IDs là fields riêng, không surrogate entity ID. Không sinh clock/random ID trong deterministic core. Legacy API/storage string IDs cần alias/migration compatibility; không đồng nghĩa tiếp tục tạo arbitrary string IDs cho entity mới. Chi tiết [backend operations](backend-operations.md).

## Optimistic locking

Read trả entityVersion và strong ETag khi endpoint mới hỗ trợ. Mutation entity hiện hữu yêu cầu `If-Match` và/hoặc expectedVersion theo contract operation; nếu cả hai có mặt phải đồng nhất. Missing precondition → 428, ETag mismatch → 412; domain state/version conflict trong command contract → 409. Existing endpoints giữ semantics đang public cho tới compatibility rollout; không tuyên bố hiện tại tất cả có 428/412.

```sql
UPDATE owner_entity
SET payload = :payload, updated_at = :trusted_now,
    updated_by = :actor, row_version = row_version + 1
WHERE tenant_id = :tenant AND id = :id
  AND is_deleted = FALSE AND row_version = :expected;
```

Zero affected rows không được coi thành công hoặc retry blind. Phân biệt not-found/deleted/forbidden/conflict mà không leak entity existence ra actor không có quyền. Kết quả row update, audit evidence, command receipt và outbox commit cùng owner transaction. Child entity update thuộc aggregate phải kiểm/increment aggregate version hoặc explicit child concurrency contract; không lock mỗi row độc lập rồi vi phạm invariant tổng thể.

Engine không có SQL CAS; expected state/version được kiểm trong authoritative Raft command. Việc thêm expectedVersion vào command chưa có field đòi Protobuf compatibility và semantics review. Projection version không đủ chứng minh Engine state vẫn ở version đó.

## Soft delete và restore

- Default read/list/search/reference lookup loại `isDeleted=true`; includeDeleted chỉ endpoint/capability có quyền, không tự mở qua query flag.
- Delete dùng CAS, audit reason và outbox tombstone; không physical DELETE qua public CRUD API. Restore cũng CAS và phải revalidate uniqueness, references, publication policy; không auto restore child rows/cascade xuyên service.
- Constraint nhất quán: live row có deleted metadata NULL; deleted row có deletedAt/deletedBy/reason hợp lệ. Restore xóa marker nhưng audit evidence giữ toàn bộ delete/restore history.
- Chốt unique-key policy theo entity: code đã dùng trong workflow/MDM không recycle nếu có thể làm đổi nghĩa lịch sử. Partial unique index chỉ cho phép reuse nếu domain cho phép, không đặt mặc định cho mọi bảng.
- Soft-delete referenced entity không làm mất historical/versioned lookup. Live references mới bị chặn; existing consumers nhận tombstone/version publication có scope, không “record missing” rồi fallback global.
- Cancel task, terminate instance, archive model, retire release và cancel export là domain actions riêng; không thay bằng soft-delete hoặc restore runtime workflow.
- Purge/erasure theo Governance, retention/legal hold và approved job riêng. Soft delete không tự đáp ứng erasure; không cho `permanent=true` từ prototype trở thành generic hard-delete endpoint.

## Immutable evidence, versions và technical records

Mọi business entity vẫn có audit/soft-delete/version metadata trong target schema, nhưng **published immutable content không bị sửa để đánh dấu xóa**. Archive/tombstone nằm ở lifecycle/visibility envelope hoặc registry version mới; payload/digest lịch sử giữ nguyên. Restore registry không ghi đè artifact/version đã phát hành.

Append-only audit evidence và Engine events không có API update/delete/restore tùy ý. Inbox/outbox/checkpoint, report facts, Raft/storage records là technical/evidence data: dùng owner-defined immutable append/checkpoint/CAS/retention protocol; không lắp generic soft-delete filter làm mất idempotency hoặc lịch sử replay. DBML có các common fields làm envelope logic, không khẳng định runtime đều có columns này hoặc cho phép mutate evidence. Physical mapping của từng technical table phải được review riêng, giữ safety invariant hiện tại.

## API và UI

Writable DTO chỉ khai báo business fields và expectedVersion; server reject audit/deletion fields trong arbitrary patch. Response chứa audit metadata theo field visibility. UI giữ entityVersion từ read; mở modal không reset version, conflict refetch/diff trước submit mới. Delete cần confirmation/reason theo policy; recycle-bin/restore chỉ dùng cho entity được phép, không expose cho mọi record. Bulk action giữ item ID + expectedVersion + item result; không dùng một global version cho cả batch.

## Gate triển khai

Additive migrations → backfill system principal/defaults có lineage → enforce NOT NULL/CHECK/CAS → switch repositories/DTO → kiểm API compatibility. Test: hai writers cùng version chỉ một commit; delete-vs-update/restore race; includeDeleted không bypass tenant/policy; immutable audit/content không sửa; duplicate command không thêm effect; unique conflict khi restore. Không thay toàn bộ production tables bằng một script destructive.
