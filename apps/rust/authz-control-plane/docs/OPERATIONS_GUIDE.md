# Tài liệu Hướng dẫn Nghiệp vụ & Vận hành
# AuthZ Platform — Dynamic 5-Layer Authorization

> **Phiên bản:** 1.0 | **Đối tượng:** Solution Architect, Backend Developer, DevOps, Security Team  
> **Hệ thống:** authz-control-plane — BPMP Authorization Control Plane

---

## Mục lục

- [1. Tổng quan Kiến trúc](#1-tổng-quan-kiến-trúc)
- [2. Pipeline 5 Lớp — Luồng xử lý chi tiết](#2-pipeline-5-lớp--luồng-xử-lý-chi-tiết)
- [3. Hướng dẫn Nghiệp vụ theo Usecase](#3-hướng-dẫn-nghiệp-vụ-theo-usecase)
  - [3.1 Phân quyền theo Chi nhánh (Branch Isolation)](#31-phân-quyền-theo-chi-nhánh-branch-isolation)
  - [3.2 Phân quyền Tạm thời (Temporary Permission)](#32-phân-quyền-tạm-thời-temporary-permission)
  - [3.3 Phân quyền theo Instance (Resource-scoped Role)](#33-phân-quyền-theo-instance-resource-scoped-role)
  - [3.4 Ủy quyền bắc cầu (Delegation Chain — ReBAC)](#34-ủy-quyền-bắc-cầu-delegation-chain--rebac)
  - [3.5 Phân quyền theo Ca làm việc (Temporal + Shift)](#35-phân-quyền-theo-ca-làm-việc-temporal--shift)
  - [3.6 Phân quyền đa tầng kết hợp (Composite)](#36-phân-quyền-đa-tầng-kết-hợp-composite)
  - [3.7 Khóa khẩn cấp (Emergency Revoke)](#37-khóa-khẩn-cấp-emergency-revoke)
  - [3.8 Multi-tenant Isolation](#38-multi-tenant-isolation)
- [4. Data Filter — Lọc dữ liệu trả về](#4-data-filter--lọc-dữ-liệu-trả-về)
  - [4.1 Row Filter — Lọc hàng](#41-row-filter--lọc-hàng)
  - [4.2 Field Filter — Che giấu trường nhạy cảm](#42-field-filter--che-giấu-trường-nhạy-cảm)
  - [4.3 Filter cho đa Backend (SQL / ES / MongoDB)](#43-filter-cho-đa-backend-sql--es--mongodb)
- [5. Policy Engine — Quản lý và Triển khai Policy](#5-policy-engine--quản-lý-và-triển-khai-policy)
  - [5.1 Vòng đời Policy (DRAFT → SHADOW → ACTIVE → ARCHIVED)](#51-vòng-đời-policy-draft--shadow--active--archived)
  - [5.2 Shadow Mode — Kiểm thử Policy an toàn](#52-shadow-mode--kiểm-thử-policy-an-toàn)
  - [5.3 Policy Conflict Resolution — Xử lý xung đột](#53-policy-conflict-resolution--xử-lý-xung-đột)
  - [5.4 Policy-as-Code với GitOps](#54-policy-as-code-với-gitops)
- [6. Audit, Debug & Replay](#6-audit-debug--replay)
  - [6.1 Explain API — Tại sao bị DENY?](#61-explain-api--tại-sao-bị-deny)
  - [6.2 Replay API — Tái hiện quyết định cũ](#62-replay-api--tái-hiện-quyết-định-cũ)
  - [6.3 Decision Log & Sampling Strategy](#63-decision-log--sampling-strategy)
- [7. Edge Cases — Xử lý trường hợp ngoại lệ](#7-edge-cases--xử-lý-trường-hợp-ngoại-lệ)
  - [EC-1: Temporal + Cache không nhất quán](#ec-1-temporal--cache-không-nhất-quán)
  - [EC-2: ReBAC Cycle Detection](#ec-2-rebac-cycle-detection)
  - [EC-3: Audit Log khi Sidecar Crash](#ec-3-audit-log-khi-sidecar-crash)
  - [EC-4: JIT Attribute Fetch thất bại](#ec-4-jit-attribute-fetch-thất-bại)
  - [EC-5: Escape Hatch Governance](#ec-5-escape-hatch-governance)
  - [EC-6: Big Node trong ReBAC Graph](#ec-6-big-node-trong-rebac-graph)
  - [EC-7: ReBAC Filter trên NoSQL Backend](#ec-7-rebac-filter-trên-nosql-backend)
  - [EC-8: Attribute Out-of-Sync](#ec-8-attribute-out-of-sync)
  - [EC-9: Fail-Open vs Fail-Closed](#ec-9-fail-open-vs-fail-closed)
  - [EC-10: Policy Version Diverge quá Ngưỡng](#ec-10-policy-version-diverge-quá-ngưỡng)
  - [EC-11: Decision Log Explosion](#ec-11-decision-log-explosion)
  - [EC-12: User vừa đổi Chi nhánh](#ec-12-user-vừa-đổi-chi-nhánh)
  - [EC-13: Permission kế thừa từ Role cha bị xung đột](#ec-13-permission-kế-thừa-từ-role-cha-bị-xung-đột)
  - [EC-14: Tenant Suspended — toàn bộ user bị DENY](#ec-14-tenant-suspended--toàn-bộ-user-bị-deny)
  - [EC-15: Sidecar mất kết nối với Control Plane](#ec-15-sidecar-mất-kết-nối-với-control-plane)
- [8. Vận hành & Giám sát](#8-vận-hành--giám-sát)
  - [8.1 Metrics quan trọng](#81-metrics-quan-trọng)
  - [8.2 Runbook: Phản ứng sự cố](#82-runbook-phản-ứng-sự-cố)
  - [8.3 Bảo trì định kỳ](#83-bảo-trì-định-kỳ)
- [9. Quy tắc & Giới hạn Hệ thống](#9-quy-tắc--giới-hạn-hệ-thống)
- [10. Câu hỏi Thường gặp (FAQ)](#10-câu-hỏi-thường-gặp-faq)

---

## 1. Tổng quan Kiến trúc

### 1.1 Mô hình 5 Lớp

```
Mọi request phân quyền đi qua 5 lớp tuần tự.
Lớp trước DENY → dừng ngay, không đánh giá lớp sau.

┌─────────────────────────────────────────────────────────┐
│  REQUEST: user_id + resource_type + action + context    │
└──────────────────────────┬──────────────────────────────┘
                           │
              ┌────────────▼────────────────┐
              │  LAYER 1: EMERGENCY REVOKE  │  ← O(1) in-memory
              │  Kiểm tra danh sách khóa   │    (không DB)
              └────────────┬────────────────┘
                    PASS   │ DENY → trả về ngay
              ┌────────────▼────────────────┐
              │  LAYER 2: TEMPORAL GATE     │  ← pure memory
              │  Giờ làm việc, IP, Ca trực  │    (không cache)
              └────────────┬────────────────┘
                    PASS   │ DENY → trả về ngay
              ┌────────────▼────────────────┐
              │  LAYER 3: RBAC              │  ← 1 SQL query
              │  Role hierarchy → Permission│    WITH RECURSIVE
              └────────────┬────────────────┘
                    ALLOW  │ không match → layer D
              ┌────────────▼────────────────┐
              │  LAYER 4: ABAC + ReBAC      │  ← AST eval
              │  Attribute + Relation graph  │    + graph lookup
              └────────────┬────────────────┘
                    ALLOW  │ DENY → trả về ngay
              ┌────────────▼────────────────┐
              │  LAYER 5: DATA FILTER       │  ← filter áp dụng
              │  Row filter + Field masking  │    vào response
              └────────────┬────────────────┘
                           │
              ┌────────────▼────────────────┐
              │  RESPONSE: ALLOW + filters   │
              └─────────────────────────────┘
```

### 1.2 Thành phần Hệ thống

| Thành phần | Vai trò | Công nghệ |
|-----------|---------|-----------|
| **authz-core** | Domain models, AST types, Errors | Rust |
| **authz-engine** | 5-layer pipeline evaluator | Rust + Tokio |
| **authz-db** | Repository layer + SQL migrations | Rust + sqlx |
| **authz-server** | HTTP REST API (Axum) | Rust + Axum |
| **PostgreSQL** | Persistence layer | PostgreSQL 16+ |
| **Redis/DashMap** | Emergency revoke cache, policy bundle | In-memory |

### 1.3 Nguyên tắc cốt lõi

| Nguyên tắc | Mô tả |
|-----------|-------|
| **Fail-Closed** | Không chắc → DENY (default cho banking) |
| **Explicit DENY** | DENY phải rõ ràng, không có implicit DENY |
| **Immutable Audit** | Log không bao giờ bị sửa/xóa |
| **No Hardcode** | Mọi policy, role, filter đều qua DB |
| **Monotonic Cache** | Cache key mang `attributes_version` — không dùng TTL dài |

---

## 2. Pipeline 5 Lớp — Luồng xử lý chi tiết

### 2.1 Layer 1 — Emergency Revoke

**Mục đích:** Khóa tức thì user bị compromise trước khi bất kỳ DB query nào chạy.

**Cơ chế:**
- Danh sách `user_id` bị khóa được load vào `DashSet` (in-memory, lock-free) khi server khởi động.
- Cập nhật real-time qua CDC (Debezium) từ bảng `authz_emergency_revoke`.
- Latency: **O(1)**, < 0.1ms.

**Khi nào trigger:**
- Phát hiện tài khoản bị tấn công / credential leak.
- User vi phạm chính sách nghiêm trọng cần dừng ngay lập tức.
- Tài khoản đang bị điều tra.

**Kết quả:**
- `cleared_at IS NULL` → DENY ngay, không kiểm tra layer nào khác.
- `expires_at` hết hạn → tự động bỏ khóa (engine check `expires_at`).

---

### 2.2 Layer 2 — Temporal Gate

**Mục đích:** Kiểm tra điều kiện thời gian và môi trường — không cache được.

> **Tại sao tách khỏi ABAC?** Nếu nhúng `env.now()` vào `condition_expr`, compiled predicate cache (P2) sẽ miss liên tục vì mỗi request có timestamp khác nhau → hiệu năng sập. Temporal gate tách ra, chạy trước cache, không ảnh hưởng cache key.

**Các điều kiện kiểm tra:**

| Điều kiện | Ví dụ | Cấu hình |
|----------|-------|---------|
| Ngày trong tuần | Chỉ T2–T6 | `allowed_days: [1,2,3,4,5]` |
| Khung giờ (theo timezone) | 07:30–17:30 ICT | `allowed_from`, `allowed_until`, `timezone` |
| IP CIDR allowlist | Chỉ từ mạng nội bộ | `allowed_cidr: ['10.0.0.0/8']` |
| Ca làm việc (shift) | Đang trong ca trực | `require_shift: true` + `shift_table_ref` |

**Luồng:**
1. Lấy `temporal_policy` từ bundle (đã cache local).
2. Tính toán thuần bộ nhớ: so sánh `now()` với `allowed_from/until` trong timezone.
3. Kiểm tra `client_ip` có nằm trong `allowed_cidr` không.
4. Nếu `require_shift = true` → JIT fetch từ shift service (EC-4).
5. Bất kỳ điều kiện nào fail → DENY với lý do cụ thể.

---

### 2.3 Layer 3 — RBAC (Role-Based Access Control)

**Mục đích:** Kiểm tra Role hierarchy → Permission cho resource_type + action.

**Cơ chế P1 — 1 Query duy nhất:**

```sql
-- Toàn bộ role hierarchy, permissions, filters được lấy trong 1 JOIN
WITH RECURSIVE role_tree AS (
    SELECT id, parent_role_id FROM role WHERE id = user_role.role_id
    UNION ALL
    SELECT r2.id, r2.parent_role_id FROM role r2
    JOIN role_tree rt ON r2.id = rt.parent_role_id
)
SELECT p.id, p.action, p.scope, rf.filter_expr, ff.allowed_fields, ...
FROM role_tree r_hier
JOIN role_permission rp ON rp.role_id = r_hier.id
JOIN permission p ON p.id = rp.permission_id AND p.resource_type = :resourceType
LEFT JOIN row_filter rf ...
LEFT JOIN field_filter ff ...
LEFT JOIN policy_rule pr ...
WHERE user_id = :userId AND (expires_at IS NULL OR expires_at > NOW())
ORDER BY priority DESC;
```

**Role kế thừa:**
- Role `SENIOR_ANALYST` có parent `ANALYST` → kế thừa tất cả permission của `ANALYST`.
- Engine traverse lên cây đến root, gom tất cả permission.
- `priority` cao hơn → được evaluate trước (dùng cho DENY override).

**Scope permission:**

| Scope | Ý nghĩa | Cách kiểm tra |
|-------|---------|---------------|
| `own` | Chỉ resource mình tạo | `resource.created_by == user.id` |
| `branch` | Tất cả resource trong branch của mình | ABAC: `user.branch_code == resource.branch_code` |
| `all` | Toàn hệ thống (tenant-wide) | Không cần filter thêm |
| `custom` | Tùy chỉnh hoàn toàn qua `row_filter` | Delegate sang Layer 5 |

---

### 2.4 Layer 4 — ABAC + ReBAC

**Mục đích:** Kiểm tra điều kiện phức tạp dựa trên attribute và quan hệ.

#### ABAC — AST Evaluation

Engine duyệt cây `condition_expr` đệ quy với short-circuit:
- `AND`: gặp `false` đầu tiên → dừng ngay (không tính các node còn lại).
- `OR`: gặp `true` đầu tiên → dừng ngay.

**Các loại ValueSource trong AST:**

| Type | Mô tả | Ví dụ |
|------|-------|-------|
| `user_attr` | Attribute của user | `user.branch_code`, `user.level` |
| `resource_field` | Field của resource | `resource.branchCode`, `resource.status` |
| `literal` | Giá trị cố định | `"ACTIVE"`, `["PENDING", "DRAFT"]` |
| `env` | Ngữ cảnh môi trường | `env.NOW`, `env.CURRENT_DATE`, `env.REQUEST_IP` |
| `external_attr` | Lấy từ service ngoài (JIT) | `shift_service.current_shift_status` |
| `relation` | Trigger ReBAC graph check | `relation.delegate_of` |

**Các toán tử hỗ trợ:**

| Operator | Ý nghĩa | Ví dụ |
|---------|---------|-------|
| `EQ` | Bằng | `user.branch == resource.branch` |
| `NEQ` | Khác | `resource.status != "ARCHIVED"` |
| `IN` | Nằm trong tập hợp | `resource.status IN ["PENDING","DRAFT"]` |
| `NOT_IN` | Không nằm trong tập | `resource.type NOT_IN ["CONFIDENTIAL"]` |
| `GTE` / `LTE` | Lớn/nhỏ hơn hoặc bằng | `user.level >= resource.min_level` |
| `LIKE` | Pattern match | `resource.code LIKE "VPB%"` |
| `IS_NULL` | Kiểm tra null | `resource.archived_at IS_NULL` |
| `EXISTS` | Tồn tại quan hệ (ReBAC) | `relation.delegate_of EXISTS` |

#### ReBAC — Graph Traversal

Được trigger khi AST có node `type: "relation"`.

**Luồng 2 tầng:**
1. **Tầng 1 — Materialized (O(1)):** Lookup `relation_reachability` → nếu có → trả về ngay.
2. **Tầng 2 — Live Traversal (Fallback):** `WITH RECURSIVE` trên `relation_tuple`, giới hạn `depth = 10`.

**Circuit Breaker:**
- Nếu live traversal fail sau 3 lần → circuit OPEN → DENY + ghi `REBAC_CIRCUIT_OPEN` vào audit.
- Circuit tự reset sau 30 giây.

---

### 2.5 Layer 5 — Data Filter

**Mục đích:** Không DENY request — thay vào đó lọc data trả về chỉ còn phần được phép xem.

Hai loại filter:
- **Row Filter:** Lọc hàng (inject vào WHERE clause / ES filter / MongoDB $match).
- **Field Filter:** Che giấu trường (remove field hoặc replace bằng mask pattern).

> **Quan trọng:** Layer 5 không dừng request. Engine vẫn trả về ALLOW, nhưng response data đã được lọc.

---

## 3. Hướng dẫn Nghiệp vụ theo Usecase

### 3.1 Phân quyền theo Chi nhánh (Branch Isolation)

**Bài toán:** Chuyên viên chi nhánh Hà Nội chỉ được xem hồ sơ của chi nhánh Hà Nội.

**Cấu hình:**

```sql
-- 1. Tạo role
INSERT INTO role (tenant_id, code, name) VALUES (:tid, 'BRANCH_SPECIALIST', 'Chuyên viên Chi nhánh');

-- 2. Tạo permission với scope = 'branch'
INSERT INTO permission (tenant_id, code, resource_type, action, scope)
VALUES (:tid, 'READ_DOCUMENT_BRANCH', 'document', 'read', 'branch');

-- 3. Gán row filter: chỉ document cùng branch
INSERT INTO row_filter (permission_id, tenant_id, resource_type, filter_expr)
VALUES (
    :permId, :tid, 'document',
    '{
        "type": "LEAF",
        "left":  {"type": "user_attr",     "key": "branch_code"},
        "op":    "EQ",
        "right": {"type": "resource_field","key": "branchCode"}
    }'
);

-- 4. Gán user vào role
INSERT INTO user_role (user_id, role_id, tenant_id) VALUES (:userId, :roleId, :tid);
```

**Luồng evaluate:**
1. Layer 1–2: PASS.
2. Layer 3 RBAC: user có `READ_DOCUMENT_BRANCH` với `scope=branch`.
3. Layer 4 ABAC: kiểm tra `user.branch_code == resource.branchCode` → ALLOW.
4. Layer 5: inject `WHERE branch_code = 'HN01'` vào query.

**Kết quả:** User chỉ thấy document của HN01, không thể xem HCM01 dù biết ID.

---

### 3.2 Phân quyền Tạm thời (Temporary Permission)

**Bài toán:** Manager ủy quyền cho nhân viên A xử lý một nhóm hồ sơ trong 3 ngày.

**Cấu hình:**

```sql
-- Gán role tạm thời với expires_at
INSERT INTO user_role (user_id, role_id, tenant_id, expires_at, granted_by, grant_reason)
VALUES (
    :userId, :roleId, :tid,
    NOW() + INTERVAL '3 days',
    :managerId,
    'Hỗ trợ xử lý backlog tháng 6 — Ticket #JIRA-1234'
);
```

**Lưu ý vận hành:**
- Hết hạn → engine tự động bỏ qua `user_role` này (không cần cleanup job).
- Kiểm tra quyền đang còn hiệu lực: `WHERE expires_at IS NULL OR expires_at > NOW()`.
- Muốn thu hồi sớm: `DELETE FROM user_role WHERE id = :id`.
- Muốn gia hạn: `UPDATE user_role SET expires_at = :newExpiry WHERE id = :id`.

> **Edge case:** User đang thực hiện request đúng lúc `expires_at` chạm ngưỡng → request hiện tại vẫn được xử lý đến hoàn thành (vì check tại thời điểm bắt đầu request). Request tiếp theo sẽ bị DENY.

---

### 3.3 Phân quyền theo Instance (Resource-scoped Role)

**Bài toán:** User B chỉ được duyệt hợp đồng số #456, không phải toàn bộ.

**Cấu hình:**

```sql
-- 1. Đăng ký resource instance (chỉ cần cho hợp đồng đặc biệt)
INSERT INTO resource_instance (resource_type_id, tenant_id, external_ref, owner_id)
VALUES (:contractTypeId, :tid, 'contract-456', :ownerId);

-- 2. Gán role REVIEWER chỉ cho instance đó
INSERT INTO user_role (user_id, role_id, tenant_id, resource_scope_id)
VALUES (:userId, :reviewerRoleId, :tid, :contractInstanceId);
```

**Luồng evaluate:**
- Engine kiểm tra `user_role.resource_scope_id`:
  - `NULL` → global role, áp dụng tất cả resource của type đó.
  - `= :resourceInstanceId` → chỉ áp dụng cho instance này.
- Khi evaluate, engine match `resource_scope_id` với `resource.external_ref` được extract từ request.

**Kết quả:** User B có quyền REVIEWER trên contract-456, nhưng `GET /contracts/789` → DENY.

---

### 3.4 Ủy quyền bắc cầu (Delegation Chain — ReBAC)

**Bài toán:** A ủy quyền cho B, B ủy quyền cho C. C muốn xem tài liệu mà A là chủ sở hữu.

**Cấu hình Relation Tuples:**

```sql
-- A → B: A ủy quyền cho B
INSERT INTO relation_tuple (tenant_id, subject, relation, object, expires_at)
VALUES (:tid, 'user:A', 'delegate_of', 'user:B', NULL);

-- B → C: B ủy quyền cho C (có thời hạn)
INSERT INTO relation_tuple (tenant_id, subject, relation, object, expires_at)
VALUES (:tid, 'user:B', 'delegate_of', 'user:C', NOW() + INTERVAL '7 days');
```

**Policy AST sử dụng relation node:**

```json
{
  "type": "OR",
  "conditions": [
    {
      "type": "LEAF",
      "left":  {"type": "user_attr",     "key": "id"},
      "op":    "EQ",
      "right": {"type": "resource_field","key": "created_by"}
    },
    {
      "type": "LEAF",
      "left":  {"type": "relation", "key": "delegate_of", "target": "resource.owner_id"},
      "op":    "EXISTS"
    }
  ]
}
```

**Luồng khi C request xem tài liệu của A:**
1. ABAC leaf 1: `C.id == resource.created_by` → `false` (C không phải chủ).
2. ABAC leaf 2: `relation.EXISTS(C --delegate_of→ ... → A)` → trigger ReBAC.
3. ReBAC: lookup `relation_reachability` → tìm path `C → B → A` → `true`.
4. ALLOW.

**Kiểm tra chain có bị cycle không:**
- Trigger `fn_check_relation_cycle` tự động ngăn khi insert `A delegate_of B` nếu `B delegate_of A` đã tồn tại.

**Thu hồi ủy quyền:**

```sql
-- Thu hồi ngay lập tức
DELETE FROM relation_tuple WHERE subject = 'user:B' AND relation = 'delegate_of' AND object = 'user:C';

-- CDC event → recompute relation_reachability → C mất quyền bắc cầu trong vài giây
```

---

### 3.5 Phân quyền theo Ca làm việc (Temporal + Shift)

**Bài toán:** Nhân viên chỉ được xem dữ liệu trong giờ hành chính VÀ đang trong ca trực.

**Cấu hình:**

```sql
-- Đăng ký external attribute source cho shift service
INSERT INTO external_attribute_source (tenant_id, code, name, base_url, attribute_path, cache_ttl_secs, timeout_ms)
VALUES (
    :tid, 'shift_service', 'Shift Management Service',
    'http://shift-svc.internal',
    '/api/v1/users/{userId}/shift-status',
    30,   -- cache 30 giây
    150   -- timeout 150ms
);

-- Tạo temporal policy cho permission
INSERT INTO temporal_policy (permission_id, tenant_id, name, allowed_days, allowed_from, allowed_until, timezone, require_shift, shift_table_ref, is_active)
VALUES (
    :permId, :tid, 'Giờ hành chính VPBank',
    '{1,2,3,4,5}',  -- T2-T6
    '07:30', '17:30',
    'Asia/Ho_Chi_Minh',
    true,
    'shift_service:on_shift',  -- key trong external_attribute_source
    true
);
```

**Luồng evaluate Layer 2:**
1. Kiểm tra ngày: hôm nay có trong `{1,2,3,4,5}` không?
2. Kiểm tra giờ: `07:30 ≤ now_ICT ≤ 17:30`?
3. `require_shift = true` → JIT fetch `shift_service/users/{userId}/shift-status`.
4. Cache key: `shift_service:{userId}:on_shift` với TTL 30 giây.
5. Nếu fetch thất bại và `fallback_value = null` → DENY với lý do `JIT_UNAVAILABLE`.
6. Tất cả PASS → tiếp tục pipeline.

**Edge case:** Nhân viên đang làm việc thì hết giờ (17:30):
- Request đang thực hiện → hoàn thành bình thường.
- Request mới sau 17:30 → Layer 2 DENY với lý do `Outside working hours: 17:31:05`.

---

### 3.6 Phân quyền đa tầng kết hợp (Composite)

**Bài toán:** SENIOR_ANALYST chi nhánh HN được phê duyệt khoản vay > 5 tỷ VND trong giờ hành chính NHƯNG chỉ khi khoản vay do họ phụ trách.

**Cấu hình AST:**

```json
{
  "type": "AND",
  "conditions": [
    {
      "comment": "Cùng chi nhánh",
      "type": "LEAF",
      "left":  {"type": "user_attr",     "key": "branch_code"},
      "op":    "EQ",
      "right": {"type": "resource_field","key": "branchCode"}
    },
    {
      "comment": "Khoản vay lớn hơn 5 tỷ",
      "type": "LEAF",
      "left":  {"type": "resource_field","key": "amountVnd"},
      "op":    "GTE",
      "right": {"type": "literal",       "value": 5000000000}
    },
    {
      "comment": "User là người phụ trách hoặc được ủy quyền",
      "type": "OR",
      "conditions": [
        {
          "type": "LEAF",
          "left":  {"type": "user_attr",     "key": "id"},
          "op":    "EQ",
          "right": {"type": "resource_field","key": "assigned_officer_id"}
        },
        {
          "type": "LEAF",
          "left":  {"type": "relation", "key": "delegate_of", "target": "resource.assigned_officer_id"},
          "op":    "EXISTS"
        }
      ]
    }
  ]
}
```

**Thứ tự evaluate (short-circuit AND):**
1. `branch_code` mismatch → DENY ngay (bỏ qua 2 điều kiện còn lại).
2. `amountVnd < 5B` → DENY ngay (bỏ qua điều kiện 3).
3. Kiểm tra OR: là người phụ trách hoặc được ủy quyền.
4. Temporal gate (Layer 2) đã check giờ hành chính rồi → không cần check lại ở đây.

---

### 3.7 Khóa khẩn cấp (Emergency Revoke)

**Bài toán:** Phát hiện tài khoản `user-A` bị lộ thông tin lúc 3 giờ sáng — cần khóa ngay lập tức.

**Quy trình:**

```sql
-- 1. Ghi vào DB (nguồn sự thật)
INSERT INTO authz_emergency_revoke (tenant_id, user_id, reason, revoked_by, expires_at)
VALUES (
    :tid, 'user-A-uuid',
    'Credential leak detected — ticket #SEC-789',
    :adminId,
    NOW() + INTERVAL '48 hours'  -- tự hết hạn sau 48h nếu không clear thủ công
);

-- 2. CDC tự động cập nhật DashSet trong-memory của tất cả sidecar/engine instance
-- Latency: < 2 giây (Debezium CDC → Kafka → consumer)
```

**Trong thời gian chờ CDC propagate (< 2s), dùng direct API:**

```bash
# Gọi REST API của authz-server để push trực tiếp vào memory
POST /admin/emergency-revoke
{
  "userId": "user-A-uuid",
  "reason": "Credential leak detected",
  "expiresInHours": 48
}
```

**Giải khóa sau điều tra:**

```sql
UPDATE authz_emergency_revoke
SET cleared_at = NOW(),
    cleared_by = :adminId,
    clear_note = 'Đã đổi password, xác nhận không có truy cập trái phép — ticket #SEC-789 RESOLVED'
WHERE user_id = :userId AND cleared_at IS NULL;
```

**Đặc điểm:**
- Layer 1 check trước tất cả → latency < 0.1ms → không ảnh hưởng throughput.
- Không cần invalidate cache hay policy bundle — Emergency Revoke độc lập hoàn toàn.
- Audit trail đầy đủ: `revoked_by`, `reason`, `cleared_by`, `clear_note`.

---

### 3.8 Multi-tenant Isolation

**Nguyên tắc cứng:**
- Mọi query trong repository đều phải có `WHERE tenant_id = :tenantId`.
- Mọi entity (role, permission, policy, resource) đều carry `tenant_id`.
- Cross-tenant access là **KHÔNG THỂ** — không có exception.

**Tenant Suspended:**

```sql
-- Khi tenant vi phạm SLA hoặc nợ phí → SUSPENDED
UPDATE tenant SET status = 'SUSPENDED' WHERE id = :tenantId;
```

- Trigger `fn_check_tenant_active` ngăn tạo entity mới trong tenant suspended.
- Engine kiểm tra `tenant.status` tại entry point → tất cả request trả về `DENY: TENANT_SUSPENDED`.
- Không cần invalidate cache — status check là synchronous.

---

## 4. Data Filter — Lọc dữ liệu trả về

### 4.1 Row Filter — Lọc hàng

**Mục đích:** Inject filter vào query → user chỉ thấy tập con dữ liệu được phép.

**Ví dụ: User chỉ xem document STATUS = ACTIVE hoặc PENDING_REVIEW:**

```json
{
  "type": "OR",
  "conditions": [
    {
      "type": "LEAF",
      "left":  {"type": "resource_field","key": "status"},
      "op":    "EQ",
      "right": {"type": "literal",       "value": "ACTIVE"}
    },
    {
      "type": "LEAF",
      "left":  {"type": "resource_field","key": "status"},
      "op":    "EQ",
      "right": {"type": "literal",       "value": "PENDING_REVIEW"}
    }
  ]
}
```

**SQL translator sinh ra:**
```sql
WHERE (status = 'ACTIVE' OR status = 'PENDING_REVIEW')
```

**ES translator sinh ra:**
```json
{"bool": {"should": [{"term": {"status": "ACTIVE"}}, {"term": {"status": "PENDING_REVIEW"}}]}}
```

**MongoDB translator sinh ra:**
```json
{"$or": [{"status": "ACTIVE"}, {"status": "PENDING_REVIEW"}]}
```

**Kết hợp nhiều row filter:**
Khi user có nhiều permission (qua nhiều role), tất cả row filter được kết hợp bằng `AND` (intersection — an toàn nhất).

---

### 4.2 Field Filter — Che giấu trường nhạy cảm

**Hai chế độ:**

| Chế độ | Cấu hình | Kết quả |
|--------|---------|---------|
| **Allowlist** | `allowed_fields: ["id","name","status","branch"]` | Chỉ trả về các field trong list |
| **Masklist** | `masked_fields: ["credit_card","ssn"]`, `mask_pattern: "****"` | Trả về tất cả field, field nhạy cảm bị thay thế |

**Cấu hình:**

```sql
-- Ví dụ: Chuyên viên thấy thông tin cơ bản, che thẻ tín dụng và CCCD
INSERT INTO field_filter (permission_id, tenant_id, resource_type, masked_fields, mask_pattern, mask_strategy)
VALUES (
    :permId, :tid, 'customer_profile',
    ARRAY['credit_card_number', 'national_id'],
    '****',
    'REPLACE'
);
```

**Mask strategies:**

| Strategy | Hành vi | Ví dụ |
|---------|---------|-------|
| `REPLACE` | Thay toàn bộ giá trị | `4111-1111-1111-1111` → `****` |
| `TRUNCATE` | Giữ N ký tự đầu | `4111-1111-1111-1111` → `4111-****` |
| `HASH` | SHA256 prefix | `4111...` → `a8f5f167...` |

**Sensitive fields tự động:**
- Các field trong `resource_type.schema_def.sensitive_fields` bị mask tự động.
- Field phải có trong `allowed_fields` để được trả về rõ ràng.

---

### 4.3 Filter cho đa Backend (SQL / ES / MongoDB)

**Nguyên tắc:** Viết filter AST một lần, áp dụng tự động cho mọi backend.

**Vấn đề đặc biệt với NoSQL (ES / MongoDB):**
- SQL có thể dùng subquery: `WHERE id IN (SELECT object FROM relation_reachability WHERE subject = 'user:X')`.
- ES và MongoDB không có JOIN/subquery → phải **pre-fetch IDs** rồi inject `terms`/`$in`.

**Luồng với ES backend khi có relation node:**
1. Engine detect filter AST có `type: "relation"`.
2. Gọi `ReBacEngine.resolveObjects(user, relation)` → lấy danh sách IDs.
3. Giới hạn 65,536 IDs (giới hạn ES terms query).
4. Inject: `{"terms": {"created_by": ["uuid-1","uuid-2",...]}}`.

> **Giới hạn 65,536 IDs:** Nếu user có quan hệ với > 65,536 object → cảnh báo, inject partial list + ghi flag `truncated: true` vào eval_trace. Cần xem xét Group Partition (EC-6).

---

## 5. Policy Engine — Quản lý và Triển khai Policy

### 5.1 Vòng đời Policy (DRAFT → SHADOW → ACTIVE → ARCHIVED)

```
          PR Review          Shadow Run          Production
            (Git)              (7 ngày)
DRAFT ──────────────► SHADOW ──────────────► ACTIVE ──► ARCHIVED
              │                  │              │
              │     (diverge     │    (supersede│
              │      > 5%)       │    by newer) │
              └──── REJECTED     └─── BLOCKED   └──────────────────
```

**Trạng thái:**

| Status | Ý nghĩa | Ai query |
|--------|---------|---------|
| `DRAFT` | Đang soạn thảo, chưa test | Không ai |
| `SHADOW` | Chạy song song với ACTIVE | Shadow engine (async, không ảnh hưởng response) |
| `ACTIVE` | Đang áp dụng thực tế | Tất cả request |
| `ARCHIVED` | Đã thay thế, lưu lại | Audit / Replay API |

**Ràng buộc:** Chỉ có **đúng 1 version ACTIVE** mỗi policy tại một thời điểm (enforced bởi partial unique index + trigger `fn_enforce_one_active_policy_version`).

---

### 5.2 Shadow Mode — Kiểm thử Policy an toàn

**Mục đích:** Chạy policy mới song song với policy đang hoạt động. So sánh kết quả. Chỉ promote nếu divergence thấp.

**Luồng:**
1. Promote version lên `SHADOW`.
2. Mỗi request → engine evaluate cả `ACTIVE` và `SHADOW` (SHADOW async, không block response).
3. Khi kết quả khác nhau → ghi vào `policy_shadow_log` với `diverged = true` (generated column).
4. Sau 7 ngày, xem báo cáo divergence.

**Báo cáo divergence:**

```sql
SELECT
    COUNT(*) FILTER (WHERE diverged) AS diverged_count,
    COUNT(*) AS total_count,
    ROUND(100.0 * COUNT(*) FILTER (WHERE diverged) / COUNT(*), 2) AS diverge_pct,
    COUNT(*) FILTER (WHERE shadow_decision='DENY' AND active_decision='ALLOW') AS new_denials,
    COUNT(*) FILTER (WHERE shadow_decision='ALLOW' AND active_decision='DENY') AS new_allows
FROM policy_shadow_log
WHERE policy_version_id = :shadowVersionId
  AND logged_at > NOW() - INTERVAL '7 days';
```

**Ngưỡng ra quyết định:**

| Divergence % | Hành động |
|-------------|----------|
| 0–1% | Có thể promote ngay |
| 1–5% | Review các case diverged, confirm OK rồi promote |
| > 5% | BLOCK promote — bắt buộc phân tích nguyên nhân |
| `new_denials > 0` và nghiêm trọng | BLOCK — cần approval của Security team |

---

### 5.3 Policy Conflict Resolution — Xử lý xung đột

**Tình huống:** User có Role A (ALLOW document.read) và Role B (DENY document.read), cùng priority.

**Chiến lược theo từng `resource_type`:**

| Chiến lược | Hành vi | Áp dụng |
|-----------|---------|---------|
| `DENY_OVERRIDES` | 1 DENY bất kỳ → DENY | Banking (default) |
| `PERMIT_OVERRIDES` | 1 ALLOW bất kỳ → ALLOW | Internal tool |
| `FIRST_MATCH_WINS` | Policy priority cao nhất win | Complex rules |
| `UNANIMOUS_PERMIT` | Tất cả phải ALLOW → ALLOW | Tài liệu siêu mật |

**Cấu hình:**

```sql
UPDATE resource_type
SET conflict_strategy = 'DENY_OVERRIDES'
WHERE tenant_id = :tid AND code = 'document';
```

**Phát hiện conflict trong CI/CD:**
- `authz-cli validate` kiểm tra tất cả policy trước khi deploy.
- Cảnh báo nếu 2 policy cùng `priority`, cùng `resource_type + action`, effect ngược nhau.
- Warning, không block — admin phải review và set `tiebreak_order` nếu cần.

---

### 5.4 Policy-as-Code với GitOps

**Cấu trúc thư mục:**

```
policies/
├── vpbank/
│   ├── branch-isolation.yaml
│   ├── loan-approval.yaml
│   └── document-archive.yaml
└── pdms/
    ├── reviewer-access.yaml
    └── admin-override.yaml
```

**Ví dụ policy YAML:**

```yaml
apiVersion: authz.enterprise/v1
kind: Policy
metadata:
  name: branch-isolation
  tenant: vpbank
  version: "4"
spec:
  effect: ALLOW
  priority: 100
  rules:
    - subjectType: ROLE
      resourceType: document
      action: read
      condition:
        type: AND
        conditions:
          - left:  {type: user_attr,     key: branch_code}
            op:    EQ
            right: {type: resource_field,key: branchCode}
          - left:  {type: resource_field,key: status}
            op:    IN
            right: {type: literal, value: [ACTIVE, PENDING_REVIEW]}
  temporalPolicy:
    allowedDays: [1,2,3,4,5]
    allowedFrom: "07:30"
    allowedUntil: "17:30"
    timezone: Asia/Ho_Chi_Minh
```

**CI/CD Pipeline:**

```yaml
# .github/workflows/policy-deploy.yml
steps:
  - name: Validate schema & AST
    run: authz-cli validate policies/**/*.yaml
    # Check: không escape hatch, field names tồn tại trong schema_field_registry

  - name: Deploy to Shadow
    run: authz-cli shadow-deploy --policy branch-isolation --duration 7d

  - name: Check divergence (chạy sau 7 ngày)
    run: authz-cli check-divergence --policy branch-isolation --max-pct 5

  - name: Promote to ACTIVE
    if: divergence check passed
    run: authz-cli promote --policy branch-isolation
```

---

## 6. Audit, Debug & Replay

### 6.1 Explain API — Tại sao bị DENY?

**Endpoint:** `GET /authz/explain?userId={}&resourceRef={}&action={}`

**Response mẫu:**

```json
{
  "decision": "DENY",
  "decided_at": "2026-06-01T10:23:45Z",
  "matched_policy": "branch-isolation-v3",
  "trace": {
    "layers": {
      "emergency_revoke": {"result": "PASS"},
      "temporal_gate": {"result": "PASS"},
      "rbac": {"result": "ALLOW", "matched_roles": ["BRANCH_SPECIALIST"]},
      "abac": {
        "result": "DENY",
        "tree": {
          "type": "AND",
          "result": false,
          "children": [
            {
              "node": "user_attr[branch_code] EQ resource_field[branchCode]",
              "left_value": "HN01",
              "right_value": "HCM01",
              "result": false,
              "reason": "HN01 ≠ HCM01 — User thuộc chi nhánh HN01, document thuộc HCM01"
            }
          ]
        }
      }
    }
  },
  "user_attributes_at_decision": {"branch_code": "HN01", "level": 3},
  "resource_attributes_at_decision": {"branchCode": "HCM01", "status": "ACTIVE"}
}
```

**Cách đọc trace:**
1. Xem `layers` từ trên xuống — tìm layer đầu tiên có `result: DENY/FAIL`.
2. Nếu `abac.result = DENY` → xem `abac.tree` → tìm node `result: false`.
3. `reason` giải thích chính xác giá trị nào không khớp.

---

### 6.2 Replay API — Tái hiện quyết định cũ

**Endpoint:** `POST /authz/replay`

**Usecase:** Sau khi nâng cấp policy, kiểm tra "nếu policy mới áp dụng cho request 3 tháng trước thì kết quả có khác không?"

```json
// Request
{
  "decision_id": "uuid-của-decision-log-cũ",
  "use_current_policy": true
}

// Response
{
  "original_decision": "DENY",
  "replay_decision": "ALLOW",
  "changed": true,
  "explanation": "Policy branch-isolation v3 → v4: thêm exception cho senior level ≥ 4"
}
```

**Usecase khác:** Điều tra incident — xem chính xác user A lúc 14:00 ngày X có thể làm gì:
```bash
GET /authz/explain?userId={A}&resourceRef={doc-123}&action=read&at=2026-05-15T14:00:00Z
```

---

### 6.3 Decision Log & Sampling Strategy

**Vấn đề:** 100M+ requests/ngày → log đầy đủ → 200GB+/ngày.

**Chiến lược sampling theo resource_type:**

```sql
-- Cấu hình log_sampling trong resource_type
UPDATE resource_type
SET log_sampling = '{
    "DENY": 1.0,    -- log 100% DENY (audit bắt buộc)
    "ALLOW": 0.01   -- log 1% ALLOW (monitoring sample)
}'
WHERE code = 'document';

-- Tài liệu siêu nhạy cảm: log nhiều hơn
UPDATE resource_type
SET log_sampling = '{"DENY": 1.0, "ALLOW": 0.1}'
WHERE code = 'secret_contract';
```

**Cơ chế:**
- DENY: **luôn** log 100% (không thể bỏ qua — yêu cầu audit).
- ALLOW: random sampling theo `sampleRate`.
- ALLOW không được log → chỉ tăng Prometheus counter `authz.allow.total`.

---

## 7. Edge Cases — Xử lý trường hợp ngoại lệ

### EC-1: Temporal + Cache không nhất quán

**Tình huống:** Policy cache (compiled predicate) vẫn còn hạn nhưng temporal_policy vừa được cập nhật (VD: rút ngắn giờ làm việc từ 17:30 xuống 17:00).

**Giải pháp:**
- Temporal policy được load từ **policy bundle** (hot-swappable in-memory).
- Khi admin update `temporal_policy` → publish event → bundle reload.
- Bundle reload atomic (swap con trỏ `volatile` reference).
- Không cần restart service.

**Lưu ý vận hành:** Sau khi update temporal_policy, bundle propagate đến tất cả sidecar trong < 5 giây (qua Kafka). Trong window 5 giây này, có thể có request pass temporal gate với policy cũ.

---

### EC-2: ReBAC Cycle Detection

**Tình huống:** Cố tình insert `C delegate_of A` khi đã có `A → B → C`.

**Cơ chế ngăn chặn:**
- Trigger `fn_check_relation_cycle` chạy `WITH RECURSIVE` trước mỗi INSERT.
- Nếu phát hiện cycle → `RAISE EXCEPTION 'check_violation'` → INSERT bị rollback.
- Application nhận `SqlxError::Database` với errcode `check_violation` → trả về 400 Bad Request.

**Response khi bị chặn:**
```json
{
  "error": "CYCLE_DETECTED",
  "message": "Cannot create relation: user:C delegate_of user:A would create a cycle (A→B→C→A)",
  "code": 400
}
```

**Lưu ý:** Trigger check có giới hạn depth = 15 để tránh chạy lâu. Với graph rất lớn, application-level DFS nên check trước khi gọi INSERT.

---

### EC-3: Audit Log khi Sidecar Crash

**Tình huống:** Sidecar xử lý xong AuthZ request, đang relay audit log lên Kafka thì pod crash.

**Giải pháp — Local WAL Buffer:**
1. Audit event được ghi vào local Chronicle Queue (disk-persisted, ~1μs) **trước khi return response**.
2. Async relay lên Kafka/IAM.
3. Khi pod restart → WAL relay agent đọc lại WAL từ điểm cuối đã commit.
4. IAM Service dùng `ON CONFLICT (id) DO NOTHING` → idempotent, không duplicate.

**Kubernetes preStop Hook:**
```yaml
lifecycle:
  preStop:
    exec:
      command: ["/bin/sh", "-c", "curl -X POST localhost:8080/actuator/wal-flush && sleep 5"]
```
- `wal-flush` endpoint flush toàn bộ WAL synchronously trước khi K8s terminate pod.
- `terminationGracePeriodSeconds: 30` đảm bảo đủ thời gian.

---

### EC-4: JIT Attribute Fetch thất bại

**Tình huống 1:** Shift service down → fetch thất bại → `fallback_value = null`.

**Hành vi:** Engine fail-closed → DENY với lý do `JIT_UNAVAILABLE: shift_service`.

**Tình huống 2:** Shift service down → `fallback_value = {"on_shift": false}`.

**Hành vi:** Engine dùng fallback → nếu policy cần `on_shift = true` → DENY.

**Tình huống 3:** Shift service slow (> 150ms timeout).

**Hành vi:** Request timeout → Circuit Breaker ghi nhận 1 failure. Sau 3 failures liên tiếp → Circuit OPEN → mọi JIT fetch cho source này đều dùng fallback ngay, không gọi thực tế.

**Monitoring:** Alert khi `jit_fetch_failure_rate > 1%` trong 5 phút liên tiếp.

---

### EC-5: Escape Hatch Governance

**Tình huống:** Developer muốn dùng raw SQL trong row_filter vì "viết AST phức tạp".

**Cơ chế ngăn chặn:**
- Trigger `fn_enforce_escape_hatch_approval` chặn INSERT nếu `sql_fragment IS NOT NULL` mà không có `escape_hatch_approved_by`.
- CI/CD `authz-cli validate` reject policy YAML có escape hatch.

**Quy trình hợp lệ khi thực sự cần:**
1. Tạo Jira ticket với đủ justification.
2. Security team approve ticket.
3. INSERT với đủ 4 governance fields:
```sql
INSERT INTO row_filter (permission_id, resource_type, filter_expr, sql_fragment,
    escape_hatch_reason, escape_hatch_approved_by, escape_hatch_approved_at, escape_hatch_ticket_ref)
VALUES (
    :permId, 'document', '{}',
    'EXISTS (SELECT 1 FROM special_contract_lookup WHERE ...)',
    'AST không thể express subquery correlated này — xem phân tích trong ticket',
    :securityTeamLeadId,
    NOW(),
    'JIRA-SEC-456'
);
```

---

### EC-6: Big Node trong ReBAC Graph

**Tình huống:** Group `ALL_EMPLOYEES` có 50,000 members. Thêm 1 member mới → recompute 50,000 rows → CDC pipeline nghẽn.

**Giải pháp:**

**Bước 1:** Set max_fanout cho relation `member_of`:
```sql
UPDATE relation_type SET max_fanout = 10000 WHERE tenant_id = :tid AND relation = 'member_of';
```

**Bước 2:** Phân rã group lớn thành sub-partitions:
```sql
-- Tạo virtual group hierarchy
INSERT INTO group_partition (tenant_id, parent_group, child_group, partition_key, max_size)
VALUES
    (:tid, 'group:ALL_EMPLOYEES', 'group:ALL_EMPLOYEES_HN',  'branch_code=HN', 5000),
    (:tid, 'group:ALL_EMPLOYEES', 'group:ALL_EMPLOYEES_HCM', 'branch_code=HCM', 5000),
    (:tid, 'group:ALL_EMPLOYEES', 'group:ALL_EMPLOYEES_DN',  'branch_code=DN',  5000);
```

**Bước 3:** Di chuyển members vào sub-partitions:
```sql
-- Xóa tuples cũ, thêm vào sub-partitions
DELETE FROM relation_tuple WHERE subject = 'user:X' AND relation = 'member_of' AND object = 'group:ALL_EMPLOYEES';
INSERT INTO relation_tuple (tenant_id, subject, relation, object)
VALUES (:tid, 'user:X', 'member_of', 'group:ALL_EMPLOYEES_HN');
```

**Kết quả:** Mỗi sub-group chỉ recompute subgraph nhỏ. CDC pipeline không bị nghẽn.

---

### EC-7: ReBAC Filter trên NoSQL Backend

**Tình huống:** Row filter có relation node, backend là Elasticsearch, ES không có subquery.

**Giải pháp — ID Enrichment:**
1. Engine detect relation node trong filter AST.
2. Gọi `ReBacEngine.resolveObjects(user, relation)` → `["uuid-1", "uuid-3", "uuid-7"]`.
3. Inject vào ES query: `{"terms": {"created_by": ["uuid-1","uuid-3","uuid-7"]}}`.

**Edge case — > 65,536 IDs:**
- ES giới hạn `terms` query ở 65,536 items.
- Engine inject partial list + ghi flag `truncated: true` vào `eval_trace`.
- Alert: `rebac_terms_truncated` counter tăng → cần review group partitioning.

**Edge case — 0 IDs:**
- User không có quan hệ nào → engine inject `{"match_none": {}}` → ES trả về 0 kết quả.
- User thấy list rỗng, không bị error.

---

### EC-8: Attribute Out-of-Sync

**Tình huống:** Nhân viên vừa được chuyển từ chi nhánh HN sang HCM lúc 14:00. Lúc 14:05, nhân viên vẫn đang thấy document của HN.

**Nguyên nhân:** Cache `authz:ctx:{userId}:{version}` chưa bị invalidate.

**Giải pháp:**
1. Keycloak phát event `user.attribute.changed` khi admin cập nhật.
2. Event → Kafka `iam.user.attribute.changed` → IAM consumer:
   - `UPDATE user_account SET attributes = :new, attributes_version = :newVersion WHERE id = :userId AND attributes_version < :newVersion`
   - Invalidate cache key cũ: `DEL authz:ctx:{userId}:*`.
3. Request tiếp theo của nhân viên → JWT chứa `attr_version` mới → cache miss → reload từ DB.

**Worst case latency:** Kafka propagate ~2 giây. Trong 2 giây đó, cache version cũ vẫn valid → nhân viên vẫn thấy data cũ.

**Giải pháp cho zero-tolerance:**
```sql
-- Tăng version, admin chủ động invalidate
UPDATE user_account
SET attributes_version = attributes_version + 1  -- force cache miss ngay
WHERE id = :userId;
```

---

### EC-9: Fail-Open vs Fail-Closed

**Tình huống:** Sidecar mất kết nối với control plane, policy bundle không có trong memory.

**Cấu hình per tenant:**

```json
// tenant.config
{
  "fail_mode": "DENY",  // banking: fail-closed
  "fail_mode": "OPEN"   // internal tool: fail-open
}
```

**Hành vi:**

| fail_mode | Khi bundle null | Khi ReBAC circuit open | Khi JIT unavailable + no fallback |
|-----------|----------------|----------------------|----------------------------------|
| `DENY` | DENY all | DENY | DENY |
| `OPEN` | ALLOW all | ALLOW | ALLOW |

**Khuyến nghị:**
- Banking, tài chính, y tế: `DENY` (fail-closed).
- Internal analytics, reporting tool: `OPEN` (fail-open — ưu tiên availability).
- Không bao giờ dùng `OPEN` cho resource có dữ liệu nhạy cảm (PII, financial).

---

### EC-10: Policy Version Diverge quá Ngưỡng

**Tình huống:** Shadow policy mới có `diverge_pct = 8%` (> ngưỡng 5%).

**Quy trình xử lý:**

```bash
# 1. Xem các case bị diverge
authz-cli shadow-report --policy branch-isolation-v4 --show-diverged

# 2. Phân tích: diverge do ALLOW→DENY hay DENY→ALLOW?
SELECT shadow_decision, active_decision, COUNT(*), resource_ref
FROM policy_shadow_log
WHERE policy_version_id = :v4Id AND diverged = true
GROUP BY 1, 2, 3;

# 3. Nếu new_denials tăng đột biến → review policy condition
# 4. Sửa policy → tạo version mới (v5) → shadow lại
# 5. Version v4 → ARCHIVED, v5 → SHADOW
```

**Không thể force promote** nếu diverge > threshold — trigger `fn_enforce_one_active_policy_version` và CI/CD gate đều block.

---

### EC-11: Decision Log Explosion

**Tình huống:** 100M requests/ngày → log table đầy nhanh.

**Giải pháp đã tích hợp:**

**Tầng 1 — Sampling:**
- DENY: log 100%.
- ALLOW: log 1% (mặc định), tùy chỉnh per resource_type.

**Tầng 2 — Partitioning:**
- Bảng `authz_decision_log` partition theo tháng.
- Index chỉ cần trên hot partitions (30 ngày gần nhất).
- Old partitions: query nhanh vì index nhỏ hơn.

**Tầng 3 — Archival:**
- Cronjob đầu mỗi tháng: export partition cũ → Parquet → S3/GCS.
- Verify row count trước khi DROP partition khỏi PostgreSQL.
- Query cold data qua Athena/ClickHouse.

**Monitoring:** Alert khi partition size > 10GB (review sampling rate).

---

### EC-12: User vừa đổi Chi nhánh

**Tình huống:** Nhân viên HN được chuyển sang HCM. Sau 1 phút vẫn thấy document HN.

**Root cause:** Cache `authz:ctx:{userId}:{oldVersion}` vẫn sống.

**Chuỗi sự kiện sau khi đổi branch:**

```
T+0s:  Admin đổi branch trong Keycloak
T+0s:  Keycloak SPI phát event → Kafka iam.user.attribute.changed
T+2s:  IAM consumer nhận → UPDATE user_account SET attributes_version = 5
T+2s:  DELETE authz:ctx:{userId}:4 (invalidate cache cũ)
T+2s:  CDC → DashSet không cần update (không phải emergency revoke)
T+5s:  Nhân viên send request tiếp theo
T+5s:  JWT extract attr_version = 4 (JWT cũ)
T+5s:  Cache lookup authz:ctx:{userId}:4 → MISS (đã xóa)
T+5s:  Reload từ DB: attributes_version = 5, branch_code = HCM
T+5s:  Nhân viên thấy data HCM, không thấy HN nữa
```

**Nếu JWT chứa attr_version cũ (4) nhưng DB có version 5:**
- JWT claim `attr_version = 4` → cache miss.
- Reload từ DB → trả về version 5 (HCM).
- Từ request này trở đi, user thấy đúng data HCM.

> **Câu hỏi thường gặp:** "JWT có hạn 30 phút thì sao?" — JWT chỉ dùng để extract `attr_version`. Version mismatch → cache miss → DB reload với data mới nhất. JWT không chứa attribute values, chỉ chứa version number.

---

### EC-13: Permission kế thừa từ Role cha bị xung đột

**Tình huống:** Role `ANALYST` (parent) có ALLOW read, Role `SENIOR_ANALYST` (child) có DENY read cùng resource_type.

**Behavior với `DENY_OVERRIDES`:**
- Engine traverse toàn bộ role hierarchy.
- Thu thập tất cả policy matches.
- `DENY_OVERRIDES` → bất kỳ DENY nào → kết quả DENY.
- SENIOR_ANALYST không thể read (dù parent ANALYST có ALLOW).

**Behavior với `FIRST_MATCH_WINS`:**
- Sort theo priority DESC.
- SENIOR_ANALYST (priority cao hơn) → DENY win.

**Best practice:**
- Không tạo DENY ở child role nếu parent có ALLOW cùng action — gây confusion.
- Dùng `DENY_OVERRIDES` strategy → explicit DENY ở bất kỳ level nào đều có hiệu lực.
- CI/CD warning khi phát hiện parent có ALLOW, child có DENY cùng resource+action.

---

### EC-14: Tenant Suspended — toàn bộ user bị DENY

**Tình huống:** Tenant bị suspend (nợ phí, vi phạm chính sách).

**Cơ chế:**
```sql
UPDATE tenant SET status = 'SUSPENDED' WHERE id = :tenantId;
```

**Hành vi:**
- Engine kiểm tra `tenant.status` tại entry point của mỗi request.
- `SUSPENDED` → DENY tất cả request với lý do `TENANT_SUSPENDED`.
- Trigger `fn_check_tenant_active` ngăn tạo entity mới (role, policy, user_role).
- Không cần invalidate cache — check status là synchronous, từ DB.

**Phục hồi:** `UPDATE tenant SET status = 'ACTIVE'` → có hiệu lực ngay với request tiếp theo.

---

### EC-15: Sidecar mất kết nối với Control Plane

**Tình huống:** Network partition giữa sidecar và IAM Service. Sidecar đang có policy bundle cũ.

**Hành vi:**
- Sidecar tiếp tục dùng local bundle (evaluation không cần network).
- Emergency revoke: vẫn hoạt động nếu Redis accessible (hoặc local in-memory copy).
- Policy updates: bị delay đến khi kết nối phục hồi.
- Khi reconnect: nhận diff hoặc full bundle → atomic swap.

**Fail mode:**
- `fail_mode = DENY` (banking): nếu bundle null và không reconnect được → DENY all.
- Nếu bundle non-null nhưng stale: tiếp tục serve với policy cũ + log `STALE_BUNDLE` warning.

**Alert:** `sidecar_bundle_age_seconds > 300` → PagerDuty alert.

---

## 8. Vận hành & Giám sát

### 8.1 Metrics quan trọng

**SLA targets:**

| Metric | Target | Alert threshold |
|--------|--------|----------------|
| `authz_p99_latency_ms` | < 5ms | > 10ms |
| `authz_p50_latency_ms` | < 1ms | > 3ms |
| `jit_fetch_success_rate` | > 99% | < 98% |
| `rebac_circuit_open_total` | 0 | > 0 |
| `bundle_stale_age_seconds` | < 60 | > 300 |
| `shadow_diverge_pct` | < 5% | > 5% |
| `emergency_revoke_active_count` | Monitor | Sudden spike |

**Prometheus metrics được expose:**

```
authz_decisions_total{decision="ALLOW|DENY", resource_type, action, tenant}
authz_decision_duration_seconds{layer="temporal|rbac|abac|rebac|filter", quantile}
authz_jit_fetch_duration_seconds{source, status="success|timeout|circuit_open"}
authz_rebac_lookup_total{method="materialized|live_traversal"}
authz_shadow_divergence_total{policy_version_id}
authz_bundle_version{tenant_id}
authz_emergency_revoke_active{tenant_id}
```

---

### 8.2 Runbook: Phản ứng sự cố

#### Sự cố 1: `authz_p99_latency > 10ms`

```bash
# 1. Kiểm tra layer nào chậm
SELECT layer, AVG(duration_us), MAX(duration_us), COUNT(*)
FROM authz_decision_log
WHERE decided_at > NOW() - INTERVAL '5 minutes'
GROUP BY layer ORDER BY AVG(duration_us) DESC;

# 2. Nếu RBAC chậm → kiểm tra index
EXPLAIN (ANALYZE, BUFFERS) 
SELECT ... FROM user_role JOIN role_hierarchy ...
WHERE user_id = 'problematic-user-id';

# 3. Nếu ReBAC chậm → circuit breaker đang open?
SELECT * FROM authz_decision_log 
WHERE eval_trace->>'rebac_result' = 'CIRCUIT_OPEN'
  AND decided_at > NOW() - INTERVAL '5 minutes';

# 4. Nếu JIT chậm → shift service có vấn đề?
GET /metrics → authz_jit_fetch_duration_seconds_p99
```

#### Sự cố 2: User bị DENY không đúng

```bash
# 1. Gọi Explain API
GET /authz/explain?userId={X}&resourceRef={Y}&action={Z}

# 2. Xem trace để xác định layer/node fail
# 3. Nếu temporal: kiểm tra timezone và giờ làm việc
# 4. Nếu ABAC: so sánh user.attribute vs resource.attribute trong trace
# 5. Nếu cần urgent fix: override tạm thời qua resource_acl
INSERT INTO resource_acl (resource_instance_id, tenant_id, subject_id, subject_type, actions)
VALUES (:resourceId, :tid, :userId, 'USER', '{read}');
```

#### Sự cố 3: CDC pipeline lag (relation_reachability stale)

```bash
# Kiểm tra lag
SELECT MAX(computed_at), NOW() - MAX(computed_at) AS lag
FROM relation_reachability WHERE tenant_id = :tid;

# Nếu lag > 30s → live traversal đang được dùng thay thế (OK nhưng chậm hơn)
# Kiểm tra Kafka consumer lag
kafka-consumer-groups.sh --describe --group authz-cdc-consumer

# Force rebuild nếu cần
authz-cli rebuild-reachability --tenant :tid --relation delegate_of
```

---

### 8.3 Bảo trì định kỳ

| Tần suất | Công việc |
|---------|----------|
| Hàng ngày | Kiểm tra dashboard metrics, alert threshold |
| Hàng tuần | Review shadow divergence reports, DENY spike analysis |
| Hàng tháng | Archive audit log partitions → S3, tạo partition tháng mới |
| Hàng quý | Review policy conflicts, dọn user_role expired > 90 ngày |
| Hàng năm | Kiểm tra relation_reachability path length distribution |

**Tạo partition tháng mới (chạy trước đầu tháng):**

```sql
-- Ví dụ: tạo partition tháng 7/2026
CREATE TABLE authz_decision_log_2026_07 PARTITION OF authz_decision_log
    FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');

CREATE TABLE policy_shadow_log_2026_07 PARTITION OF policy_shadow_log
    FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');

CREATE TABLE user_attribute_history_2026_07 PARTITION OF user_attribute_history
    FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');
```

---

## 9. Quy tắc & Giới hạn Hệ thống

| Giới hạn | Giá trị | Lý do |
|---------|---------|-------|
| ReBAC max depth | 10 | Đủ cho mọi tổ chức thực tế, tránh infinite loop |
| ReBAC circuit open threshold | 3 lần fail liên tiếp | Balance giữa availability và protection |
| JIT fetch timeout | 150ms | AuthZ SLA target p99 < 5ms; JIT là overhead thêm |
| JIT cache TTL | 30s | Đủ ngắn để reflect thực tế (shift status thay đổi) |
| ES terms query max | 65,536 IDs | Giới hạn Elasticsearch |
| Fan-out limit per relation | Cấu hình (default null) | Big Node prevention |
| Policy divergence threshold | 5% | Configurable per tenant |
| Bundle stale alert | 300s | Network issue detection |
| Max batch check size | 100 (configurable) | Prevent abuse |
| Emergency revoke list | Không giới hạn | Safety critical — no cap |

---

## 10. Câu hỏi Thường gặp (FAQ)

**Q: User bị DENY nhưng không biết tại sao, làm sao điều tra?**

A: Gọi `GET /authz/explain?userId={}&resourceRef={}&action={}`. Response trả về full trace từng layer và từng AST node với giá trị actual.

---

**Q: Cần cấp quyền khẩn cấp cho user X truy cập resource Y ngay lập tức?**

A: Dùng `resource_acl` — không cần qua policy/role:
```sql
INSERT INTO resource_acl (resource_instance_id, tenant_id, subject_id, subject_type, actions, expires_at)
VALUES (:resourceId, :tid, :userId, 'USER', '{read,write}', NOW() + INTERVAL '2 hours');
```
Có hiệu lực ngay với request tiếp theo. Tự hết hạn sau 2 giờ.

---

**Q: Thêm attribute mới cho user (ví dụ: clearance_level) thì cần làm gì?**

A: Không cần thay đổi schema.
1. Update `user_account.attributes = {..., "clearance_level": 3}` (bump `attributes_version`).
2. Update `schema_field_registry`: thêm `clearance_level` với `sql_name`, `data_type`.
3. Viết policy AST dùng `{"type": "user_attr", "key": "clearance_level"}`.
4. Deploy policy qua GitOps → shadow test → promote.

---

**Q: Có thể export danh sách tất cả user có permission X không?**

A: Có, nhưng cần chú ý đây là heavy query:
```sql
SELECT DISTINCT u.id, u.username
FROM user_account u
JOIN user_role ur ON ur.user_id = u.id AND (ur.expires_at IS NULL OR ur.expires_at > NOW())
JOIN (
    WITH RECURSIVE role_tree AS (
        SELECT id, parent_role_id FROM role WHERE id = ur.role_id
        UNION ALL SELECT r2.id, r2.parent_role_id FROM role r2 JOIN role_tree rt ON r2.id = rt.parent_role_id
    ) SELECT id FROM role_tree
) r_hier ON true
JOIN role_permission rp ON rp.role_id = r_hier.id
JOIN permission p ON p.id = rp.permission_id AND p.code = 'READ_DOCUMENT_BRANCH'
WHERE u.tenant_id = :tid AND u.is_active = TRUE;
```

---

**Q: Policy mới deploy xong bao lâu có hiệu lực?**

A: Sau khi promote ACTIVE:
- Control Plane publish event → Kafka.
- Sidecar consumer nhận và swap bundle.
- Latency: < 5 giây trong điều kiện network bình thường.
- Kiểm tra: `GET /admin/bundle-version` trên từng sidecar.

---

**Q: Làm thế nào để test policy mà không ảnh hưởng production?**

A: Dùng Shadow Mode:
1. Upload version mới → `SHADOW`.
2. Chạy 7 ngày — hệ thống tự so sánh kết quả.
3. Xem divergence report.
4. Promote nếu OK.

Hoặc dùng Replay API với request từ production:
```bash
POST /authz/replay {"decision_id": "uuid", "use_policy_version": "new-version-id"}
```

---

**Q: Nếu shadow policy có `new_denials > 0` thì có promote được không?**

A: Phụ thuộc business risk:
- New denials = users hiện đang có quyền sẽ mất quyền sau promote.
- Cần review từng case: có intentional (fix security gap) hay accidental (bug trong policy)?
- Nếu intentional → notify users trước → promote.
- Nếu accidental → fix policy → shadow lại.
- Không bao giờ force promote khi có new_denials mà chưa phân tích.

---

> **Tài liệu liên quan:**  
> - [db/README.md](../db/README.md) — Flyway DDL & Database Setup  
> - [AuthZ-Platform-Dynamic-5-Layer-Design.md](../AuthZ-Platform-Dynamic-5-Layer-Design.md) — Architecture Reference  
> - `/authz/explain` — Explain API  
> - `/authz/replay` — Replay API  
> - `/admin/bundle-version` — Bundle Health Check
