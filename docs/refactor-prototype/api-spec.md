# BPMP API Specification

Version: `0.1.0-design`, 2026-11-10. **Design catalog, không phải xác nhận API production đầy đủ.** Ưu tiên P0 cho runtime BPMP/authoring/query/task; P1 cho các business/admin capabilities bám prototype. P0 ở catalog là ưu tiên sản phẩm, không đồng nghĩa semantics đã được Engine hỗ trợ.

## 1. Artifacts và nguồn sự thật

- [Design OpenAPI 3.1 JSON](api-spec.openapi.json): current Gateway operations + proposed operations, request/response schemas, headers, errors, owner/capability và priority.
- [Operation reference](api-operation-reference.md): đầy đủ method/path/operationId/payload và internal RPC inventory.
- [Machine catalog](api-catalog.json): metadata và evidence cho phát triển/traceability.
- [UI action → API mapping](ui-api-mapping.md), [CSV](ui-api-mapping.csv): từng control/event trong snapshot inventory; phân biệt mapped candidate, UI-only/draft và unresolved review.
- [Entity conventions](entity-conventions.md): bắt buộc audit/soft-delete/version.

Nguồn contract **đang có** vẫn là `apps/go/api-gateway/internal/apidocs/openapi.json`, route registry trong `internal/gateway/handler.go` và `contracts/proto/bpmp/*/v1`. File thiết kế không được embed/publish thay contract runtime khi endpoints chưa implement. `current-registration` chỉ là bằng chứng contract/route, không phải proof deployment, availability hoặc full behavior correctness.

Tái tạo từ repo root:

```sh
python3 tools/build-api-design.py
python3 tools/map-prototype-api.py
```

Không sửa generated artifacts thủ công. Generator giữ SHA-256 của embedded OpenAPI baseline. Existing operations giữ nguyên body/responses; các metadata `x-status/x-owner/x-priority/x-evidence` chỉ phục vụ design.

## 2. Quy ước public API

**Namespace:** chuẩn hóa target thành `/v1/...` theo Gateway hiện có. Các path `/api/v1/...` trong tài liệu thiết kế trước là logical facade examples, chưa triển khai; dùng catalog này làm target route planning, không tự thêm namespace thứ hai. BFF route có thể khác transport RPC nhưng không đổi authority.

**Auth:** bearer JWT cho API clients hoặc opaque BFF session cookie cho browser; cookie unsafe requests phải có CSRF proof. `X-BPMP-Tenant-ID` phải khớp authenticated scope; `X-Correlation-ID` xuyên boundary. Service-to-service dùng mTLS và actor/workload proof theo contract hiện hữu. Không nhận actor/role/audit metadata trong body rồi tin là authority.

**Commands:** `X-Command-ID` + `Idempotency-Key`, cùng payload digest cho cùng intent. API receipt phân biệt accepted/committed với projection convergence. Query dùng POST không vì vậy mà trở thành business mutation. Validation/evaluation không tự deploy/commit workflow.

**Optimistic lock:** target mutations hiện hữu bắt buộc strong `If-Match` hoặc expectedVersion theo operation. OpenAPI target chọn `If-Match` cho mutable resource operations và expectedVersion khi payload cần; cả hai có mặt phải đồng nhất. Collection create dùng idempotency, không yêu cầu ETag tài nguyên chưa tồn tại. Publication baseGeneration, draft revision và batch item versions có concurrency contract riêng, không giả các counter cùng meaning.

**Soft delete:** CRUD DELETE business entity là tombstone + version increment + audit/outbox. Restore có explicit endpoint và quyền riêng. DELETE `/v1/query-exports/{id}` hiện tại vẫn là **cancel job**, không đổi thành delete entity. Cancel instance không phải soft delete. Audit/event/immutable publication không có arbitrary PATCH/DELETE nội dung; catalog không tạo CRUD cho chúng.

**Paging:** target list có opaque scope/query-bound cursor, pageSize 1–100 là design cap ban đầu; production cap do validated configuration, không tự áp vào existing endpoints. IncludeDeleted chỉ khi owner cho phép và actor có quyền. Filter/order/group-by dùng typed allowlist; không nhận SQL hoặc arbitrary column names.

**Responses:** entityVersion là persistence concurrency counter, khác model/workflow/release version. Audit fields read-only và có masking. Created command trả receipt/resourceId; client GET resource để lấy entity/current ETag. Async job trả status URL/job ID theo concrete response contract; không coi HTTP 202 là business hoàn tất.

**Errors:** `application/problem+json` theo schema Problem baseline. Target semantics: 400 malformed, 401 authn, 403 deny, 404 not-visible/not-found, 409 domain version/state/idempotency conflict, 412 ETag mismatch, 422 validation, 428 missing precondition, 429 quota/cost, 503 required dependency/snapshot unavailable. Thêm 413 cho upload/body limit và 410 cho expired cursor/artifact tại implementation slice. Không âm thầm đổi existing status codes; compatibility rollout bắt buộc.

## 3. BPMP core full-feature scope và khoảng trống

| Capability | Current evidence | Target API và implementation gate |
|---|---|---|
| Start workflow | `POST /v1/workflows/{workflowType}/instances` → Engine HandleCommand | Giữ route/receipt/idempotency; validate immutable workflow/artifact và input contract; enrich initial variables/business-key contract nếu cần, không coi đã có |
| Runtime capability/catalog | Existing browser config, WIR/compiler/runtime | GET runtime capabilities, runnable workflows/versions; không hiển thị feature unsupported như đã bật |
| Command status | Receipt hiện tại trả command_id/committed_sequence/duplicate | GET commands/{id}; cần authorized durable receipt lookup, không leak receipt sau revoke |
| Query/detail/history | Projection typed query/aggregate/event query đã có | Instance detail/diagram/variables/history facades reuse typed projection, không đọc RocksDB trực tiếp |
| Runtime control | Không có cancel/suspend/resume/migrate/move commands trong current CommandEnvelope oneof | Target routes là backlog **blocked on verified Engine semantics**; dry-run/approval/version required |
| Message/signal correlation | Runtime boundary/timer machinery có; không có generic public message/signal route trong Gateway registry | Target correlate routes phải có correlation uniqueness, subscriptions, TTL/authorization; không expose generic injection |
| User task | Get/list/complete/delegate và claim/unclaim HTTP có | Giữ existing actions; add form schema/validation/richer submit-form, batch/history; mọi outcome qua cùng Engine authority |
| Service tasks/workers | Internal HandleCommand service-task completion + bidirectional OpenDispatchStream có | Worker completion/lease/credit qua internal protocol; planned admin job list/retry/cancel không bypass lease fencing |
| Script/multi-instance/boundary | Typed command variants và event contracts đã có | Không cần tạo public REST cho mọi internal event; external invocation chỉ khi use case/capability cho phép |
| CMMN runtime case | ActivateCase/EvaluateCaseSentries và Human case query có | `/v1/runtime-cases` command facades; tách `/v1/customer-cases` hồ sơ customer khỏi existing `/v1/cases/{caseID}` runtime query |
| DMN/BPMN/form authoring | Compiler/runtime có, authoring API owner planned | Model/draft/version/check-in/validation/compare/revert/export/import/deployment và evaluation sandbox; target DTO definition phải map compiler profile |
| Governance | Internal approval/prepare-commit/revoke RPCs có | Public governance facade cần workflow approval/authz; không expose EngineGovernanceService trực tiếp |
| Realtime | Cockpit SSE current | Giữ reconciliation-only; không SSE payload như authoritative command result |

Internal RPC đầy đủ được trích tự động trong operation reference. `RaftPeerService` chỉ cluster peers; `RemoteWorkerDispatchService` chỉ authorized workers; Governance RPC chỉ approved service calls. Không bọc toàn bộ RPC vào generic public `/commands` có arbitrary payload.

### Completion flow current và target form extension

Current HTTP payload ví dụ (đọc exact schema trong baseline):

```http
POST /v1/work-items/task-123/complete
X-BPMP-Tenant-ID: tenant-1
X-Correlation-ID: corr-1
X-Command-ID: cmd-1
Idempotency-Key: intent-1
Content-Type: application/json

{"decision":"APPROVE","expected_version":7}
```

Bearer/session-CSRF theo môi trường. Current complete request không có arbitrary `values`/form schema fields. Target `POST /v1/work-items/{workItemID}/submit-form` bổ sung expectedVersion, formVersionId, values, outcome, reason và masterDataBundleId; cần durable form submission/evidence contract và compatibility tests. Không thêm property vào JSON frontend rồi giả backend đã lưu.

## 4. Coverage theo prototype feature

| Prototype | API families / hành vi cần map |
|---|---|
| Header / Sidebar | Runtime config, session context/tenant switch, preferences; navigate/collapse/dark local không cần gọi business API mỗi click |
| ModelerModule / Palette / Properties | Authoring process model draft; graph edits/drag/pan/zoom local; save/check-in/validate/deploy/artifact là API actions |
| DMN table / visual / history / compare / inspect | Decision draft chứa clauses/rules/cells; cell edits local đến save; version/evaluation/compare/revert/export endpoints; inject sample conflict chỉ dev |
| Operation | Typed process query, diagram/variables/history; cancel, migrate plan/execute, token-move plan/execute, batch; không PATCH projection để đổi runtime |
| Tasklist | Existing work-item list/detail/claim/unclaim/complete/delegate; planned form validation/submit/history/batch; sort/filter current page local, server search là query |
| Task Management / Access Audit | Queue + typed policy CRUD/validate/publish/revoke; role/resource grants; audit query/export; drawer edit local, save/publish là command |
| Identity | User lifecycle, memberships/roles/direct permission/security/bulk; service account certificate jobs; resource probe/status; server enforces last-admin và mandatory audit |
| Foundation / Endpoints | Templates/instantiate, assignment policy, endpoints/probe, document settings, notification templates/preferences; không generic Common-service CRUD |
| Client Portal | Service offerings, customer Case CRUD/submit/withdraw/lookup/timeline, comments, document attachment/upload; officer action reuse Task command, không Case approve trực tiếp |
| Embedded viewer | Authorized diagram/variable query, local zoom/selection/copy; export/copy không mở quyền đọc hidden values |
| Optimize / dashboards | Existing Projection aggregate cho operational scope; proposed analytics heatmap/throughput/health/portal-metrics, report query/export/schedule; incident retry về Engine |
| MDM Admin (mới theo yêu cầu, chưa có trong prototype) | Models/datasets/records/validation/import/releases/publication/snapshot/change/subscription APIs; không invent source UI evidence |

Bulk actions cần enum allowlist theo owner và discriminated per-action input trong implementation contract: user lock/unlock/department/roles/permissions/security/delete/export; role grant/revoke/type/assignment/delete/export; permissions assign/revoke/mandatory-audit/export; resources probe/status/export; workload certificate rotate/renew/revoke/export; tasks approve/reject; instance cancel/migrate/move. Target batch schema hiện là envelope chung, `action` và `values` phải được refine trước sinh production client; không arbitrary patch owner state. “Permanent delete” trong prototype map Governance purge review, không generic hard-delete API.

## 5. Các HTTP surface current ngoài Gateway

Đây là **service-local/internal HTTP**, không mặc nhiên route public qua Gateway; auth từ owning source, không bearer/browser assumption chung:

| Owner | Current surface | Evidence |
|---|---|---|
| Cockpit Gateway | `GET /realtime/v1/events?names=...`, Last-Event-ID; bearer/tenant/correlation; cursor unknown → resync-required | `apps/go/cockpit-gateway/README.md` |
| Authz control plane | `POST /authz/v1/check`, `/filter`, `/query`, `/query-revoke-feed`, `/explain`; `POST /admin/v1/query-revoke` | `apps/rust/authz-control-plane/crates/authz-server/src/app.rs` |
| Authz admin | `/admin/v1/tenants` POST/GET; `/tenants/:id` GET/PUT/DELETE; `/tenants/:id/status` PUT | `handlers/admin/mod.rs`; DTO/status semantics trong handlers |
| Authz policy/RBAC | `/admin/v1/policies` POST; `/policies/:id/promote` POST; `/revoke` POST, `/revoke/:user_id` DELETE; `/roles`, `/permissions`, `/users/:user_id/roles` POST | Cùng Authz admin router; service JWT middleware |
| Health/metrics/docs | Listener/path theo service bootstrap config | Operational endpoints không tính như UI/business feature APIs; không đưa vào business CRUD |

Nếu target Admin BFF gọi existing Authz API, phải map/extend typed contract và optimistic concurrency, không lập một authority RBAC thứ hai. Các route internal trên chưa có OpenAPI payload đầy đủ trong design JSON; source/DTO review là release gate trước expose hoặc phát hành SDK.

## 6. Known contract gaps và giới hạn “đầy đủ”

Catalog là danh mục đủ rộng để lập kế hoạch full-feature APIs theo scope đã khảo sát, **không phải tuyên bố mọi schema đã đủ để sinh production SDK**:

- Một số existing OpenAPI query/export operations dùng schema generic `object`. Exact types nằm trong Projection Protobuf/handler; giữ nguyên để tránh giả contract mới là hiện hữu. Phải bổ sung typed request/response với drift tests ở owner trước release.
- Target `definition`, form `values`, rules, typed filters, metrics và batch `values` là versioned domain documents. Spec hiện có envelope và owner, nhưng JSON Schema/rule grammar/enums/limits/reference constraints theo version phải được chốt ở implementation slice. Không chấp nhận arbitrary JSON/SQL chỉ vì schema là object.
- API scope/runtime capability/version support, per-operation capability strings trong target là logical design; cần map typed policy/action catalog hiện có và negative tests. Query/field policy không suy ra từ route name.
- Internal Authz HTTP payloads và browser/session producer còn cần contract audit riêng; gRPC inventory là source signatures, không thay full Protobuf comments/stream semantics.
- Native control + event có thể chỉ cùng một interaction; lexical mapping chỉ là development evidence. Mọi unresolved row và NO_DIRECT_HANDLER phải được phân loại và walkthrough; không đánh dấu 100% parity vì có operation ID trong bảng.
- Wire numeric precision, enum/oneof semantics và JSON naming: existing proto-backed APIs theo serializer/contract hiện tại (ví dụ `expected_version`), target facades dùng camelCase; explicit mapping và generated client tests bắt buộc.

## 7. Development và release gates

Mỗi action cần ticket: inventory source/hash/line → use-case → operationId → owner/RPC → DTO/schema version → typed capability → expectedVersion/ETag → loading/validation/success/403/409/timeout → audit evidence → contract/E2E test.

Thứ tự: (1) P0 core runtime/query/tasks và capability gaps; (2) authoring/version/deployment; (3) Portal/Case/Document; (4) Identity/Task Mgmt/Foundation; (5) MDM/Report extension theo dependency. Không implement unsupported cancel/migration chỉ bằng đổi projection state để đủ route.

Trước production client generation: resolve generic schemas và action enums, verify OpenAPI bằng parser/validator chuẩn, compare current method/path/operationIds với Gateway registry, lint/breaking Protobuf, negative tenant/auth/soft-delete/concurrency tests, double-click/lost-response retry, UI walkthrough theo từng persona. New audit fields/If-Match/status code là additive/versioned migration có compatibility rollout, không auto sửa tất cả runtime endpoints trong đợt docs.

## 8. Evidence của đợt thiết kế này

Catalog hiện có **313 HTTP operations: 31 current-registration + 282 proposed**, và **36 internal RPCs**. Traceability có **1.326 dòng**: 805 LOCAL_UI, 313 LOCAL_DRAFT, 195 API_CANDIDATE, 13 NO_DIRECT_HANDLER. Đây là lexical source classifications, không phải số use cases hoặc số tính năng đã implement; mọi dòng vẫn cần source/browser acceptance review.

Đã kiểm tra 31 current method/path/operationIds khớp Gateway registry và body/responses không bị sửa; operationIds duy nhất; local schema refs/path parameters/precondition headers; mapping IDs; 130 logical table envelopes không trùng cột; local document links và deterministic regeneration. Chưa chạy standards OpenAPI validator, browser walkthrough hoặc runtime/migration tests. Trước SDK publication phải qua các gate ở mục 6–7.

## 9. UUID và per-service management API

Mọi new owned entity ID là UUID; new target entity/reference schemas và path parameters được annotate `format: uuid` khi là entity ID. Existing contracts và các opaque instance/work-item/command refs, external subjects, business/chunk keys giữ explicit compatibility mapping, không tự sửa historical IDs. ID generation ngoài deterministic core; UUID không thay authorization.

[Management API design](service-operations.openapi.json) tách `/healthz`, `/readyz`, `/livez` compatibility alias và `/metrics` theo **từng backend service**, không coi đó là Gateway business routes hoặc cộng vào 313 business operations. Listener/network/metrics format và cleanup schedule contract theo [backend operations](backend-operations.md). File này là target design, chưa xác nhận runtime wiring.
