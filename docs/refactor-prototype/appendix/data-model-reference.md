# Data model và ERD

**Proposed logical model; không chạy như migration.** Tất cả bảng bên dưới là target names, kể cả tên giống bảng hiện có. Mỗi schema DBML biểu diễn database/credential/migration owner riêng. Không tạo một PostgreSQL database chung cho tất cả context.

## Quy ước dữ liệu

**Bắt buộc:** mọi business entity dùng [AuditEntity + SoftDeletable + VersionedEntity](entity-conventions.md). DBML đã bổ sung created_by/updated_by, is_deleted, deleted_at/deleted_by/delete_reason và row_version. Các dictionary/ERD bên dưới lược common fields; row_version là concurrency counter, không model/release version. Immutable evidence/technical records có storage mapping riêng được quy định trong tài liệu entity, không cho mutable soft delete làm hỏng replay/dedup.

Access schema dưới đây là vocabulary logic, chưa đủ semantics để tự triển khai policy. Áp dụng quy tắc kết hợp, tenant guard, typed scope, deny precedence, row/field enforcement và revocation trong [đặc tả phân quyền và truy cập dữ liệu](../authorization-and-data-access.md). Organization/branch dùng tenant-local parent-child tree; branch scope kế thừa xuống descendants, không lên parent/sibling. Queue là trục độc lập, kết hợp AND với branch khi action yêu cầu cả hai. Không thêm JSON grants/rules song song trên role. Vertical slice có additive migration `db/projection-service/migrations/008_authorization_branch_ancestry.sql`: projection lưu immutable `resource_branch_id`, ancestry/revision phục vụ audit và nguồn gốc sự kiện. Query branch authorization resolve descendants hiện hành từ Organization owner và lọc theo `resource_branch_id` trong tập branch hiện hành, không dùng stored ancestry làm quyền hiện tại sau reparent. Legacy row thiếu `resource_branch_id` hoặc revision không match branch grant.


- Mỗi bảng có `tenant_id uuid`, `id uuid`, `created_at timestamptz`, `updated_at timestamptz`, `row_version bigint`; PK `(tenant_id, id)`. FK nội owner phải ghép tenant. External refs chỉ là logical links, không FK hoặc cross-database JOIN.
- `?` là nullable; còn lại NOT NULL. UUID là surrogate design key, không thay engine string reference. Key nghiệp vụ unique theo tenant và scope; thời gian UTC, render locale ở UI; amount dùng decimal + currency, không chuỗi VND hoặc floating point.
- Published versions immutable. Draft có revision CAS; process graph/XML là canonical editor document, normalized elements/flows phục vụ validation/query và được ghi cùng publication transaction. Không cho sửa hai representation độc lập.
- Snapshot/diff DMN dùng stable rule/clause IDs và immutable version. Clause/rule/cell phải cùng decision version; flow source/target phải cùng process version. Constraint bằng composite scope key hoặc deferred constraint trigger trong physical migration.
- Reference array trong candidate configuration là immutable authoring config; runtime membership dùng bảng liên kết. Không dùng JSON thay role/permission relation. JSONB dành cho expression, schemas, typed variables, graph và event payload có schema version/size cap.
- Credentials chỉ lưu secret reference; PII có classification/encryption và field-level masking. Delete theo retention/hold; audit và published artifacts không cascade-delete tùy tiện.

## Mapping schema hiện có

| Target context | Evidence hiện tại | Quy tắc chuyển đổi |
|---|---|---|
| Human | `db/human-runtime/migrations/001_human_runtime.sql`: work_items, assignment_policies, human_cases, human_case_plan_items, human_audit_log, human_event_inbox, escalation_outbox | Extend bằng additive migrations; không tạo work_items thứ hai. Human case-plan là runtime context, không đồng nhất customer Case. |
| Projection | `db/projection-service/migrations/`: workflow_event_read_models, projection_saved_queries, projection_query_exports, `007_authorization_resource_branch.sql`, `008_authorization_branch_ancestry.sql` | Extend/rebuild authorized read models; map saved_queries/export_jobs sang bảng hiện hữu. |
| Configuration | `db/configuration-service/migrations/`: profiles, versions, active scopes, audit/outbox/idempotency | Giữ service và lifecycle hiện có; endpoint secrets, Case hay user không chuyển vào config profile. |
| Governance | `db/governance-service/migrations/`: approval requests, signed approvals, audit | Tái sử dụng approval/evidence; không nhân bản trong Case. |
| Access | `apps/rust/authz-control-plane/db/` và owning Rust migrations | Schema ở đây là logical vocabulary; phải map typed policy, subject, tenant lifecycle vào owner hiện có, không tạo parallel Authz DB. External IdP vẫn sở hữu credential/MFA. |
| Authoring, customer Case, Document, Integration | Chưa có deployable owners tương ứng trong architecture inventory | New bounded contexts theo ADR-011; migrations chỉ thêm khi triển khai vertical slice. |
| MDM | Context planned trong ADR-011, chưa có deployable | Schema động có kiểm soát, immutable release/bundle, read replicas scoped; chi tiết [MDM](mdm-service.md). |
| Notification | Context planned trong ADR-011, ranh giới chốt trong ADR-013, chưa có deployable | Template version/release bất biến, delivery partition theo thời gian, hàng đợi River trong cùng database; migrations thêm cùng vertical slice đầu tiên; chi tiết [Notification](notification-service.md). |
| Engine | RocksDB/Raft, không phải SQL | Runtime event/state/snapshot/token/idempotency không có authoritative SQL tables; instance_views và work_items là projections. |

## Bảng và quan hệ theo owner

Các cột chung được lược khỏi dictionary để dễ đọc; có đầy đủ trong DBML. ERD liệt kê mọi bảng, cạnh chỉ FK nội owner. Bảng đứng riêng liên hệ qua external references/event, không phải thiếu FK cần bổ sung.

### access

| Bảng | Cột riêng (`?`: nullable) |
|---|---|
| organizations | `code: text`; `name: text`; `parent_id: @organizations?`; `status: text` |
| subjects | `external_subject: text`; `kind: text`; `display_name: text`; `email: text?`; `status: text`; `organization_id: @organizations?` |
| memberships | `subject_id: @subjects`; `organization_id: @organizations`; `status: text` |
| roles | `code: text`; `name: text`; `role_type: text`; `status: text` |
| permissions | `code: text`; `resource_type: text`; `action: text`; `mandatory_audit: boolean` |
| role_permissions | `role_id: @roles`; `permission_id: @permissions` |
| role_bindings | `role_id: @roles`; `subject_id: @subjects`; `scope_type: text`; `scope_id: text`; `expires_at: timestamptz?` |
| direct_grants | `subject_id: @subjects`; `permission_id: @permissions`; `scope_type: text`; `scope_id: text`; `effect: text`; `expires_at: timestamptz?` |
| policy_versions | `policy_key: text`; `version: bigint`; `rules: jsonb`; `digest: text`; `status: text`; `published_at: timestamptz?` |
| policy_bindings | `policy_version_id: @policy_versions`; `resource_type: text`; `resource_id: text` |
| workload_principals | `external_subject: text`; `name: text`; `status: text`; `credential_ref: text` |
| certificate_rotations | `principal_id: @workload_principals`; `external_job_id: text?`; `fingerprint: text?`; `not_after: timestamptz?`; `status: text` |
| outbox | `event_id: uuid`; `aggregate_id: uuid`; `aggregate_version: bigint`; `event_type: text`; `payload: jsonb`; `published_at: timestamptz?`; `attempts: int` |
| inbox | `source: text`; `event_id: text`; `payload_digest: text`; `applied_at: timestamptz` |
| command_receipts | `command_id: uuid`; `intent_digest: text`; `actor_ref: uuid`; `aggregate_id: uuid`; `status: text`; `result: jsonb`; `expires_at: timestamptz?` |
| audit_evidence | `actor_ref: uuid`; `action: text`; `resource_ref: text`; `command_id: uuid?`; `before_digest: text?`; `after_digest: text?`; `occurred_at: timestamptz` |

```mermaid
erDiagram
  organizations {
    uuid tenant_id PK
    uuid id PK
  }
  subjects {
    uuid tenant_id PK
    uuid id PK
  }
  memberships {
    uuid tenant_id PK
    uuid id PK
  }
  roles {
    uuid tenant_id PK
    uuid id PK
  }
  permissions {
    uuid tenant_id PK
    uuid id PK
  }
  role_permissions {
    uuid tenant_id PK
    uuid id PK
  }
  role_bindings {
    uuid tenant_id PK
    uuid id PK
  }
  direct_grants {
    uuid tenant_id PK
    uuid id PK
  }
  policy_versions {
    uuid tenant_id PK
    uuid id PK
  }
  policy_bindings {
    uuid tenant_id PK
    uuid id PK
  }
  workload_principals {
    uuid tenant_id PK
    uuid id PK
  }
  certificate_rotations {
    uuid tenant_id PK
    uuid id PK
  }
  outbox {
    uuid tenant_id PK
    uuid id PK
  }
  inbox {
    uuid tenant_id PK
    uuid id PK
  }
  command_receipts {
    uuid tenant_id PK
    uuid id PK
  }
  audit_evidence {
    uuid tenant_id PK
    uuid id PK
  }
  organizations |o--o{ organizations : "parent_id"
  organizations |o--o{ subjects : "organization_id"
  subjects ||--o{ memberships : "subject_id"
  organizations ||--o{ memberships : "organization_id"
  roles ||--o{ role_permissions : "role_id"
  permissions ||--o{ role_permissions : "permission_id"
  roles ||--o{ role_bindings : "role_id"
  subjects ||--o{ role_bindings : "subject_id"
  subjects ||--o{ direct_grants : "subject_id"
  permissions ||--o{ direct_grants : "permission_id"
  policy_versions ||--o{ policy_bindings : "policy_version_id"
  workload_principals ||--o{ certificate_rotations : "principal_id"
```

### authoring

| Bảng | Cột riêng (`?`: nullable) |
|---|---|
| process_models | `model_key: text`; `name: text`; `category: text`; `description: text?`; `status: text` |
| process_drafts | `model_id: @process_models`; `base_version_id: @process_versions?`; `revision: bigint`; `graph: jsonb`; `xml: text?`; `validation: jsonb` |
| process_versions | `model_id: @process_models`; `version: bigint`; `source_uri: text`; `digest: text`; `engine_profile: text`; `compiler_version: text` |
| process_elements | `process_version_id: @process_versions`; `element_key: text`; `element_type: text`; `name: text`; `properties: jsonb` |
| process_flows | `process_version_id: @process_versions`; `source_id: @process_elements`; `target_id: @process_elements`; `condition: text?`; `waypoints: jsonb` |
| process_pools | `process_version_id: @process_versions`; `pool_key: text`; `participant_ref: text`; `geometry: jsonb` |
| process_lanes | `pool_id: @process_pools`; `lane_key: text`; `actor_role_ref: text`; `geometry: jsonb` |
| node_assignments | `element_id: @process_elements`; `lane_id: @process_lanes?`; `candidate_subjects: jsonb`; `candidate_roles: jsonb`; `sla_policy_ref: text?`; `form_version_id: @form_versions?` |
| decision_models | `decision_key: text`; `name: text`; `description: text?` |
| decision_drafts | `decision_id: @decision_models`; `base_version_id: @decision_versions?`; `revision: bigint`; `definition: jsonb` |
| decision_versions | `decision_id: @decision_models`; `version: bigint`; `version_tag: text`; `summary: text`; `hit_policy: text`; `digest: text`; `source_uri: text` |
| decision_clauses | `decision_version_id: @decision_versions`; `clause_key: text`; `direction: text`; `ordinal: int`; `label: text`; `expression: text`; `type_ref: text`; `allowed_values: jsonb` |
| decision_rules | `decision_version_id: @decision_versions`; `rule_key: text`; `ordinal: int`; `active: boolean`; `annotation: text?` |
| decision_cells | `rule_id: @decision_rules`; `clause_id: @decision_clauses`; `expression: text`; `value: jsonb?` |
| decision_evaluations | `decision_version_id: @decision_versions`; `input_digest: text`; `result: jsonb`; `status: text`; `duration_ms: bigint`; `expires_at: timestamptz` |
| form_definitions | `form_key: text`; `name: text` |
| form_versions | `form_id: @form_definitions`; `version: bigint`; `schema: jsonb`; `ui_schema: jsonb`; `digest: text` |
| workflow_templates | `template_key: text`; `name: text`; `domain: text` |
| workflow_template_versions | `template_id: @workflow_templates`; `version: bigint`; `graph: jsonb`; `parameter_schema: jsonb`; `digest: text` |
| model_dependencies | `process_version_id: @process_versions`; `decision_version_id: @decision_versions?`; `form_version_id: @form_versions?`; `called_process_version_id: @process_versions?`; `binding_type: text` |
| deployments | `process_version_id: @process_versions`; `environment_ref: text`; `engine_profile: text`; `artifact_digest: text?`; `status: text`; `command_id: uuid`; `receipt_ref: text?`; `error_code: text?` |
| outbox | `event_id: uuid`; `aggregate_id: uuid`; `aggregate_version: bigint`; `event_type: text`; `payload: jsonb`; `published_at: timestamptz?`; `attempts: int` |
| inbox | `source: text`; `event_id: text`; `payload_digest: text`; `applied_at: timestamptz` |
| command_receipts | `command_id: uuid`; `intent_digest: text`; `actor_ref: uuid`; `aggregate_id: uuid`; `status: text`; `result: jsonb`; `expires_at: timestamptz?` |
| audit_evidence | `actor_ref: uuid`; `action: text`; `resource_ref: text`; `command_id: uuid?`; `before_digest: text?`; `after_digest: text?`; `occurred_at: timestamptz` |

```mermaid
erDiagram
  process_models {
    uuid tenant_id PK
    uuid id PK
  }
  process_drafts {
    uuid tenant_id PK
    uuid id PK
  }
  process_versions {
    uuid tenant_id PK
    uuid id PK
  }
  process_elements {
    uuid tenant_id PK
    uuid id PK
  }
  process_flows {
    uuid tenant_id PK
    uuid id PK
  }
  process_pools {
    uuid tenant_id PK
    uuid id PK
  }
  process_lanes {
    uuid tenant_id PK
    uuid id PK
  }
  node_assignments {
    uuid tenant_id PK
    uuid id PK
  }
  decision_models {
    uuid tenant_id PK
    uuid id PK
  }
  decision_drafts {
    uuid tenant_id PK
    uuid id PK
  }
  decision_versions {
    uuid tenant_id PK
    uuid id PK
  }
  decision_clauses {
    uuid tenant_id PK
    uuid id PK
  }
  decision_rules {
    uuid tenant_id PK
    uuid id PK
  }
  decision_cells {
    uuid tenant_id PK
    uuid id PK
  }
  decision_evaluations {
    uuid tenant_id PK
    uuid id PK
  }
  form_definitions {
    uuid tenant_id PK
    uuid id PK
  }
  form_versions {
    uuid tenant_id PK
    uuid id PK
  }
  workflow_templates {
    uuid tenant_id PK
    uuid id PK
  }
  workflow_template_versions {
    uuid tenant_id PK
    uuid id PK
  }
  model_dependencies {
    uuid tenant_id PK
    uuid id PK
  }
  deployments {
    uuid tenant_id PK
    uuid id PK
  }
  outbox {
    uuid tenant_id PK
    uuid id PK
  }
  inbox {
    uuid tenant_id PK
    uuid id PK
  }
  command_receipts {
    uuid tenant_id PK
    uuid id PK
  }
  audit_evidence {
    uuid tenant_id PK
    uuid id PK
  }
  process_models ||--o{ process_drafts : "model_id"
  process_versions |o--o{ process_drafts : "base_version_id"
  process_models ||--o{ process_versions : "model_id"
  process_versions ||--o{ process_elements : "process_version_id"
  process_versions ||--o{ process_flows : "process_version_id"
  process_elements ||--o{ process_flows : "source_id"
  process_elements ||--o{ process_flows : "target_id"
  process_versions ||--o{ process_pools : "process_version_id"
  process_pools ||--o{ process_lanes : "pool_id"
  process_elements ||--o{ node_assignments : "element_id"
  process_lanes |o--o{ node_assignments : "lane_id"
  form_versions |o--o{ node_assignments : "form_version_id"
  decision_models ||--o{ decision_drafts : "decision_id"
  decision_versions |o--o{ decision_drafts : "base_version_id"
  decision_models ||--o{ decision_versions : "decision_id"
  decision_versions ||--o{ decision_clauses : "decision_version_id"
  decision_versions ||--o{ decision_rules : "decision_version_id"
  decision_rules ||--o{ decision_cells : "rule_id"
  decision_clauses ||--o{ decision_cells : "clause_id"
  decision_versions ||--o{ decision_evaluations : "decision_version_id"
  form_definitions ||--o{ form_versions : "form_id"
  workflow_templates ||--o{ workflow_template_versions : "template_id"
  process_versions ||--o{ model_dependencies : "process_version_id"
  decision_versions |o--o{ model_dependencies : "decision_version_id"
  form_versions |o--o{ model_dependencies : "form_version_id"
  process_versions |o--o{ model_dependencies : "called_process_version_id"
  process_versions ||--o{ deployments : "process_version_id"
```

### human

| Bảng | Cột riêng (`?`: nullable) |
|---|---|
| queues | `queue_key: text`; `name: text`; `process_key: text`; `status: text` |
| queue_memberships | `queue_id: @queues`; `subject_ref: uuid?`; `role_ref: uuid?`; `scope: jsonb` |
| assignment_policy_versions | `queue_id: @queues`; `version: bigint`; `strategy: text`; `max_active: int`; `escalation_seconds: bigint`; `target_group_ref: text`; `active: boolean` |
| work_items | `instance_ref: text`; `element_key: text`; `queue_id: @queues?`; `assignee_ref: uuid?`; `status: text`; `priority: text`; `due_at: timestamptz?`; `engine_version: bigint`; `form_version_ref: uuid?` |
| work_item_candidates | `work_item_id: @work_items`; `subject_ref: uuid?`; `role_ref: uuid?` |
| assignment_history | `work_item_id: @work_items`; `from_subject_ref: uuid?`; `to_subject_ref: uuid?`; `command_id: uuid`; `reason: text?`; `effective_at: timestamptz` |
| delegations | `work_item_id: @work_items`; `from_subject_ref: uuid`; `to_subject_ref: uuid`; `depth: int`; `expires_at: timestamptz?`; `command_id: uuid` |
| sla_policies | `policy_key: text`; `version: bigint`; `calendar_ref: text?`; `warning_seconds: bigint`; `deadline_seconds: bigint`; `escalation_action: jsonb` |
| sla_timers | `work_item_id: @work_items`; `policy_id: @sla_policies`; `due_at: timestamptz`; `status: text`; `attempt: int`; `lease_until: timestamptz?` |
| outbox | `event_id: uuid`; `aggregate_id: uuid`; `aggregate_version: bigint`; `event_type: text`; `payload: jsonb`; `published_at: timestamptz?`; `attempts: int` |
| inbox | `source: text`; `event_id: text`; `payload_digest: text`; `applied_at: timestamptz` |
| command_receipts | `command_id: uuid`; `intent_digest: text`; `actor_ref: uuid`; `aggregate_id: uuid`; `status: text`; `result: jsonb`; `expires_at: timestamptz?` |
| audit_evidence | `actor_ref: uuid`; `action: text`; `resource_ref: text`; `command_id: uuid?`; `before_digest: text?`; `after_digest: text?`; `occurred_at: timestamptz` |

```mermaid
erDiagram
  queues {
    uuid tenant_id PK
    uuid id PK
  }
  queue_memberships {
    uuid tenant_id PK
    uuid id PK
  }
  assignment_policy_versions {
    uuid tenant_id PK
    uuid id PK
  }
  work_items {
    uuid tenant_id PK
    uuid id PK
  }
  work_item_candidates {
    uuid tenant_id PK
    uuid id PK
  }
  assignment_history {
    uuid tenant_id PK
    uuid id PK
  }
  delegations {
    uuid tenant_id PK
    uuid id PK
  }
  sla_policies {
    uuid tenant_id PK
    uuid id PK
  }
  sla_timers {
    uuid tenant_id PK
    uuid id PK
  }
  outbox {
    uuid tenant_id PK
    uuid id PK
  }
  inbox {
    uuid tenant_id PK
    uuid id PK
  }
  command_receipts {
    uuid tenant_id PK
    uuid id PK
  }
  audit_evidence {
    uuid tenant_id PK
    uuid id PK
  }
  queues ||--o{ queue_memberships : "queue_id"
  queues ||--o{ assignment_policy_versions : "queue_id"
  queues |o--o{ work_items : "queue_id"
  work_items ||--o{ work_item_candidates : "work_item_id"
  work_items ||--o{ assignment_history : "work_item_id"
  work_items ||--o{ delegations : "work_item_id"
  work_items ||--o{ sla_timers : "work_item_id"
  sla_policies ||--o{ sla_timers : "policy_id"
```

### case

| Bảng | Cột riêng (`?`: nullable) |
|---|---|
| service_offerings | `service_key: text`; `name: text`; `category: text`; `description: text`; `status: text` |
| service_offering_versions | `offering_id: @service_offerings`; `version: bigint`; `process_version_ref: uuid`; `form_version_ref: uuid`; `document_requirements: jsonb`; `sla_policy_ref: uuid?`; `presentation: jsonb` |
| customers | `external_customer_ref: text?`; `subject_ref: uuid`; `name: text`; `organization_name: text?`; `tax_id_ciphertext: text?`; `phone_ciphertext: text?`; `email_ciphertext: text?` |
| cases | `tracking_code: text`; `offering_version_id: @service_offering_versions`; `customer_id: @customers`; `business_key: text`; `branch_ref: text?`; `status: text`; `orchestration_state: text`; `assigned_officer_ref: uuid?`; `amount: decimal?`; `currency: text?`; `term_months: int?`; `purpose: text?`; `deadline_at: timestamptz?` |
| case_variables | `case_id: @cases`; `name: text`; `type_ref: text`; `value: jsonb`; `classification: text` |
| case_workflow_links | `case_id: @cases`; `instance_ref: text`; `process_version_ref: uuid`; `start_command_id: uuid`; `engine_receipt_ref: text?`; `status: text` |
| case_steps | `case_id: @cases`; `element_key: text`; `execution_ref: text?`; `name: text`; `status: text`; `handler_ref: uuid?`; `started_at: timestamptz?`; `completed_at: timestamptz?` |
| case_comments | `case_id: @cases`; `author_ref: uuid`; `visibility: text`; `body: text`; `edited_at: timestamptz?` |
| case_document_links | `case_id: @cases`; `document_ref: uuid`; `document_version_ref: uuid`; `category: text`; `requirement_key: text?`; `status_projection: text` |
| case_decisions | `case_id: @cases`; `task_ref: text`; `command_id: uuid`; `outcome: text`; `reason: text`; `engine_receipt_ref: text?`; `status: text` |
| workflow_start_jobs | `case_id: @cases`; `command_id: uuid`; `payload_digest: text`; `attempts: int`; `next_attempt_at: timestamptz?`; `lease_until: timestamptz?`; `status: text`; `last_error: text?` |
| outbox | `event_id: uuid`; `aggregate_id: uuid`; `aggregate_version: bigint`; `event_type: text`; `payload: jsonb`; `published_at: timestamptz?`; `attempts: int` |
| inbox | `source: text`; `event_id: text`; `payload_digest: text`; `applied_at: timestamptz` |
| command_receipts | `command_id: uuid`; `intent_digest: text`; `actor_ref: uuid`; `aggregate_id: uuid`; `status: text`; `result: jsonb`; `expires_at: timestamptz?` |
| audit_evidence | `actor_ref: uuid`; `action: text`; `resource_ref: text`; `command_id: uuid?`; `before_digest: text?`; `after_digest: text?`; `occurred_at: timestamptz` |

```mermaid
erDiagram
  service_offerings {
    uuid tenant_id PK
    uuid id PK
  }
  service_offering_versions {
    uuid tenant_id PK
    uuid id PK
  }
  customers {
    uuid tenant_id PK
    uuid id PK
  }
  cases {
    uuid tenant_id PK
    uuid id PK
  }
  case_variables {
    uuid tenant_id PK
    uuid id PK
  }
  case_workflow_links {
    uuid tenant_id PK
    uuid id PK
  }
  case_steps {
    uuid tenant_id PK
    uuid id PK
  }
  case_comments {
    uuid tenant_id PK
    uuid id PK
  }
  case_document_links {
    uuid tenant_id PK
    uuid id PK
  }
  case_decisions {
    uuid tenant_id PK
    uuid id PK
  }
  workflow_start_jobs {
    uuid tenant_id PK
    uuid id PK
  }
  outbox {
    uuid tenant_id PK
    uuid id PK
  }
  inbox {
    uuid tenant_id PK
    uuid id PK
  }
  command_receipts {
    uuid tenant_id PK
    uuid id PK
  }
  audit_evidence {
    uuid tenant_id PK
    uuid id PK
  }
  service_offerings ||--o{ service_offering_versions : "offering_id"
  service_offering_versions ||--o{ cases : "offering_version_id"
  customers ||--o{ cases : "customer_id"
  cases ||--o{ case_variables : "case_id"
  cases ||--o{ case_workflow_links : "case_id"
  cases ||--o{ case_steps : "case_id"
  cases ||--o{ case_comments : "case_id"
  cases ||--o{ case_document_links : "case_id"
  cases ||--o{ case_decisions : "case_id"
  cases ||--o{ workflow_start_jobs : "case_id"
```

### document

| Bảng | Cột riêng (`?`: nullable) |
|---|---|
| documents | `document_key: text`; `owner_kind: text`; `owner_ref: text`; `classification: text`; `retention_policy_ref: text?`; `status: text` |
| document_versions | `document_id: @documents`; `version: bigint`; `object_key: text`; `checksum: text`; `media_type: text`; `byte_size: bigint`; `original_name: text`; `scan_status: text`; `verified_by: uuid?`; `verified_at: timestamptz?` |
| upload_sessions | `document_id: @documents`; `expected_size: bigint`; `expected_checksum: text?`; `expires_at: timestamptz`; `status: text`; `object_key: text` |
| scan_jobs | `document_version_id: @document_versions`; `scanner_profile: text`; `verdict: text?`; `attempts: int`; `status: text`; `lease_until: timestamptz?` |
| document_access_grants | `document_id: @documents`; `subject_ref: uuid`; `action: text`; `expires_at: timestamptz?` |
| retention_holds | `document_id: @documents`; `reason: text`; `placed_by: uuid`; `released_at: timestamptz?` |
| outbox | `event_id: uuid`; `aggregate_id: uuid`; `aggregate_version: bigint`; `event_type: text`; `payload: jsonb`; `published_at: timestamptz?`; `attempts: int` |
| inbox | `source: text`; `event_id: text`; `payload_digest: text`; `applied_at: timestamptz` |
| command_receipts | `command_id: uuid`; `intent_digest: text`; `actor_ref: uuid`; `aggregate_id: uuid`; `status: text`; `result: jsonb`; `expires_at: timestamptz?` |
| audit_evidence | `actor_ref: uuid`; `action: text`; `resource_ref: text`; `command_id: uuid?`; `before_digest: text?`; `after_digest: text?`; `occurred_at: timestamptz` |

```mermaid
erDiagram
  documents {
    uuid tenant_id PK
    uuid id PK
  }
  document_versions {
    uuid tenant_id PK
    uuid id PK
  }
  upload_sessions {
    uuid tenant_id PK
    uuid id PK
  }
  scan_jobs {
    uuid tenant_id PK
    uuid id PK
  }
  document_access_grants {
    uuid tenant_id PK
    uuid id PK
  }
  retention_holds {
    uuid tenant_id PK
    uuid id PK
  }
  outbox {
    uuid tenant_id PK
    uuid id PK
  }
  inbox {
    uuid tenant_id PK
    uuid id PK
  }
  command_receipts {
    uuid tenant_id PK
    uuid id PK
  }
  audit_evidence {
    uuid tenant_id PK
    uuid id PK
  }
  documents ||--o{ document_versions : "document_id"
  documents ||--o{ upload_sessions : "document_id"
  document_versions ||--o{ scan_jobs : "document_version_id"
  documents ||--o{ document_access_grants : "document_id"
  documents ||--o{ retention_holds : "document_id"
```

### integration

| Bảng | Cột riêng (`?`: nullable) |
|---|---|
| endpoints | `endpoint_key: text`; `name: text`; `service_type: text`; `status: text` |
| endpoint_versions | `endpoint_id: @endpoints`; `version: bigint`; `url: text`; `http_method: text`; `auth_type: text`; `secret_ref: text?`; `timeout_ms: int`; `retry_policy: jsonb`; `allowed_destinations: jsonb` |
| integration_bindings | `endpoint_version_id: @endpoint_versions`; `process_version_ref: uuid`; `element_key: text`; `mapping: jsonb` |
| probe_jobs | `endpoint_id: @endpoints`; `status: text`; `latency_ms: int?`; `observed_at: timestamptz?`; `error_code: text?` |
| delivery_attempts | `endpoint_version_id: @endpoint_versions`; `command_id: uuid`; `request_digest: text`; `attempt: int`; `status: text`; `next_attempt_at: timestamptz?`; `response_code: int?` |
| callback_receipts | `endpoint_id: @endpoints`; `external_event_id: text`; `signature_key_ref: text`; `payload_digest: text`; `received_at: timestamptz`; `status: text` |
| outbox | `event_id: uuid`; `aggregate_id: uuid`; `aggregate_version: bigint`; `event_type: text`; `payload: jsonb`; `published_at: timestamptz?`; `attempts: int` |
| inbox | `source: text`; `event_id: text`; `payload_digest: text`; `applied_at: timestamptz` |
| command_receipts | `command_id: uuid`; `intent_digest: text`; `actor_ref: uuid`; `aggregate_id: uuid`; `status: text`; `result: jsonb`; `expires_at: timestamptz?` |
| audit_evidence | `actor_ref: uuid`; `action: text`; `resource_ref: text`; `command_id: uuid?`; `before_digest: text?`; `after_digest: text?`; `occurred_at: timestamptz` |

```mermaid
erDiagram
  endpoints {
    uuid tenant_id PK
    uuid id PK
  }
  endpoint_versions {
    uuid tenant_id PK
    uuid id PK
  }
  integration_bindings {
    uuid tenant_id PK
    uuid id PK
  }
  probe_jobs {
    uuid tenant_id PK
    uuid id PK
  }
  delivery_attempts {
    uuid tenant_id PK
    uuid id PK
  }
  callback_receipts {
    uuid tenant_id PK
    uuid id PK
  }
  outbox {
    uuid tenant_id PK
    uuid id PK
  }
  inbox {
    uuid tenant_id PK
    uuid id PK
  }
  command_receipts {
    uuid tenant_id PK
    uuid id PK
  }
  audit_evidence {
    uuid tenant_id PK
    uuid id PK
  }
  endpoints ||--o{ endpoint_versions : "endpoint_id"
  endpoint_versions ||--o{ integration_bindings : "endpoint_version_id"
  endpoints ||--o{ probe_jobs : "endpoint_id"
  endpoint_versions ||--o{ delivery_attempts : "endpoint_version_id"
  endpoints ||--o{ callback_receipts : "endpoint_id"
```

### notification

| Bảng | Cột riêng (`?`: nullable) |
|---|---|
| categories | `category_key: text`; `name: text`; `default_lane: text`; `user_unsubscribable: boolean`; `ambiguous_outcome_policy: text`; `retention_class: text`; `retain_rendered_copy: boolean`; `status: text` |
| applications | `app_key: text`; `name: text`; `auth_mode: text`; `principal_ref: text`; `allowed_namespaces: jsonb`; `allowed_channels: jsonb`; `default_lane: text`; `quota_config: jsonb`; `callback_url: text?`; `callback_secret_ref: text?`; `owner_team: text?`; `default_sender_identity: jsonb?`; `status: text` |
| application_api_keys | `application_id: @applications`; `key_prefix: text`; `secret_hash: text`; `status: text`; `expires_at: timestamptz?`; `last_used_at: timestamptz?` |
| layouts | `layout_key: text`; `name: text`; `channel: text`; `status: text` |
| layout_versions | `layout_id: @layouts`; `version: bigint`; `syntax: text`; `variable_schema: jsonb`; `content: jsonb`; `content_hash: text`; `status: text`; `published_at: timestamptz?` |
| templates | `template_key: text`; `application_id: @applications`; `category_id: @categories`; `name: text`; `default_locale: text`; `provenance: jsonb?`; `tags: jsonb`; `status: text` |
| template_versions | `template_id: @templates`; `version: bigint`; `schema_major: int`; `syntax: text`; `layout_version_id: @layout_versions?`; `variable_schema: jsonb`; `content_hash: text`; `source_commit: text?`; `change_note: text?`; `approval_ref: text?`; `status: text`; `published_at: timestamptz?` |
| template_variants | `template_version_id: @template_versions`; `channel: text`; `locale: text`; `variant_kind: text`; `subject_tmpl: text?`; `html_tmpl: text?`; `text_tmpl: text?`; `mjml_source: text?`; `provider_template_id: text?`; `param_mapping: jsonb?`; `variant_hash: text` |
| template_releases | `template_id: @templates`; `schema_major: int`; `template_version_id: @template_versions`; `ordinal: bigint`; `previous_ordinal: bigint?`; `activate_at: timestamptz`; `activated_at: timestamptz?`; `approval_ref: text?`; `status: text` |
| publication_releases | `artifact_kind: text`; `artifact_id: uuid`; `target_version_id: uuid`; `ordinal: bigint`; `previous_ordinal: bigint?`; `activate_at: timestamptz`; `activated_at: timestamptz?`; `approval_ref: text?`; `status: text` |
| provider_profiles | `provider_key: text`; `name: text`; `channel: text`; `driver_kind: text`; `preset_key: text?`; `active_ordinal: bigint`; `operational_state: text` |
| provider_revisions | `provider_id: @provider_profiles`; `revision: bigint`; `config: jsonb`; `credentials_ref: text?`; `sender_identity: jsonb`; `capabilities: jsonb`; `quota_config: jsonb`; `config_hash: text`; `status: text`; `published_at: timestamptz?` |
| provider_health | `provider_id: @provider_profiles`; `breaker_state: text`; `error_rate_bps: int`; `window_requests: int`; `window_failures: int`; `last_transition_at: timestamptz`; `last_error_code: text?`; `updated_by_replica: text` |
| breaker_transitions | `provider_id: @provider_profiles`; `from_state: text`; `to_state: text`; `trigger_kind: text`; `reason: text`; `failure_rate_bps: int?`; `slow_call_rate_bps: int?`; `replica_ref: text`; `occurred_at: timestamptz` |
| breaker_overrides | `provider_id: @provider_profiles`; `override_mode: text`; `reason: text`; `ordinal: bigint`; `expires_at: timestamptz`; `revoked_at: timestamptz?`; `status: text` |
| routing_policies | `policy_key: text`; `application_id: @applications?`; `category_id: @categories?`; `channel: text`; `active_ordinal: bigint`; `status: text` |
| routing_policy_versions | `routing_policy_id: @routing_policies`; `version: bigint`; `routes: jsonb`; `failover_policy: jsonb`; `digest: text`; `status: text`; `published_at: timestamptz?` |
| trigger_rules | `rule_key: text`; `name: text`; `workflow_type: text`; `node_id: text?`; `trigger_kind: text`; `template_id: @templates`; `schema_major: int`; `active_ordinal: bigint`; `status: text` |
| trigger_rule_revisions | `rule_id: @trigger_rules`; `revision: bigint`; `version_selector: text`; `condition_cel: text?`; `recipient_spec: jsonb`; `data_mapping_cel: jsonb`; `referenced_vars: jsonb`; `delay_seconds: int`; `dedup_window_seconds: int`; `digest: text`; `status: text`; `published_at: timestamptz?` |
| workflow_instance_contexts | `instance_ref: text`; `workflow_type: text`; `initiator_subject_ref: uuid?`; `encrypted_vars: bytea`; `key_ref: text`; `workflow_completed_at: timestamptz?`; `expires_at: timestamptz` |
| subject_contacts | `subject_ref: uuid`; `channel: text`; `encrypted_address: bytea`; `address_hash: text`; `masked_address: text`; `locale: text?`; `timezone: text?`; `verified: boolean`; `source_version: bigint` |
| recipient_preferences | `subject_ref: uuid`; `category_id: @categories`; `channel: text`; `enabled: boolean`; `quiet_hours: jsonb?` |
| suppressions | `channel: text`; `address_hash: text`; `masked_address: text`; `reason: text`; `source_provider_id: @provider_profiles?`; `expires_at: timestamptz?`; `status: text` |
| notification_requests | `application_id: @applications`; `idempotency_key: text`; `request_digest: text`; `template_id: @templates`; `schema_major: int`; `pinned_version: bigint?`; `category_id: @categories`; `lane: text`; `priority: smallint`; `correlation_id: text?`; `trigger_rule_revision_id: @trigger_rule_revisions?`; `is_test: boolean`; `send_at: timestamptz?`; `expires_at: timestamptz?`; `status: text` |
| deliveries | `request_id: @notification_requests`; `template_version_id: @template_versions`; `variant_id: @template_variants`; `release_ordinal: bigint`; `recipient_subject_ref: uuid?`; `address_hash: text`; `masked_address: text`; `channel: text`; `lane: text`; `status: text`; `selected_provider_id: @provider_profiles?`; `provider_message_id: text?`; `content_hash: text`; `resend_of_delivery_id: @deliveries?`; `attempt_count: int`; `next_attempt_at: timestamptz?`; `expires_at: timestamptz?`; `completed_at: timestamptz?` |
| delivery_payloads | `delivery_id: @deliveries`; `encrypted_address: bytea?`; `encrypted_data: bytea?`; `encrypted_secrets: bytea?`; `rendered_copy_encrypted: bytea?`; `key_ref: text`; `secrets_erased_at: timestamptz?`; `expires_at: timestamptz` |
| delivery_attempts | `delivery_id: @deliveries`; `provider_id: @provider_profiles`; `provider_revision_id: @provider_revisions`; `attempt_no: int`; `outcome_class: text`; `provider_status_code: text?`; `provider_message_id: text?`; `error_code: text?`; `latency_ms: int`; `started_at: timestamptz`; `finished_at: timestamptz` |
| delivery_events | `delivery_id: @deliveries`; `event_type: text`; `from_status: text?`; `to_status: text`; `detail: jsonb?`; `occurred_at: timestamptz` |
| provider_callbacks | `provider_id: @provider_profiles`; `provider_event_id: text`; `delivery_id: @deliveries?`; `event_kind: text`; `provider_status: text`; `signature_valid: boolean`; `payload_digest: text`; `received_at: timestamptz`; `processed_at: timestamptz?` |
| inapp_messages | `delivery_id: @deliveries`; `subject_ref: uuid`; `category_key: text`; `title: text`; `body_html: text`; `action_url: text?`; `metadata: jsonb?`; `read_at: timestamptz?`; `archived_at: timestamptz?`; `expires_at: timestamptz` |
| delivery_metrics_hourly | `bucket_start: timestamptz`; `application_id: @applications`; `template_id: @templates`; `category_id: @categories`; `provider_id: @provider_profiles?`; `channel: text`; `lane: text`; `accepted_count: bigint`; `sent_count: bigint`; `delivered_count: bigint`; `bounced_count: bigint`; `failed_count: bigint`; `deferred_count: bigint`; `expired_count: bigint`; `suppressed_count: bigint`; `latency_histogram: jsonb?` |
| outbox | `event_id: uuid`; `aggregate_id: uuid`; `aggregate_version: bigint`; `event_type: text`; `payload: jsonb`; `published_at: timestamptz?`; `attempts: int` |
| inbox | `source: text`; `event_id: text`; `payload_digest: text`; `applied_at: timestamptz` |
| command_receipts | `command_id: uuid`; `intent_digest: text`; `actor_ref: uuid`; `aggregate_id: uuid`; `status: text`; `result: jsonb`; `expires_at: timestamptz?` |
| audit_evidence | `actor_ref: uuid`; `action: text`; `resource_ref: text`; `command_id: uuid?`; `before_digest: text?`; `after_digest: text?`; `resource_type: text`; `severity: text`; `actor_display: text?`; `actor_role: text?`; `client_ip: text?`; `details: jsonb?`; `occurred_at: timestamptz` |

```mermaid
erDiagram
  categories {
    uuid tenant_id PK
    uuid id PK
  }
  applications {
    uuid tenant_id PK
    uuid id PK
  }
  application_api_keys {
    uuid tenant_id PK
    uuid id PK
  }
  layouts {
    uuid tenant_id PK
    uuid id PK
  }
  layout_versions {
    uuid tenant_id PK
    uuid id PK
  }
  templates {
    uuid tenant_id PK
    uuid id PK
  }
  template_versions {
    uuid tenant_id PK
    uuid id PK
  }
  template_variants {
    uuid tenant_id PK
    uuid id PK
  }
  template_releases {
    uuid tenant_id PK
    uuid id PK
  }
  publication_releases {
    uuid tenant_id PK
    uuid id PK
  }
  provider_profiles {
    uuid tenant_id PK
    uuid id PK
  }
  provider_revisions {
    uuid tenant_id PK
    uuid id PK
  }
  provider_health {
    uuid tenant_id PK
    uuid id PK
  }
  breaker_transitions {
    uuid tenant_id PK
    uuid id PK
  }
  breaker_overrides {
    uuid tenant_id PK
    uuid id PK
  }
  routing_policies {
    uuid tenant_id PK
    uuid id PK
  }
  routing_policy_versions {
    uuid tenant_id PK
    uuid id PK
  }
  trigger_rules {
    uuid tenant_id PK
    uuid id PK
  }
  trigger_rule_revisions {
    uuid tenant_id PK
    uuid id PK
  }
  workflow_instance_contexts {
    uuid tenant_id PK
    uuid id PK
  }
  subject_contacts {
    uuid tenant_id PK
    uuid id PK
  }
  recipient_preferences {
    uuid tenant_id PK
    uuid id PK
  }
  suppressions {
    uuid tenant_id PK
    uuid id PK
  }
  notification_requests {
    uuid tenant_id PK
    uuid id PK
  }
  deliveries {
    uuid tenant_id PK
    uuid id PK
  }
  delivery_payloads {
    uuid tenant_id PK
    uuid id PK
  }
  delivery_attempts {
    uuid tenant_id PK
    uuid id PK
  }
  delivery_events {
    uuid tenant_id PK
    uuid id PK
  }
  provider_callbacks {
    uuid tenant_id PK
    uuid id PK
  }
  inapp_messages {
    uuid tenant_id PK
    uuid id PK
  }
  delivery_metrics_hourly {
    uuid tenant_id PK
    uuid id PK
  }
  outbox {
    uuid tenant_id PK
    uuid id PK
  }
  inbox {
    uuid tenant_id PK
    uuid id PK
  }
  command_receipts {
    uuid tenant_id PK
    uuid id PK
  }
  audit_evidence {
    uuid tenant_id PK
    uuid id PK
  }
  applications ||--o{ application_api_keys : "application_id"
  layouts ||--o{ layout_versions : "layout_id"
  applications ||--o{ templates : "application_id"
  categories ||--o{ templates : "category_id"
  templates ||--o{ template_versions : "template_id"
  layout_versions |o--o{ template_versions : "layout_version_id"
  template_versions ||--o{ template_variants : "template_version_id"
  templates ||--o{ template_releases : "template_id"
  template_versions ||--o{ template_releases : "template_version_id"
  provider_profiles ||--o{ provider_revisions : "provider_id"
  provider_profiles ||--o{ provider_health : "provider_id"
  provider_profiles ||--o{ breaker_transitions : "provider_id"
  provider_profiles ||--o{ breaker_overrides : "provider_id"
  applications |o--o{ routing_policies : "application_id"
  categories |o--o{ routing_policies : "category_id"
  routing_policies ||--o{ routing_policy_versions : "routing_policy_id"
  templates ||--o{ trigger_rules : "template_id"
  trigger_rules ||--o{ trigger_rule_revisions : "rule_id"
  categories ||--o{ recipient_preferences : "category_id"
  provider_profiles |o--o{ suppressions : "source_provider_id"
  applications ||--o{ notification_requests : "application_id"
  templates ||--o{ notification_requests : "template_id"
  categories ||--o{ notification_requests : "category_id"
  trigger_rule_revisions |o--o{ notification_requests : "trigger_rule_revision_id"
  notification_requests ||--o{ deliveries : "request_id"
  template_versions ||--o{ deliveries : "template_version_id"
  template_variants ||--o{ deliveries : "variant_id"
  provider_profiles |o--o{ deliveries : "selected_provider_id"
  deliveries |o--o{ deliveries : "resend_of_delivery_id"
  deliveries ||--o{ delivery_payloads : "delivery_id"
  deliveries ||--o{ delivery_attempts : "delivery_id"
  provider_profiles ||--o{ delivery_attempts : "provider_id"
  provider_revisions ||--o{ delivery_attempts : "provider_revision_id"
  deliveries ||--o{ delivery_events : "delivery_id"
  provider_profiles ||--o{ provider_callbacks : "provider_id"
  deliveries |o--o{ provider_callbacks : "delivery_id"
  deliveries ||--o{ inapp_messages : "delivery_id"
  applications ||--o{ delivery_metrics_hourly : "application_id"
  templates ||--o{ delivery_metrics_hourly : "template_id"
  categories ||--o{ delivery_metrics_hourly : "category_id"
  provider_profiles |o--o{ delivery_metrics_hourly : "provider_id"
```

`template_versions`, `template_variants`, `layout_versions`, `provider_revisions`, `routing_policy_versions` và `trigger_rule_revisions` là bất biến sau khi `PUBLISHED`. `template_releases` và `publication_releases` dùng `ordinal` tăng đơn điệu (rollback tạo release mới trỏ về version cũ). `delivery_payloads` và `workflow_instance_contexts` lưu dữ liệu nhạy cảm mã hóa AES-256-GCM và phân vùng theo ngày để xóa nhanh theo TTL; `deliveries`, `delivery_attempts`, `delivery_events`, `provider_callbacks` phân vùng theo tháng. Các bảng nội bộ của hàng đợi River đặt trong schema kỹ thuật riêng `notification_queue` trong cùng `notification_db`; chi tiết tại [Notification Service](notification-service.md).

### projection

| Bảng | Cột riêng (`?`: nullable) |
|---|---|
| instance_views | `instance_ref: text`; `process_key: text`; `process_version: bigint`; `business_key: text`; `case_ref: uuid?`; `status: text`; `branch_ref: text?`; `started_at: timestamptz`; `completed_at: timestamptz?`; `source_version: bigint` |
| node_execution_facts | `instance_view_id: @instance_views`; `execution_ref: text`; `element_key: text`; `started_at: timestamptz?`; `completed_at: timestamptz?`; `wait_ms: bigint?`; `processing_ms: bigint?`; `outcome: text?` |
| incident_views | `instance_view_id: @instance_views`; `element_key: text`; `error_code: text`; `retry_count: int`; `status: text`; `source_version: bigint` |
| metric_buckets | `process_key: text`; `process_version: bigint`; `element_key: text?`; `bucket_start: timestamptz`; `bucket_seconds: int`; `dimensions: jsonb`; `measures: jsonb`; `watermark: text` |
| saved_queries | `owner_subject_ref: uuid`; `name: text`; `resource_type: text`; `query: jsonb`; `visibility: text` |
| export_jobs | `owner_subject_ref: uuid`; `query: jsonb`; `status: text`; `object_ref: text?`; `expires_at: timestamptz?`; `watermark: text?`; `policy_version: text` |
| consumer_checkpoints | `consumer: text`; `partition: int`; `offset: bigint`; `source_event_id: text`; `applied_at: timestamptz` |
| inbox | `source: text`; `event_id: text`; `payload_digest: text`; `applied_at: timestamptz` |

```mermaid
erDiagram
  instance_views {
    uuid tenant_id PK
    uuid id PK
  }
  node_execution_facts {
    uuid tenant_id PK
    uuid id PK
  }
  incident_views {
    uuid tenant_id PK
    uuid id PK
  }
  metric_buckets {
    uuid tenant_id PK
    uuid id PK
  }
  saved_queries {
    uuid tenant_id PK
    uuid id PK
  }
  export_jobs {
    uuid tenant_id PK
    uuid id PK
  }
  consumer_checkpoints {
    uuid tenant_id PK
    uuid id PK
  }
  inbox {
    uuid tenant_id PK
    uuid id PK
  }
  instance_views ||--o{ node_execution_facts : "instance_view_id"
  instance_views ||--o{ incident_views : "instance_view_id"
```

### audit_search

| Bảng | Cột riêng (`?`: nullable) |
|---|---|
| audit_entries | `source_service: text`; `source_event_id: text`; `actor_ref: uuid`; `action: text`; `resource_type: text`; `resource_ref: text`; `before_digest: text?`; `after_digest: text?`; `evidence_ref: text`; `occurred_at: timestamptz` |
| consumer_checkpoints | `consumer: text`; `partition: int`; `offset: bigint`; `source_event_id: text`; `applied_at: timestamptz` |
| inbox | `source: text`; `event_id: text`; `payload_digest: text`; `applied_at: timestamptz` |

```mermaid
erDiagram
  audit_entries {
    uuid tenant_id PK
    uuid id PK
  }
  consumer_checkpoints {
    uuid tenant_id PK
    uuid id PK
  }
  inbox {
    uuid tenant_id PK
    uuid id PK
  }
```


### mdm

MDM sở hữu model/dataset publication; details về UI, distribution và freshness trong [MDM service](mdm-service.md). Các quan hệ bên dưới nội tenant; shared publisher references phải dùng explicit scoped publication links, không tạo FK cross-tenant.

| Bảng | Cột riêng (`?`: nullable) |
|---|---|
| data_models | `model_key: text`; `name: text`; `description: text?`; `status: text` |
| model_drafts | `model_id: @data_models`; `base_version_id: @model_versions?`; `revision: bigint`; `field_schema: jsonb`; `constraints: jsonb`; `ui_schema: jsonb` |
| model_versions | `model_id: @data_models`; `version: bigint`; `field_schema: jsonb`; `constraints: jsonb`; `ui_schema: jsonb`; `digest: text`; `compatibility: text` |
| master_datasets | `dataset_key: text`; `model_id: @data_models`; `name: text`; `status: text`; `source_owner: text` |
| record_drafts | `dataset_id: @master_datasets`; `model_version_id: @model_versions`; `record_key: text`; `revision: bigint`; `payload: jsonb`; `valid_from: timestamptz`; `valid_to: timestamptz?`; `operation: text` |
| master_records | `dataset_id: @master_datasets`; `record_key: text`; `status: text` |
| record_versions | `record_id: @master_records`; `model_version_id: @model_versions`; `version: bigint`; `payload: jsonb`; `digest: text`; `valid_from: timestamptz`; `valid_to: timestamptz?`; `tombstone: boolean` |
| record_references | `source_version_id: @record_versions`; `field_key: text`; `target_record_version_id: @record_versions`; `relation_type: text` |
| source_mappings | `record_version_id: @record_versions`; `source_system: text`; `external_code: text`; `valid_from: timestamptz`; `valid_to: timestamptz?` |
| dataset_releases | `dataset_id: @master_datasets`; `model_version_id: @model_versions`; `release_number: bigint`; `manifest_ref: text`; `digest: text`; `status: text`; `published_at: timestamptz` |
| release_members | `release_id: @dataset_releases`; `record_version_id: @record_versions` |
| publication_bundles | `publication_scope: text`; `generation: bigint`; `previous_generation: bigint?`; `digest: text`; `manifest_ref: text`; `approval_ref: text?`; `active_at: timestamptz`; `status: text` |
| bundle_items | `bundle_id: @publication_bundles`; `dataset_release_id: @dataset_releases` |
| publication_heads | `publication_scope: text`; `generation: bigint`; `bundle_id: @publication_bundles` |
| validation_reports | `dataset_id: @master_datasets`; `draft_revision_digest: text`; `schema_digest: text`; `result: jsonb`; `status: text`; `expires_at: timestamptz?` |
| consumer_subscriptions | `workload_ref: text`; `dataset_id: @master_datasets`; `view_scope: jsonb`; `supported_schemas: jsonb`; `distribution_policy_epoch: bigint`; `lease_policy: jsonb`; `status: text` |
| consumer_sync_status | `subscription_id: @consumer_subscriptions`; `consumer_instance_ref: text`; `generation: bigint`; `checkpoint: text`; `last_seen_at: timestamptz`; `status: text` |
| mdm_jobs | `job_type: text`; `dataset_id: @master_datasets?`; `command_id: uuid`; `expected_revision_digest: text?`; `status: text`; `result: jsonb?`; `attempts: int`; `next_attempt_at: timestamptz?`; `lease_until: timestamptz?` |
| outbox | `event_id: uuid`; `aggregate_id: uuid`; `aggregate_version: bigint`; `event_type: text`; `payload: jsonb`; `published_at: timestamptz?`; `attempts: int` |
| inbox | `source: text`; `event_id: text`; `payload_digest: text`; `applied_at: timestamptz` |
| command_receipts | `command_id: uuid`; `intent_digest: text`; `actor_ref: uuid`; `aggregate_id: uuid`; `status: text`; `result: jsonb`; `expires_at: timestamptz?` |
| audit_evidence | `actor_ref: uuid`; `action: text`; `resource_ref: text`; `command_id: uuid?`; `before_digest: text?`; `after_digest: text?`; `occurred_at: timestamptz` |

```mermaid
erDiagram
  data_models {
    uuid tenant_id PK
    uuid id PK
  }
  model_drafts {
    uuid tenant_id PK
    uuid id PK
  }
  model_versions {
    uuid tenant_id PK
    uuid id PK
  }
  master_datasets {
    uuid tenant_id PK
    uuid id PK
  }
  record_drafts {
    uuid tenant_id PK
    uuid id PK
  }
  master_records {
    uuid tenant_id PK
    uuid id PK
  }
  record_versions {
    uuid tenant_id PK
    uuid id PK
  }
  record_references {
    uuid tenant_id PK
    uuid id PK
  }
  source_mappings {
    uuid tenant_id PK
    uuid id PK
  }
  dataset_releases {
    uuid tenant_id PK
    uuid id PK
  }
  release_members {
    uuid tenant_id PK
    uuid id PK
  }
  publication_bundles {
    uuid tenant_id PK
    uuid id PK
  }
  bundle_items {
    uuid tenant_id PK
    uuid id PK
  }
  publication_heads {
    uuid tenant_id PK
    uuid id PK
  }
  validation_reports {
    uuid tenant_id PK
    uuid id PK
  }
  consumer_subscriptions {
    uuid tenant_id PK
    uuid id PK
  }
  consumer_sync_status {
    uuid tenant_id PK
    uuid id PK
  }
  mdm_jobs {
    uuid tenant_id PK
    uuid id PK
  }
  outbox {
    uuid tenant_id PK
    uuid id PK
  }
  inbox {
    uuid tenant_id PK
    uuid id PK
  }
  command_receipts {
    uuid tenant_id PK
    uuid id PK
  }
  audit_evidence {
    uuid tenant_id PK
    uuid id PK
  }
  data_models ||--o{ model_drafts : "model_id"
  model_versions |o--o{ model_drafts : "base_version_id"
  data_models ||--o{ model_versions : "model_id"
  data_models ||--o{ master_datasets : "model_id"
  master_datasets ||--o{ record_drafts : "dataset_id"
  model_versions ||--o{ record_drafts : "model_version_id"
  master_datasets ||--o{ master_records : "dataset_id"
  master_records ||--o{ record_versions : "record_id"
  model_versions ||--o{ record_versions : "model_version_id"
  record_versions ||--o{ record_references : "source_version_id"
  record_versions ||--o{ record_references : "target_record_version_id"
  record_versions ||--o{ source_mappings : "record_version_id"
  master_datasets ||--o{ dataset_releases : "dataset_id"
  model_versions ||--o{ dataset_releases : "model_version_id"
  dataset_releases ||--o{ release_members : "release_id"
  record_versions ||--o{ release_members : "record_version_id"
  publication_bundles ||--o{ bundle_items : "bundle_id"
  dataset_releases ||--o{ bundle_items : "dataset_release_id"
  publication_bundles ||--o{ publication_heads : "bundle_id"
  master_datasets ||--o{ validation_reports : "dataset_id"
  master_datasets ||--o{ consumer_subscriptions : "dataset_id"
  consumer_subscriptions ||--o{ consumer_sync_status : "subscription_id"
  master_datasets |o--o{ mdm_jobs : "dataset_id"
```

RecordVersions/payload và modelVersions là immutable sau publication. Current draft, publication_heads và consumer_sync_status là mutable CAS. Mọi publication đóng manifest snapshot, head generation, audit/outbox trong owner transaction; không để readers thấy partial bundle. Record draft revision được digest vào validation report; chỉ publish đúng revision đã kiểm. Release members phải thuộc đúng dataset và model version; bundle không chứa hai release của cùng dataset. Các scope/uniqueness constraints chi tiết trong tài liệu MDM; DBML chưa encode đầy đủ CHECK/unique/index.

Consumer-local read model (không phải MDM authority): snapshot staging theo scope/bundle, immutable record lookup index, active bundle pointer, inbox/checkpoint và distribution authorization lease. Consumer tự sở hữu migration; không thêm các bảng đó vào MDM database để các services dùng chung.

## Quan hệ cross-context (logical, không FK)

```mermaid
flowchart LR
  Case[case.cases] -. workflow link .-> Engine[Engine instance reference]
  Case -. customer subject .-> Access[access.subjects]
  Case -. offering version .-> Authoring[authoring.process_versions và form_versions]
  Case -. document links .-> Document[document.document_versions]
  Human[human.work_items] -. instance reference .-> Engine
  Human -. assignee hoặc candidate .-> Access
  Human -. form version .-> Authoring
  Integration[integration.integration_bindings] -. element/version .-> Authoring
  Projection[projection.instance_views] -. committed events .-> Engine
  Notification[notification.deliveries] -. committed events .-> Engine
  Notification -. recipient subject .-> Access
  Audit[audit_search.audit_entries] -. evidence ref .-> Owner[authoritative owner audit]
```

## Constraint và index bắt buộc khi physical design

| Phạm vi | Constraint / index |
|---|---|
| Natural keys | UNIQUE tenant + org code, external subject, role code, permission code, process/decision/form/template/service/endpoint key; version tables UNIQUE tenant + parent_id + version. |
| Membership/grant | UNIQUE tenant + subject + org; role_permission tenant+role+permission; role binding và direct grant phải có scope/effect uniqueness; expires_at > created_at khi có expiry. |
| BPMN/DMN | UNIQUE tenant+process_version+element_key; tenant+decision_version+clause_key; tenant+decision_version+rule_key; rule+clause cell unique; ordinal unique trong version/direction; clause/rule scope phải đồng nhất. |
| Case | UNIQUE tenant+tracking_code; business_key uniqueness scope cần chốt với Engine start contract; case_workflow_links UNIQUE tenant+start_command_id và instance_ref; workflow_start_jobs UNIQUE tenant+command_id. |
| Worklist | Index tenant+assignee+status+due_at+id và tenant+queue+status+priority+id. Source engine_version monotonic; reject stale event; one live claim bằng owner CAS, không browser lock. |
| SLA/assignment | max_active > 0; escalation/deadline >= 0; timer unique theo task/policy/trigger; queue_memberships và candidates đúng một subject hoặc role; delegation depth bounded, không vòng. |
| Documents | byte_size >= 0, checksum bắt buộc khi finalize; UNIQUE tenant+document+version; object_key immutable; scan verdict enum; upload expiry; no attach/download before permitted scan state. |
| Queries/metrics | Index tenant+process_key+started_at+id, tenant+instance_ref; metric bucket unique theo full dimensions hash+bucket; monotonic watermark. Không index PII plaintext. |
| Delivery/callback | UNIQUE tenant+endpoint+external_event_id; intent/event+recipient+channel uniqueness; attempt/deadline cap; retry index status+next_attempt_at; lease expiry index. |
| Infrastructure | Outbox event_id unique per owner/tenant, unpublished partial index; inbox UNIQUE tenant+source+event_id; receipts UNIQUE tenant+command_id with payload digest comparison; checkpoint UNIQUE tenant+consumer+partition. |
| Lifecycle | CHECK state/outcome/type enums, allowed state transitions trong owner; optimistic UPDATE WHERE row_version=expected; immutable version constraints; audit retention và evidence signing theo policy. |

## Physical design gates

Chưa có migration SQL cho toàn bộ logical model để tránh biến đề xuất thành schema chạy thật. Các migration additive cho Authz branch row filter là ngoại lệ của vertical slice được liệt kê ở Mapping schema hiện có. Với mỗi migration tiếp theo cần chốt nullability theo use case, version scope composite constraints, key type tương thích schema hiện có, RLS/tenant guards, phân vùng/retention, cardinality, query plans, encryption và backup/recovery. DBML mô tả PK/FK và nullability; các uniqueness/check/index ở bảng trên là yêu cầu bổ sung chưa được encode hết vào DBML.

Chỉ triển khai migration additive theo owner: expand schema → dual-compatible readers/writers nếu cần → backfill idempotent → verify counts/digests + tenant scope → switch read/write → contract sau cửa sổ rollback. Không dùng mock array làm nguồn backfill production.

## Database placement và reporting extension

Xem [service–database matrix](databases-and-reporting.md) để phân biệt storage của Engine, service-owned PostgreSQL, object storage và reporting read stores. Các bảng `projection.*` trong DBML vẫn thuộc Projection; không chuyển ngầm sang Report. Report facts/metadata là proposed extension được mô tả trong mục 7 của tài liệu Reporting, chưa có physical schema hoặc ERD/DBML chi tiết. Các local replicas là derived data, không cross-service FK và không owner mới cho business state.

## UUID và retention operations

New platform-owned entity PK/FK dùng native UUID; existing external/legacy refs có compatibility mapping riêng. Outbox/log tables bắt buộc có scheduled cleanup owner-local với eligibility/checkpoint/retention/archive rules trong [backend standards](backend-operations.md). Common soft-delete metadata không thay thế technical retention protocol và không cho phép purge business/audit history tùy ý.
