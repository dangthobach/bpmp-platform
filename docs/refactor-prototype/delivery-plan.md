# Lộ trình triển khai và acceptance gates

## Phạm vi thay đổi hiện tại

Đợt này tạo khảo sát source, thiết kế, mô hình logic/ERD và inventory generator. Không thay runtime, không tạo production migration, không coi mock behavior là yêu cầu đã được chốt. Các phase dưới đây là **workstreams đề xuất**, không thay số phase hoặc gate trong `docs/ods-refactor-progress.md`.

## Backlog có thứ tự và điều kiện hoàn tất

| Ưu tiên | Workstream / owner | Deliverable | Gate để hoàn tất |
|---|---|---|---|
| P0 | Product + frontend + domain owners | Walkthrough inventory, phân loại required/demo/unsupported cho từng action; chuẩn hóa glossary Case/Task/Instance | Mọi control có use-case ID hoặc lý do loại; persona/action matrix, accepted state transitions và error states; ghi evidence runtime |
| P0 | Platform/release | Đóng hosted/staging rehearsal đang tồn tại | Signed catalog, unavailable/incompatible remote, rollback, session/CSRF và trace evidence trên môi trường được chỉ định; không tự bỏ gate |
| P0 | Architecture/domain | Review bounded contexts, external engine policy, authoritative data, physical mappings | ADR cho thay đổi boundary; xác nhận reuse Human/Authz/Projection, không tạo authority song song |
| P1 | Design system + Shell | Tokens, primitives, QueryTable/forms/command states, component fixtures, dependency rules | Hai consumer representative; keyboard/focus/contrast, theme isolation, independent remote build; không regress task-mfe |
| P1 | Task vertical slice | Tasklist prototype → Task MFE hiện hữu; officer panel dùng cùng command | Hai actor claim cùng task chỉ một thành công; stale version; complete/sendback chuẩn workflow; retry giữ key; receipt vs projection UX |
| P1 | Case + Document + Portal | Service catalog, submit, start saga, upload/scan, detail/comment/lookup | Timeout sau Engine commit không tạo duplicate; document chưa scan không attach; INTERNAL comment không leak; customer khác không lookup được |
| P2 | Authoring + compiler | BPMN/DMN drafts, version history/diff, form versions, deployment job | Publish immutable; concurrent edit conflict; reject unsupported extensions; XML roundtrip; pinned dependency versions; no fake deploy toast |
| P2 | Operations + Governance | Instance detail, cancel, incident retry; migrate/move chỉ khi Engine có verified semantics | Dry-run, actor/approval, expectedVersion, per-item batch results; reconnect/lag và error không làm sai runtime |
| P2 | Access + IdP adapter | Users/roles/policies/queues, bulk jobs, service account lifecycle | Revocation propagation, tenant isolation, last-admin protection, cert rotation overlap; audit evidence giữ tại owner |
| P3 | Foundation owners | Assignment, templates, endpoints, notification/document settings | Concurrent assignment limits/fairness; probe results thật; secrets không vào browser; bounded retry/DLQ; idempotent callbacks |
| P3 | Analytics/Audit | Heatmap, throughput, health, portal metrics, authorized exports | Metric definitions/dedup đúng; source watermark và freshness; export expiry/visibility; replay không đếm lặp |
| P3 | Migration/release | Canary theo tenant/capability, route-by-route strangler, legacy retirement | Golden-path parity, rollback rehearsal, compatible DB, traffic/latency/error baselines, không còn legacy route consumers |

## Test matrix tối thiểu

| Boundary | Kịch bản phải chứng minh |
|---|---|
| Auth | Cross-tenant ID, forged actor/role, expired session, CSRF, policy revoked trong khi form đang mở; UI hide action không thay backend deny |
| Commands | Double click, response lost after commit, retry same intent, same key different payload rejected, 409 no auto overwrite, partial batch failure |
| Events | Duplicate/out-of-order/gap, poison schema, Kafka unavailable, consumer restart sau effect trước ack, outbox replay |
| Projections | Rebuild, lag, stale query, SSE reconnect/cursor expired; frontend không báo confirmed trước source version |
| Documents | Size/type/checksum mismatch, interrupted upload, malware/quarantine, signed URL expiry, document from another tenant, legal hold |
| Authoring | Bad XML/entity expansion, graph dangling ref, unsupported node, conflicting DMN rules, evaluation timeout, old version dependency, concurrent save |
| MFE | Missing/incompatible/tampered remote, remote render crash, CSS conflict, theme/session switch, React compatibility, rollback catalog |
| Business | Request docs/sendback thực sự mở bước tương ứng, approve chưa đồng nghĩa disburse, multi-task instance không hoàn tất quá sớm, internal comment segregation |

Unit/property tests đặt cạnh owner; contract tests kiểm Protobuf/DTO/route compatibility; integration dùng PostgreSQL+Engine; Playwright cho trải nghiệm xuyên remote. Universal properties theo catalog repo, không tự thêm tag trùng. Không dùng mock-only E2E làm bằng chứng durability.

## Kiểm tra khi triển khai

Chạy các gate liên quan tới slice; full contribution gate vẫn theo CONTRIBUTING và môi trường hỗ trợ:

```sh
npm ci
npm run verify:ods
npm run test:ods-e2e
npm run test:ods-release
cargo fmt --check
cargo clippy --workspace --all-targets --all-features -- -D warnings
cargo test --workspace --all-targets
buf lint
buf breaking --against contracts/baseline/v1.binpb
```

Go test trong modules đã sửa, dùng `-race` khi môi trường hỗ trợ; `./tools/check.ps1` cho full repo gate và adapter gates theo CONTRIBUTING. Đây là commands cho implementation sắp tới, không phải các check đã chạy trong đợt documentation này.

## Dữ liệu chuyển đổi và rollback

1. Khảo sát dữ liệu/schema thực, không import mock arrays và timestamp/identity giả từ prototype.
2. Additive migrations trong owner hiện hữu; context mới có DB/credential riêng; bảo toàn tenant và natural/external IDs.
3. Backfill có cursor/checkpoint/dedup và report count/digest; đối soát sample nghiệp vụ, quyền truy cập và historical versions.
4. Shadow-read có che PII; canary route theo tenant/capability. Không dual-write độc lập cùng aggregate vào legacy và owner mới.
5. Cutover command authority một lần theo aggregate; rollback route/catalog vẫn đọc được schema mở rộng. Với irreversible command đã commit, dùng owner-defined reconciliation/compensation; rollback UI không đảo Engine history.
6. Retire legacy chỉ sau khi hết consumer cũ, đủ evidence và cửa sổ rollback; contract migration destructive là đợt riêng.

## Thông tin cần chốt trong review

Product/domain: persona thực, Case lifecycle, form rules, service catalog, branch hierarchy, policy action catalog, SLA calendar, external engine requirement. Platform/security: IdP/OIDC lifecycle, workload identity, document storage/scan, retention/legal hold. Operations: tenants/CCU, active tasks/instances, query mix, file size/throughput, RPO/RTO và SLO. Chưa có các số này nên không tuyên bố mô hình đủ capacity production.

## Workstream MDM bổ sung

Triển khai theo [MDM design](mdm-service.md): (1) model/dataset drafts, typed validation, owner migrations và publication contract; (2) MDM Admin qua BFF và immutable bundle publish; (3) một consumer replica + SDK ResolveBatch/local lookup; (4) Case/Task form reference integration, Engine evidence/replay compatibility; (5) outage/recovery/tenant isolation và performance gates trước mở rộng toàn platform. MDM publication là dependency trước consumer rollout, không chờ đến cuối roadmap mới xử lý các dropdown/reference đã dùng ở Case/Task.

Definition of done: warm pinned path 0 synchronous MDM calls, dependency misses được batch, atomic bundle swap, no cross-tenant leakage, publication/schema conflict bị reject, revoked subscription không tiếp tục serving ngoài lease policy, replay vẫn đúng khi MDM offline/đổi current version. Đo round-trip/cold-start/sync lag/bundle size và p95 theo baseline trước khi chốt SLO. Logical schema/DBML là proposal, chưa có migrations hoặc service chạy.

## Workstream Reporting bổ sung

Theo [database/reporting design](databases-and-reporting.md), giữ operational queries ở Projection và khảo sát nguồn/metric grain trước khi tách Report. Các slice: (1) source-to-fact/event contracts và signed query-policy parity; (2) PostgreSQL transactional ingest/dedup/checkpoint + dashboard aggregate; (3) versioned MDM dimensions, cross-domain reconciliation/late corrections; (4) benchmark query/ingest/export, chỉ thêm OLAP/CDC nếu có bằng chứng; (5) failover/rebuild, cut-based report versioning và canary. Không đưa reporting ingest/query vào synchronous business transaction.

Acceptance: duplicate/crash replay không làm tăng tổng; report query không phát business data RPC; source cut/lag thể hiện đúng; cross-tenant/revoked actor bị deny; load report/export không làm business write SLO vi phạm; snapshot/backfill không mất hoặc đếm hai lần dữ liệu live. Source và sink worker ownership/bounds/recovery nằm trong tài liệu Reporting; trước implementation phải cập nhật current worker/traceability catalogs bằng evidence thực.

## Entity/API contract workstream

Trước mỗi implementation slice, review [entity conventions](entity-conventions.md) và [API Spec](api-spec.md). Baseline business entity phải có audit fields, soft-delete metadata và optimistic CAS; migrations additive, existing data backfill có system principal/lineage. Contract gate bắt buộc: target schemas được refine, capabilities map typed owner policy, source UI action/handler được review, version/delete/restore race tests và current route/OpenAPI drift tests pass. Không coi generated lexical API candidates là coverage đã nghiệm thu.

## Cross-service operations gates

Theo [backend standards](backend-operations.md), từng service phải có UUID entity mapping, `/healthz` + readiness + metrics có management config/probe tests; mỗi outbox/log owner có periodic cleanup schedule, retention policy, distributed lease/fencing và metrics/runbook. Gate gồm pending/held data protection, safe checkpoint/archive verification, crash recovery và cleanup load budget. Existing `/livez`/JSON metrics không tự tính là đã đáp ứng toàn bộ target contract.
