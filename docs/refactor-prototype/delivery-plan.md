# Lộ trình refactor

## Nguyên tắc

- Triển khai theo vertical slice có owner, persistence, authorization, API/UI và vận hành; không coi inventory hoặc prototype mock là bằng chứng tính năng chạy.
- Giữ sản phẩm đa lĩnh vực: khác biệt nghiệp vụ đi qua schema/config/policy có version và validation; invariant và quyết định domain vẫn ở owner.
- Mỗi slice chỉ chuyển trạng thái hoàn tất khi có contract, failure/retry behavior, audit/telemetry và bằng chứng tích hợp.

## Thứ tự ưu tiên

| Phase | Phạm vi | Gate hoàn tất |
|---|---|---|
| 0. Chốt yêu cầu | Walkthrough prototype, persona/action matrix, glossary, domain boundaries, threat model | Mỗi action có use case, owner, lifecycle và error states được domain/product xác nhận |
| 1. Nền tảng UI | Shell, design system, route isolation, session/CSRF, task vertical slice | MFE độc lập; keyboard/accessibility; stale version, duplicate click và lỗi mạng có UX đúng |
| 2. Nghiệp vụ lõi | Case/Document/Integration, Engine + Human, projection | Owner transaction; idempotent saga; không leak tenant/row/field; projection lag thể hiện rõ |
| 3. Authoring và deploy | BPMN/DMN/forms, signed WIR, durable jobs, Engine cluster activation/rollback | Job phục hồi được; signed immutable artifact; mọi node ACK cùng artifact/config trước `ACTIVE`; rollback trỏ phiên bản đã cài |
| 4. Access/Authz | Dynamic action/resource catalog, policy lifecycle, branch hierarchy, PEP/filter tại service owner | Publish/revoke có version, audit, staleness bound; query/command/export parity và negative tests |
| 5. Dữ liệu nền và analytics | MDM, reporting, audit search, notification | Publication/rebuild có checkpoint; query không gọi đồng bộ nhiều service; load không làm ảnh hưởng write SLO |
| 6. Migration/release | Shadow read, canary theo tenant/capability, cutover và retirement | Rehearsal có parity, rollback, schema compatibility, latency/error baseline và evidence |

## Trạng thái hiện tại

- Authoring có BPMN/DMN/form draft, validation, immutable versions; Compiler tạo và tra cứu WIR ký số.
- Engine có EngineDeploymentService RPC, ghi intent/ACK qua Raft, worker xác minh artifact/config, cài tại safe point, chờ ACK các node cấu hình rồi cập nhật active-version pointer. Peer forwarding cho intent/ACK đã được nối.
- Authoring API/job worker và Gateway routes cho deploy/rollback đã được nối; worker cấp sequence trong PostgreSQL transaction, ghi outbox, dùng lease để phục hồi và đối soát Engine status. Prototype có form gọi Gateway và poll trạng thái, nhưng cần kết nối với process model/version ID thật từ Authoring; không dùng ID mock để deploy.
- Engine lấy tập ACK bắt buộc từ Raft membership hiện hành. Địa chỉ peer/TLS của node vẫn cần được cấu hình và đồng bộ theo vận hành cluster.

## Gates xuyên suốt

| Boundary | Kết quả cần chứng minh |
|---|---|
| Authorization | Cross-tenant, sibling branch, queue ∩ branch, field leakage, revoked policy, missing filter; UI hide không thay backend deny |
| Commands/jobs | Retry sau timeout, cùng key payload khác nhau, worker restart, leader change, CAS conflict, partial node failure |
| Events/projections | Duplicate, out-of-order, gap, replay, consumer crash và lag; không báo complete trước committed state |
| Deployment | Sai chữ ký/hash/scope, config resolution failure, node offline, ACK thiếu/sai node, activate/rollback, restart và recovery |
| Release | Migrations additive; shadow/canary; cutover owner duy nhất; compensation theo semantics của owner |

Schema machine-readable/OpenAPI và inventory được dẫn trong [README](README.md). Các thiết kế chuyên sâu nằm trong `appendix/`; tài liệu này chỉ giữ thứ tự và điều kiện hoàn tất.
