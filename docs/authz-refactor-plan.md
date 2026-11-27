# Kế hoạch refactor phân quyền và multi-tenant (Authz)

- Trạng thái: Draft v2 (đã tích hợp Dynamic User Attributes & đồng bộ với kế hoạch Notification Service), cần review của Authz lead, Security và các owner service
- Ngày cập nhật: 2026-11-27
- Phạm vi: `apps/rust/authz-control-plane` (authz-server, authz-app và các crate nội bộ), `crates/bpmp-authz-contracts`, `crates/bpmp-authz-engine`, `crates/bpmp-adapter-policy-bundle`, `crates/bpmp-adapter-identity-jwt`, PEP trong `apps/go/*`, tích hợp API Gateway, CI/contract/deploy liên quan
- Ngoài phạm vi: Engine core (decide/evolve, Raft), BPMN compiler, nghiệp vụ notification (xem [notification-refactor-plan.md](notification-refactor-plan.md)), Access UI (chỉ tính điểm tích hợp API)
- Số dòng tham chiếu lấy theo snapshot hiện tại của repo và có thể lệch vài dòng khi code thay đổi.

## 0. Mục tiêu và nguyên tắc

### 0.1 Mục tiêu

1. Đóng các lỗ hổng phân quyền đã xác định mà không gián đoạn luồng đang chạy.
2. Đưa code về đúng kiến trúc đích trong ADR-008, ADR-012 và `docs/refactor-prototype/authorization-and-data-access.md`: control plane quản trị và ký bundle, PEP đánh giá cục bộ, fail-closed ở mọi nơi.
3. Có một bộ ngữ nghĩa đánh giá quyền duy nhất, một thư viện Go dùng chung, và bộ test vector kiểm chứng chéo với evaluator Rust.
4. Hỗ trợ **Dynamic User Attributes (`key:value`)** lưu tại `authz-server`, nhúng canonical vào `SignedActorContext`, và **Dynamic Attribute Scope Predicates** cho phép cấu hình lọc dữ liệu phân quyền phức tạp trên nhiều workflow mà không cần sửa code Authz hay Gateway.
5. Onboard service mới chỉ bằng cấu hình và thư viện, không phải sửa code Gateway hay Authz.
6. Gỡ PDP 5 lớp legacy và code chết sau khi đã chuyển hết consumer.

### 0.2 Nguyên tắc để không bị tắc

- **N1. Lưới an toàn trước.** CI, characterization test và golden vector phải có trước khi đổi hành vi.
- **N2. Tách thay đổi cơ học khỏi thay đổi ngữ nghĩa.** Thay đổi cơ học (không đổi hành vi) không chờ quyết định. Thay đổi ngữ nghĩa cần quyết định ở mục 3 và phải chạy chế độ audit hoặc shadow trước khi enforce.
- **N3. Expand → migrate → contract** cho DB, proto, API và route. Không có bước chuyển đổi một lần cho toàn hệ thống.
- **N4. Mỗi work package (WP) merge và deploy độc lập**, có cờ bật/tắt và cách rollback.
- **N5. Mỗi quyết định có người chịu trách nhiệm, hạn chót và phương án mặc định.** Hết hạn mà chưa chốt thì áp dụng mặc định và ghi lại lý do.
- **N6. Giữ tương thích wire trong v1.** Proto chỉ thêm field, kiểm bằng `buf breaking` với `contracts/baseline/v1.binpb`. Giao thức bundle và ACK không đổi với Engine và Projection đang chạy.
- **N7. Không nới fail-closed ở bất kỳ bước nào** (ADR-008, ADR-012 dòng 90).

### 0.3 Quy ước kích thước

| Size | Ước lượng cho 1 kỹ sư |
|---|---|
| XS | ≤ 1 ngày |
| S | 1–3 ngày |
| M | 3–7 ngày |
| L | 1–3 tuần |

### 0.4 Điểm giao và đồng bộ với kế hoạch Notification Service

Hai kế hoạch có 3 điểm giao trực tiếp cần phối hợp để không làm trùng hoặc gây conflict:

| Điểm giao | Kế hoạch Authz | Kế hoạch Notification | Cách đồng bộ |
|---|---|---|---|
| gRPC fail-closed `notification-service` (P0) | `S4` / `WP1.5` | `PR3` (mục 5) | Sửa ngay trong tuần đầu ở một PR nhỏ độc lập (`WP1.5`), không chờ tới Notification `PR3`. |
| CI Go & phiên bản Go | `WP0.3` (`ods-foundation.yml`) | `PR0` (`notification-quality`, Go `1.27.4`) | Dùng chung baseline Go `1.27.4` từ Notification `PR0`; `WP0.3` bổ sung các module còn lại (projection, case, cockpit, configuration, `go/platform`, `go/contracts`). |
| Capability & Owner Manifest `notification.*` | `WP3.2`, `WP3.5`, `WP4.1`, `WP4.3` | `PR9` (mục 1) | Notification `PR9` đăng ký `authorization-owner-manifest.json` và dùng `go/platform/authz` (`actorproof`) theo chuẩn `WP3.2`/`WP3.5`; Gateway chuyển route sang registry theo `WP4.1`. |

## 1. Hiện trạng (kết quả phân tích mã nguồn)

### 1.1 Bản đồ thành phần

| Thành phần | LOC | Vai trò | Phân loại |
|---|---|---|---|
| authz-server | 5.887 | Tenant registry, catalog và bundle, actor-context, identity profile, revoke feed | TARGET |
| authz-server (một phần) | | `check`, `filter`, `explain`, `query`, `policy`, gRPC | LEGACY |
| authz-app | 3.239 | Cây tổ chức, branch scope. Tự phân quyền qua PDP legacy | TARGET, nhưng phụ thuộc LEGACY |
| authz-engine | 4.136 | PDP 5 lớp (bitmap, cuckoo, ABAC, ReBAC, temporal, filter translator, shadow) | LEGACY toàn bộ |
| authz-db | 2.365 | Repo tenant, rbac (TARGET); loader, policy, rebac (LEGACY) | Hỗn hợp |
| authz-core | 1.266 | ids, errors, tenant (SHARED); model policy, filter, rebac, resource (LEGACY) | Hỗn hợp |
| authz-sdk | 475 | Client cho `check`/`filter`/`explain` | LEGACY |
| authz-http-middleware | 441 | Timeout, giới hạn body, rate limit, problem+json | SHARED |
| authz-cli | 3 | Stub | Code chết |
| bpmp-authz-contracts, bpmp-authz-engine, bpmp-adapter-policy-bundle, bpmp-adapter-identity-jwt | | Contract, evaluator thuần, bundle store, verifier JWT | TARGET. Authz workspace chỉ dùng contracts |
| PEP Go dùng bundle | | authoring-service, projection-service | TARGET, logic bị copy |
| PEP Go chỉ kiểm capability | | human-runtime, case-service, notification-service, cockpit-gateway, configuration-service | TARGET dạng thô |

### 1.2 Lỗ hổng bảo mật

| ID | Vấn đề | Bằng chứng | Mức |
|---|---|---|---|
| S1 | Route admin không kiểm capability lẫn tenant: tạo role/permission, toàn bộ CRUD tenant, upload/promote policy, `/authz/v1/check`, `/authz/v1/filter`, `/authz/v1/explain`. `check` nhận `tenant_id`, `user_id`, `user_attributes` từ body | `handlers/admin/rbac.rs` (`create_role`, `create_permission`), `handlers/admin/tenant.rs` (principal chỉ dùng để ghi audit), `handlers/admin/policy.rs`, `handlers/check.rs` (`check_handler`) | P0 |
| S2 | Claim `capabilities` và `tenant_id` trong service JWT do bên ký tự khai, không ràng buộc với `kid`. Chỉ route owner-manifest có ánh xạ `kid → owner`. Mọi key trong JWKS, kể cả key của owner service, đều tự ký được `authz.catalog.publish` cho tenant bất kỳ | `middleware/service_auth.rs:62-110`, `handlers/admin/catalog.rs:467-476` (chỉ owner-manifest), `catalog.rs:1175-1197` (`authorize_tenant` tin claim) | P0 |
| S3 | Xác thực JWT yếu: thuật toán lấy từ header token, không kiểm issuer, không giới hạn kích thước, gọi JWKS qua HTTP ở mỗi request mà không có timeout hay cache | `service_auth.rs:80-97`; `authz-app/src/presentation/middleware/identity.rs:79-106` | P1 |
| S4 | Đường fail-open: gRPC của notification-service trả identity giả mang đủ capability khi verifier là nil, và `NewServer` chấp nhận nil; authz-app có cờ `fail_open`; authz-server cho phép `FAIL_MODE=OPEN` | `apps/go/notification-service/internal/grpcapi/server.go:29-38, 56-58`; `authz-app/src/infrastructure/authz_adapter.rs:16-53`; `authz-server/src/app.rs:173`; `authz-core/src/models/tenant.rs:29` | P0 (notification), P1 (các cờ) |
| S5 | Gateway dùng `capabilities` trong JSON envelope trả về từ Authz, không lấy từ proof đã ký | `apps/go/api-gateway/internal/gateway/authorization_proxy.go:159-163`, `handler.go:752-753` | P2 |
| S6 | RLS của `authz_app` không có hiệu lực: không `FORCE`, authz-app không set `app.tenant_id`, ứng dụng chạy bằng role sở hữu bảng | `authz-app/migrations/001_organization.sql:49-59`; `platform/e2e/manifest.json:53-54` | P2 |
| S7 | Chưa có revoke phía ghi: actor proof không phải cho query mang `revoke_epoch = 0`; bảng `human_actor_revoke_epochs` không có nơi ghi; `AuthorizationRevokeEpochUpdate` không có consumer Go. Hiện chỉ dựa vào TTL ngắn của proof | `handlers/actor_context.rs` (nhánh `query_audience`), `apps/go/human-runtime/internal/adapter/postgres/store.go:33` | P2 |

### 1.3 Rủi ro vận hành

| ID | Vấn đề | Bằng chứng |
|---|---|---|
| O1 | Một message hỏng làm authz-server crash-loop. ACK consumer và readiness consumer gặp lỗi là `bail!`/`?` mà không commit offset. Worker nào lỗi thì `select!` cũng thoát toàn bộ server, khởi động lại gặp lại đúng message đó | `policy_bundle_acknowledgement_consumer.rs:27-85`, `tenant_lifecycle.rs:61-105`, cuối hàm `run` trong `app.rs` |
| O2 | Bundle publisher giữ transaction và row lock `FOR UPDATE` trong suốt lời gọi gửi Kafka | `policy_bundle_publisher.rs:53-111` |
| O3 | Projection dừng consumer khi một bundle không cài được, làm nghẽn partition của tenant. Authoring và projection dùng hai bộ kiểm tra khác nhau, nên một bundle có thể hợp lệ ở authoring nhưng bị projection từ chối | `apps/go/projection-service/internal/adapter/queryauth/bundle.go:219-221, 243-361` |
| O4 | `BundleStore.Ready()` của authoring không được gọi ở readiness | `apps/go/authoring-service/internal/adapter/authz/bundle_authorizer.go:131` (không có caller) |
| O5 | Consumer ID của authoring sinh mới mỗi lần khởi động, nên Authz thấy một consumer "mới" sau mỗi lần restart | `apps/go/authoring-service/cmd/authoring-service/main.go:230-232, 321` |
| O6 | Outbox của authz-app chỉ ghi log rồi đánh dấu đã xử lý, sự kiện tổ chức không đi đâu | `authz-app/src/infrastructure/messaging.rs:31-45` |
| O7 | Bundle do catalog publish có `grants` rỗng. Action Engine chưa được catalog hoá (case lifecycle) rơi về transition evaluator và bị `NO_MATCHING_GRANT` sau lần publish đầu tiên của tenant | `catalog.rs:694-708`; `apps/rust/bpmp-engine/src/authorization.rs:92-148` |
| O8 | Nâng version manifest gây cửa sổ deny: PEP authoring so khớp chính xác version và digest, trong khi mỗi owner chỉ có một manifest ACTIVE | `bundle_authorizer.go:163`; `016_authorization_catalog.sql` (`idx_authz_owner_one_active_manifest`) |

### 1.4 Chất lượng code và kiến trúc

| ID | Vấn đề | Bằng chứng |
|---|---|---|
| C1 | `catalog.rs` dài 1.299 dòng, trộn SQL, validate `serde_json::Value`, ký bundle, outbox, audit, truy vấn chéo schema và hex trong handler. Có struct `CatalogSnapshot` nhưng mỗi validator tự parse lại | `catalog.rs:77-134, 353-389, 647-758, 1021, 1036-1047, 1149-1162` |
| C2 | Kiểm tenant/capability bị lặp ở ít nhất 10 nơi, chỗ so UUID chỗ so chuỗi, chỗ trả 400 chỗ trả 403 | `catalog.rs:1175-1197`, `identity.rs:43-58`, `identity_profile.rs:48-59, 239-250`, `rbac.rs:146-157`, `actor_context.rs:57-69`, `query.rs:57-63`, `query_revoke.rs:23-30, 86-93` |
| C3 | Logic idempotency bị lặp | `identity_profile.rs:62-127, 260-293`, `rbac.rs:164-199` |
| C4 | Bốn kiểu vòng lặp outbox khác nhau | `tenant_lifecycle.rs`, `query_revoke_publisher.rs` (lease + generation fencing), `policy_bundle_publisher.rs`, `authz-app/src/infrastructure/messaging.rs` |
| C5 | Code verify JWT trùng giữa authz-server và authz-app; không dùng `bpmp-adapter-identity-jwt` đã có sẵn | `service_auth.rs:62-110`, `authz-app/.../identity.rs:78-108` |
| C6 | Proto authorization được biên dịch hai lần, với prost 0.12 và 0.14 (alias `prost14`) | `authz-server/build.rs:5-16`, `authz-server/Cargo.toml:12` |
| C7 | Go: logic verify bundle và consumer bị copy giữa authoring và projection; verify actor proof có 3 bản; `jwtauth` có 2 bản copy; ký workload proof có 3 bản | `authoring-service/internal/adapter/authz/`, `projection-service/internal/adapter/queryauth/bundle.go`, `human-runtime/internal/adapter/actorverifier/verifier.go`, `configuration-service/internal/adapter/httpapi/auth.go`, `api-gateway/internal/gateway/workload.go`, `case-service/internal/engineadapter/credentials.go`, `projection-service/internal/adapter/queryauth/branch_scope.go` |
| C8 | Gateway hardcode bảng route, ánh xạ audience, suy luận capability theo chuỗi path và khoảng 60 lời gọi `hasCapability` rải ở 9 file (79 chuỗi capability). Thêm một service phải sửa khoảng 6 chỗ | `handler.go:166-306, 733-735`, `authorization_proxy.go:295-349`, `authoring.go`, `forms.go`, `deployments.go`, `case_proxy.go`, `notification_proxy.go`, `realtime_proxy.go` |
| C9 | Code chết: gRPC `authz.proto` không có server khởi động (0 chỗ dùng `Server::builder`); `relations.rs` không được route; `EmergencyRevokeCache` không có nơi ghi ngoài test; `NoopJitFetcher`; `authz-cli` là stub; `GRPC_PORT` và nhiều biến trong `.env.example` không được đọc | `authz-server/src/grpc/`, `handlers/mod.rs`, `authz-engine/src/cache/emergency_revoke.rs`, `app.rs:49-64`, `config.rs:108-111` |

### 1.5 Ngữ nghĩa đánh giá quyền đang lệch nhau

| Điểm | Authoring (Go) | Projection (Go) | Rust `evaluate_resource` |
|---|---|---|---|
| Ràng buộc manifest | So khớp chính xác version và digest | Không kiểm | Không kiểm |
| Grant có danh sách field rỗng | Hợp các field mặc định | Giao các field mặc định | Không đóng góp field nào |
| DENY | Theo field được yêu cầu | Loại trừ hàng; deny toàn tenant thì chặn cả request | Khớp là chặn toàn bộ |
| Scope branch/queue/own | Chặn toàn request | Branch/own/resource thành điều kiện lọc hàng; queue chặn toàn request | Hỗ trợ cả ba, kết hợp AND |
| Dynamic attribute scope | Chưa có | Chưa có | Chưa có |
| Wildcard `*` | Không hỗ trợ | Có | Có |
| Quy tắc capability | Phải đúng capability được yêu cầu | Actor có capability của definition | Actor có capability của definition |
| `masked_fields` | Bỏ qua | Áp dụng | Bỏ qua |
| Kiểm tra khi cài bundle | Lỏng | Chặt (danh sách đã sắp xếp, effect hợp lệ, grant khớp catalog) | Do caller kiểm |

Proto định nghĩa field rỗng là "dùng bộ field mặc định của catalog" (`contracts/proto/bpmp/authorization/v1/authorization.proto`, comment của `readable_fields`). Không bản nào làm đúng hoàn toàn.

### 1.6 Dữ liệu và quyền sở hữu

- authz-server và authz-app dùng chung database và role, chỉ tách bằng `search_path` (`platform/e2e/manifest.json:53-54`).
- authz-server đọc thẳng `authz_app.org_node` khi validate và publish catalog (`catalog.rs:1036-1047`). Như vậy publish phụ thuộc vào schema và migration của authz-app.
- Hai bộ migration sqlx cùng dùng bảng `_sqlx_migrations` và đều bắt đầu từ version 1. Chúng không đè lên nhau chỉ vì khác `search_path`. `db/flyway.conf.example` trỏ cùng thư mục migration với một ledger khác.
- Bảng `user_account` đã có sẵn cột `attributes JSONB NOT NULL DEFAULT '{}'` (có GIN index `idx_user_attributes`) và `attributes_version BIGINT NOT NULL DEFAULT 0`, cùng bảng `user_attribute_history` (`apps/rust/authz-control-plane/crates/authz-db/migrations/001_identity.sql:35-71`). **Giữ lại** `user_attribute_history` để phục vụ audit cho tính năng Dynamic User Attributes (không xóa ở WP5.3).
- Bảng chỉ PDP legacy dùng: `resource_type`, `temporal_policy`, `policy`, `policy_rule`, `policy_version`, `policy_shadow_log`, `authz_decision_log`, `field_filter`, `row_filter`, `relation_tuple`, `relation_reachability`.
- Bảng không có tham chiếu trong code Rust cần xóa ở WP5.3 (chưa kiểm trigger trong `008_triggers.sql`): `resource_instance`, `resource_acl`, `schema_field_registry`, `group_partition`, `external_attribute_source`.

### 1.7 Phụ thuộc

| Thư viện | Authz workspace | Root workspace |
|---|---|---|
| axum | 0.7 | 0.8.9 |
| sqlx | 0.7.4 | 0.8.6 |
| tonic / prost | 0.11 / 0.12 (và prost 0.14 qua contracts) | 0.14.6 / 0.14.4 |
| reqwest | 0.11 | 0.12 |
| thiserror | 1 | 2 |
| edition | 2021 | 2024 |

Cargo.lock của Authz workspace không nằm trong phạm vi Dependabot (`.github/dependabot.yml` chỉ theo dõi cargo ở `/`).

### 1.8 CI, hợp đồng và triển khai

- `apps/rust/authz-control-plane/.github/workflows/ci.yml` nằm lồng trong thư mục con nên GitHub không bao giờ chạy. Root CI không build, lint hay test Authz workspace.
- Test riêng của các crate root liên quan authz không chạy (`rust-linux-authoritative.yml:70-76` chỉ test một số package khác).
- Go CI chỉ chạy vet và test cho api-gateway, authoring-service, human-runtime (`ods-foundation.yml:78-135`). Projection, case, cockpit, configuration, notification, `go/platform` và `go/contracts` không được kiểm.
- `buf lint`, `buf breaking` và kiểm drift generated code không chạy trong CI; chỉ có `tools/check.ps1` chạy thủ công.
- Không có Helm chart hay image release cho authz-server và authz-app. Image E2E là bản debug và build không có `--locked`.
- E2E seed owner manifest bằng SQL, bỏ qua route đăng ký, và chỉ seed 2 trên 4 owner (`tools/bpmp-e2e-fixtures/src/main.rs:1497-1530`).
- REST của Authz (actor-context, resolve-subject, catalog, branch-scopes) không có contract dạng máy đọc được.

### 1.9 Tài liệu lệch code

- `apps/rust/authz-control-plane/docs/OPERATIONS_GUIDE.md` mô tả bảng, route và metric emergency revoke không tồn tại.
- `docs/architecture/traceability-matrix.md` dẫn "Authz workspace tests" làm bằng chứng, nhưng CI không chạy các test đó.
- `contracts/baseline/README.md` nói CI kiểm breaking change, nhưng không workflow nào làm việc này.
- `docs/query-pdp.md` mô tả PDP gọi từ xa, trong khi projection đang dùng bundle cục bộ (`apps/go/projection-service/cmd/projection-service/main.go:185`).
- `docs/kafka-topology.md` thiếu topic bundle và ACK.

## 2. Kiến trúc đích chi tiết

### 2.1 Cấu trúc Authz Control Plane (Rust)

Giữ một deployable `authz-server`, nhưng tổ chức lại theo hexagonal (ports & adapters) như `design.md:145-155` và cách authz-app đang làm. Chỉ tách thành crate riêng khi có lý do về build hoặc tái sử dụng.

```text
authz-server/src/
  main.rs, app.rs          composition root: đọc config, dựng adapter, gắn route, chạy worker
  platform/                auth (Principal, guard), error, idempotency, audit, outbox, kafka,
                           config, health, metrics
  tenancy/                 domain (Tenant, vòng đời), application (create/update/status/delete),
                           adapters (pg, http)
  identity/                profile, user attributes & attribute schema registry,
                           resolve-subject, gán role, cấp actor-context
  rbac/                    role, capability (bảng permission), liên kết role-capability
  catalog/                 domain (OwnerManifest, CatalogSnapshot, validate thuần),
                           application (draft/validate/publish/register),
                           adapters (pg, sealer, http)
  distribution/            outbox publisher bundle, gia hạn, ACK consumer
  revocation/              query revoke (đã có), identity revoke (mới)
```

Quy tắc phân lớp:

- Handler chỉ parse input, gọi guard, gọi use case và map response.
- Use case giữ biên transaction và chỉ nói chuyện qua port.
- Domain gồm hàm thuần, test bằng unit test, không có I/O.
- Adapter chứa sqlx, Kafka, HTTP client và việc ký bundle.

### 2.2 Xác thực và ủy quyền giữa các service (xử lý S1, S2, S3)

1. **Xác thực JWT dùng `bpmp-adapter-identity-jwt`** (`crates/bpmp-adapter-identity-jwt/src/lib.rs:12-137`): danh sách issuer và thuật toán được phép, giới hạn kích thước token, JWKS snapshot thay thế nguyên tử (`replace_jwks`). Authz bổ sung adapter tải JWKS có cache, timeout và refresh khi gặp `kid` lạ.
2. **Workload registry**: một file cấu hình cho mỗi môi trường, thay cho `AUTHZ_OWNER_MANIFEST_KEY_IDS` và `AUTHZ_ACTOR_CONTEXT_AUDIENCES`.

   ```json
   {
     "workloads": [
       { "workload_id": "api-gateway", "key_ids": ["gw-k1"],
         "roles": ["actor-delegator"],
         "workload_capabilities": ["identity.actor-context.issue", "identity.subject.resolve"] },
       { "workload_id": "authoring-service", "key_ids": ["authoring-k1"],
         "roles": ["owner", "actor-context-audience"],
         "workload_capabilities": ["authz.catalog.owner-register"] }
     ]
   }
   ```

3. **Hai loại lời gọi:**
   - *Workload-only* (resolve-subject, cấp actor-context, đăng ký manifest, đọc revoke feed): capability phải nằm trong `workload_capabilities` của workload sở hữu `kid` đã ký token.
   - *Ủy quyền thay actor* (Access admin đi qua Gateway): token mang `actor_id` và `tenant_id`, và chỉ workload có vai trò `actor-delegator` được gửi. Authz **tự tra capability của actor trong DB** bằng cùng truy vấn đang dùng để cấp actor-context (`handlers/actor_context.rs:79-127`), không tin claim `capabilities`.
4. **Một guard dùng chung** gồm extractor `Principal` và `require(capability, tenant_scope)`. Capability của mỗi route được khai báo ở đúng một chỗ.

### 2.3 Phân phối bundle và worker (xử lý O1, O2, C4)

- **Outbox chuẩn:** claim bằng lease có generation fencing (theo mẫu `query_revoke_publisher.rs`), commit, gửi Kafka, rồi đánh dấu đã gửi. Không giữ transaction trong lúc gọi mạng.
- **Consumer:** message không hợp lệ được ghi vào bảng quarantine (hoặc topic DLQ), tăng metric và commit offset. Process không thoát vì một message.
- **Supervisor:** worker lỗi được khởi động lại với backoff. Không kéo sập HTTP server. `/readyz` phản ánh trạng thái worker.
- Một factory dựng Kafka producer và consumer.

### 2.4 Thư viện Go dùng chung `go/platform/authz`

```text
go/platform/authz/
  bundle/          Verifier (một bộ kiểm tra duy nhất), Store (Install/Get/Ready/Restore),
                   port Persistence
  bundle/pgstore/  Persistence cho PostgreSQL, bảng do từng service sở hữu
  bundlefeed/      Run(ctx, client, producer, store, cfg), ACK, StableConsumerID
  eval/            EvaluateResource (một tài nguyên), EvaluateQuery (sinh scope để lọc hàng,
                   bao gồm attribute scope predicates)
  manifest/        Load, Digest (tương thích serde_json của Authz)
  actorproof/      Verifier cho signed context và JWT, KeySource reload được, MaxTTL
  workload/        Signer (delegation JWT, SignedWorkloadContext), ActorContextClient
```

API công khai dự kiến:

```go
// bundle
func NewVerifier(keys KeySource, limits Limits) (*Verifier, error)
func NewStore(v *Verifier, p Persistence) (*Store, error) // p nil bị từ chối
func (s *Store) Install(ctx context.Context, raw []byte) (InstallResult, error) // Installed | Duplicate | Stale
func (s *Store) Ready(tenantIDs []string, now time.Time) error

// eval: Rust bpmp-authz-engine là oracle
func EvaluateResource(b *authv1.AuthorizationPolicyBundle, in ResourceInput, r Revocation) Decision
func EvaluateQuery(b *authv1.AuthorizationPolicyBundle, in QueryInput, r Revocation) (QueryScope, Decision)

// manifest
func Load(path string) (*Manifest, error)
func Digest(entries []json.RawMessage) ([32]byte, error)
```

Service Rust tiếp tục dùng `bpmp-adapter-policy-bundle` và `bpmp-authz-engine`. Thư mục `contracts/testdata/authz/v1/` chứa golden vector dùng chung cho cả Rust và Go.

### 2.5 Gateway dạng khai báo (xử lý C8)

Route registry là file cấu hình, được validate khi khởi động:

```yaml
- operation: createProcessModel
  method: POST
  path: /v1/authoring/process-models
  upstream: authoring-service
  actor_context_audience: authoring-service
  capabilities: { all_of: [authoring.createProcessModel] }
```

- Một proxy upstream dùng chung thay cho các file proxy viết tay.
- CI kiểm tra mọi capability trong registry đều có trong owner manifest đã đăng ký, hoặc trong danh sách capability nền tảng (`authz.*`, `identity.*`).

### 2.6 Hợp đồng

- **Proto:** Rust chỉ dùng một bộ type prost 0.14 cho `authorization`, `tenancy` và `configuration`. Hiện `bpmp-contracts/build.rs` chưa có `tenancy.proto`, cần bổ sung. Bỏ việc authz-server tự biên dịch proto.
- **REST:** tạo `contracts/openapi/authz-control-plane.v1.yaml` cho các route admin và internal, có lint và breaking check trong CI, sinh Go client cho Gateway, case-service và công cụ bootstrap tenant.
- Bỏ `authz-server/proto/authz.proto` (gRPC legacy, không có server).

### 2.7 Dữ liệu

- authz-server và authz-app có thể ở chung cluster PostgreSQL nhưng tách schema, role và ledger migration. Không SQL chéo schema.
- authz-server kiểm branch qua API nội bộ của authz-app (mTLS + workload proof) khi validate và publish.
- Bật `FORCE ROW LEVEL SECURITY` cho bảng theo tenant của `authz_app`. Unit of work set `SET LOCAL app.tenant_id` cho mỗi transaction.
- Production chạy migration bằng job một lần (`AUTHZ_MIGRATE_ONLY`, `authz-server/src/main.rs:49-50`) và tắt migrate lúc khởi động.

### 2.8 Dynamic User Attributes (`key:value`) và Dynamic Data-Scope cho đa workflow

Để đáp ứng yêu cầu cấu hình `key:value` attributes trên User phục vụ lọc dữ liệu phân quyền phức tạp cho nhiều workflow mà vẫn giữ kiến trúc local-evaluator, deterministic hashing và fail-closed:

1. **Lưu trữ & Schema Registry tại `authz-server`:**
   - Tận dụng cột `user_account.attributes` (JSONB), `user_account.attributes_version` (BIGINT) và bảng `user_attribute_history` đã có sẵn trong `001_identity.sql`.
   - Thêm bảng `tenant_attribute_definition` (migration mới) định nghĩa allowlist các `key` hợp lệ theo tenant: `key` (ASCII `^[a-z][a-z0-9._-]{0,63}$`, hỗ trợ namespace theo workflow như `credit.region`, `loan.approval_limit`, hoặc dùng chung như `region`, `product_lines`, `department`), `value_type` (`string | string_list | int64 | bool`), `max_values`, `is_active`, `version`.
   - API quản trị (`PUT/PATCH/GET /admin/v1/users/{id}/attributes`, `CRUD /admin/v1/attribute-definitions`) yêu cầu capability `identity.attributes.manage` / `identity.attributes.read`, có CAS theo `attributes_version`, idempotency, ghi diff vào `user_attribute_history`, và tăng `revoke_epoch` khi đổi attribute đang dùng cho phân quyền (kết nối `WP6.5`).
2. **Nhúng vào `SignedActorContext` (additive trong `authorization.proto` v1):**
   - Thêm message `ActorAttribute { string key = 1; repeated string values = 2; }` (hoặc union giá trị có kiểu) và field `repeated ActorAttribute attributes = 14;` vào `SignedActorContext`.
   - **Canonical & bounded:** Không dùng `map<string, ...>` vì thứ tự serialize protobuf map không deterministic. `actor_context.rs` sắp xếp `attributes` theo `key` tăng dần và `values` theo thứ tự từ điển trước khi tính `content_hash` và ký Ed25519; giới hạn cứng `max_attributes` (mặc định 32), `max_values_per_attribute` (mặc định 32), `max_value_bytes` (mặc định 128). Vượt giới hạn → fail-closed khi cấp proof.
3. **Khai báo khả năng enforce trong Owner Manifest & Grant (additive v1):**
   - `AuthorizationResourceDefinition` (owner manifest) thêm `repeated ResourceAttributeScope supported_attribute_scopes = 10;` khai báo rõ danh sách `attribute_key`, `value_type` và `supported_operators` mà service owner thực sự có cột/JSONB index và biết enforce (nguyên tắc: dynamic config không tạo ra filter mà code không enforce).
   - `AuthorizationResourceGrant` thêm `repeated AttributeScopePredicate attribute_predicates = 19;`:
     - `resource_attribute`: tên thuộc tính trên dữ liệu/workflow (ví dụ `region`, `product_line`, `workflow_type`, `risk_tier`).
     - `operator`: enum đóng (`EQ`, `IN`, `CONTAINS_ANY`, `GTE`, `LTE`).
     - `value_source`: `LITERAL` (giá trị tĩnh trong grant) hoặc `ACTOR_ATTRIBUTE` (tham chiếu động tới `actor_attribute_key`, ví dụ `region`, `credit.product_lines`).
   - Ràng buộc scope: `attribute_predicates` là một trục scope hẹp, **loại trừ lẫn nhau với `tenant_scope = true`**, và kết hợp **AND** với các trục `branch_scope_ids`, `queue_scope_ids`, `own_scope`, `resource_scope_ids` trong cùng một grant. Nhiều grant ALLOW hợp nhất bằng **OR**; Explicit **DENY** thắng mọi ALLOW.
4. **Đánh giá cục bộ (Rust oracle & Go PEP) và lọc dữ liệu đa workflow:**
   - **Single resource (`EvaluateResource`):** `ResourceAuthorizationInput` nhận thêm `actor_attributes` (từ `SignedActorContext`) và `resource_attributes` (từ nguồn authoritative của owner/WIR/projection, không từ client). Thiếu `actor_attribute_key` hoặc thiếu `resource_attribute` trên bản ghi khi grant có predicate yêu cầu → predicate trả `false` (**fail-closed**).
   - **Query row-filter (`EvaluateQuery` tại Projection):** Evaluator resolve `ACTOR_ATTRIBUTE` thành danh sách giá trị cụ thể của actor hiện tại rồi trả về cây điều kiện lọc có kiểu (`QueryScope.AttributePredicates`). Adapter `queryauth` biên dịch sang parameterized SQL (chỉ map tới cột hoặc đường dẫn JSONB nằm trong allowlist của owner manifest), áp trước `LIMIT/OFFSET/COUNT/AGGREGATE`.
   - **Đa workflow:** Các workflow khác nhau có thể khai báo bộ `resource_attributes` riêng (qua workflow binding/projection metadata) và cấu hình bộ `attribute_predicates` tương ứng trong catalog mà không cần sửa code Authz, Gateway hay thêm cột cứng cho từng nghiệp vụ.

### 2.9 Quy trình onboard service mới (trạng thái đích)

1. Service khai báo `authorization-owner-manifest.json` (gồm actions, fields và `supported_attribute_scopes` nếu có) và dùng `go/platform/authz` (hoặc crate Rust tương ứng).
2. Thêm workload vào workload registry của Authz và route vào route registry của Gateway. Chỉ sửa cấu hình.
3. Helm hook đăng ký manifest bằng key của chính service.
4. Công cụ bootstrap tenant áp template role, capability, attribute definitions và grant cho từng tenant, publish, rồi chờ ACK.

## 3. Các quyết định cần chốt

Mỗi quyết định có người chịu trách nhiệm và hạn chót là 1 tuần sau khi Phase 0 xong. Quá hạn thì áp dụng phương án mặc định và ghi vào decision log.

| ID | Câu hỏi | Phương án mặc định đề xuất | Lý do | Chặn |
|---|---|---|---|---|
| D1 | Ngữ nghĩa evaluator v1 | Rust `evaluate_resource` là chuẩn, với hai điều chỉnh. (a) Grant có danh sách field rỗng nhận field mặc định của các catalog definition có `capability` nằm trong `required_capabilities` của grant, đúng theo comment trong proto. (b) DENY khớp subject, resource và scope thì chặn action ở chế độ một tài nguyên, và thành điều kiện loại trừ hàng ở chế độ query. Giữ wildcard `*`. Actor phải có capability của definition và đủ mọi `required_capabilities`. `masked_fields` được áp dụng ở mọi PEP trả dữ liệu | Một nguồn chân lý. Engine không dùng field (manifest Engine để trống field), nên điều chỉnh (a) không đổi quyết định của Engine | WP3.3–WP3.5 |
| D2 | Có ràng buộc chính xác `manifest_version` và digest khi đánh giá không | Không. So khớp theo `(owner, resource_type, action, capability)`, giữ version và digest trong bundle để audit, và giữ quy tắc "action code không đổi nghĩa; đổi nghĩa thì tạo action mới" | Loại bỏ cửa sổ deny khi rolling upgrade (O8) | WP3.3 |
| D3 | Số phận PDP legacy (`check`, `filter`, `explain`, `query`, gRPC) | Gỡ bỏ. authz-app chuyển sang actor-context. `/authz/v1/query` không có caller production nên deprecate rồi xoá. Giữ `/admin/v1/query-revoke` và publisher Kafka | Theo ADR-008, PDP qua mạng không được nằm trên đường xử lý chính | WP5.1–WP5.3 |
| D4 | Revoke phía ghi (identity/membership/attributes) | Dùng `AuthorizationRevokeEpochUpdate` đã có trong proto, phát theo tenant/actor; PEP áp floor qua thư viện chung. Trong lúc chờ, giữ TTL proof ≤ 120 giây | Tái sử dụng contract có sẵn | WP6.5 |
| D5 | Ranh giới của authz-app | Cùng bounded context Authz, deployable riêng, schema và role riêng, giao tiếp qua API nội bộ | Phù hợp ADR-004 và ADR-012 dòng 31 | WP6.1, WP6.2 |
| D6 | Công cụ migration | Giữ sqlx (ledger đã áp dụng ở các môi trường), chạy bằng job một lần, bỏ Flyway example, ghi lại trong `db/README.md` | Tránh phải dựng lại ledger | WP6.4 |
| D7 | Gộp Authz workspace vào root workspace | Chỉ gộp sau khi gỡ legacy và nâng phụ thuộc | Giảm lượng code phải nâng cấp | WP5.5 |
| D8 | Kế thừa role | Giữ khớp chính xác role code như tài liệu hiện tại; ẩn `parent_role_id` khỏi API quản trị. Nếu cần kế thừa, bên cấp proof mở rộng danh sách role theo contract có version | Tránh hiểu nhầm giữa PDP cũ và mô hình bundle | WP4.4 |
| D9 | Ủy quyền giữa service | Workload registry và Authz tự tra capability của actor (mục 2.2) | Đóng S2 | WP1.3 |
| D10 | Transition grants của Engine sau khi publish catalog | Trước khi publish catalog lần đầu cho một tenant, Engine phải catalog hoá mọi action đang dùng, kể cả case lifecycle. Bước validate cảnh báo nếu tenant vẫn còn phụ thuộc transition grants. Phương án thay thế: thêm field `transition_grants` (additive) vào snapshot | Tránh O7 | Rollout catalog cho tenant dùng Case |
| D11 | Có tự tạo permission (capability) khi bật catalog entry không | Có. Khi publish, Authz bảo đảm tồn tại `permission.code = capability` cho mọi entry `enabled` (idempotent). Gán capability vào role vẫn là thao tác quản trị | Bỏ một bước thủ công dễ sai | WP4.5 |
| D12 | Phạm vi Dynamic User Attributes & Attribute Scope v1 | Lưu `user_account.attributes` (JSONB) + `tenant_attribute_definition` tại `authz-server`; nhúng canonical sorted `ActorAttribute` vào `SignedActorContext` (bounded); hỗ trợ `AttributeScopePredicate` (`EQ`, `IN`, `CONTAINS_ANY`, `GTE`, `LTE`) với `LITERAL` và `ACTOR_ATTRIBUTE` trên cả `EvaluateResource` và `EvaluateQuery`; thiếu attribute bắt buộc → fail-closed | Đáp ứng lọc dữ liệu phức tạp đa workflow mà giữ kiến trúc embedded evaluator không cần gọi mạng | WP2.7, WP3.7 |

## 4. Lộ trình

### 4.1 Tổng quan

Ước lượng thời gian giả định 2–3 kỹ sư chia theo lane. Các phase gối đầu nhau; phase sau không cần chờ phase trước xong hẳn nếu đã có đủ WP phụ thuộc.

| Phase | Mục tiêu | Thời lượng | Bắt đầu khi |
|---|---|---|---|
| P0 | Lưới an toàn: CI, characterization test, golden vector | 1–2 tuần | Ngay |
| P1 | Đóng lỗ hổng bảo mật, chạy audit trước khi enforce | ~2 tuần | WP0.1 và phần rbac/tenant của WP0.5 xong |
| P2 | Tái cấu trúc control plane (cơ học) + quản trị User Attributes | 3–4 tuần | WP0.5 xong |
| P3 | Thư viện Go dùng chung, thống nhất ngữ nghĩa + Dynamic Attribute Scope | 4–6 tuần | WP0.6 xong, D1/D2/D12 đã chốt cho phần ngữ nghĩa |
| P4 | Tự động hoá onboard, Gateway dạng khai báo | 2–3 tuần | WP1.3 và WP3.2 xong |
| P5 | Gỡ legacy, nâng phụ thuộc | 3–4 tuần | D3 đã chốt, WP5.1 xong |
| P6 | Dữ liệu và cô lập tenant | 2–3 tuần | D5/D6 đã chốt; chạy song song P4/P5 |
| P7 | Triển khai và tài liệu | Liên tục | Ngay |

### 4.2 Lane làm song song

| Lane | Phạm vi | WP |
|---|---|---|
| A. Platform/CI | Workflow, buf, Helm, Dependabot | WP0.1–WP0.4, WP0.7, P7 |
| B. Authz (Rust) | authz-server, authz-app, crate Rust | WP0.5, P1 (phần Rust), P2 (gồm WP2.7), WP3.7 (phần Rust), P5, P6 |
| C. Go platform | `go/platform/authz`, các PEP Go | WP0.6, WP1.5, P3 (gồm WP3.7 phần Go/Projection) |
| D. Gateway và tích hợp | api-gateway, công cụ onboard | WP1.6, P4 |

### 4.3 Phụ thuộc chính

```text
WP0.1 ──> WP0.5 ──> WP1.1 ──> WP1.2 ──> WP1.3 ──> WP4.2
                 ├─> WP2.1 ──> WP2.3 ──> WP6.1
                 └─> WP2.7 (User Attributes API & SignedActorContext) ──> WP3.7
WP0.3 ──> WP0.6 ──> (D1, D2, D12) ──> WP3.1 ──> WP3.2 ──> WP3.3, WP3.4, WP3.5, WP3.7 ──> WP4.1
WP0.4 ──> WP3.6 (OpenAPI) ──> WP4.5 (bootstrap CLI)
D3 ──> WP5.1 ──> WP5.2 ──> WP5.3, WP5.5
D5 ──> WP6.1 ──> WP6.2 ──> WP6.3
```

Đường găng: WP0.1 → WP0.5 → WP1.1 → WP1.3 → WP4.2. Các WP cơ học trong P2 và WP3.2 không nằm trên đường găng và không chờ quyết định.

### 4.4 Phase 0: Lưới an toàn (không đổi hành vi)

#### WP0.1 CI cho Authz workspace (Lane A, S–M)

- Thêm `.github/workflows/authz-control-plane.yml` với `working-directory: apps/rust/authz-control-plane`, gồm `cargo fmt --check`, `cargo clippy --workspace --all-targets --locked -- -D warnings`, `cargo test --workspace --locked` và service PostgreSQL.
- Path filter: `apps/rust/authz-control-plane/**`, `crates/bpmp-authz-contracts/**`, `contracts/proto/bpmp/{authorization,tenancy,configuration}/**`.
- Xoá thư mục `apps/rust/authz-control-plane/.github/`.
- Lần chạy đầu có thể đỏ vì lỗi tồn đọng. Cho phép job không bắt buộc tối đa 1 tuần, sửa xong thì chuyển thành required check.
- Xong khi: job xanh và là required check.

#### WP0.2 Test và clippy cho crate root liên quan authz (Lane A, S)

- `cargo test --locked -p bpmp-authz-contracts -p bpmp-authz-engine -p bpmp-adapter-policy-bundle -p bpmp-adapter-identity-jwt`, cùng clippy cho các crate này.
- Thêm path filter tương ứng vào `rust-linux-authoritative.yml`.

#### WP0.3 Go CI cho các module còn thiếu (Lane A, S)

- `go vet` và `go test -race` cho projection-service, case-service, cockpit-gateway, configuration-service, notification-service (đồng bộ với Notification `PR0` và Go `1.27.4`), `go/platform`, `go/contracts`.
- Path filter thêm `go/platform/**`, `go/contracts/**` và các thư mục service trên.

#### WP0.4 Kiểm tra hợp đồng (Lane A, S)

- CI chạy `buf lint`, `buf breaking --against contracts/baseline/v1.binpb`, và `buf generate` kèm `git diff --exit-code` cho `go/contracts/gen`.
- Pin version plugin trong `buf.gen.yaml`.
- Quyết định về `gen/rust` (không có consumer): xoá, hoặc đưa vào kiểm drift.

#### WP0.5 Characterization test cho authz-server (Lane B, M–L)

Dùng `sqlx::test` với PostgreSQL. Ghi lại hành vi hiện tại, kể cả hành vi sẽ sửa ở P1; các test đó đánh dấu rõ WP sẽ thay đổi chúng.

- **Owner manifest:** đăng ký lại idempotent; cùng version nhưng khác nội dung; dùng lại version đã retire; sai `kid` hoặc `sub`.
- **Catalog:** tạo draft; cập nhật với `expected_draft_revision` đúng và sai; mọi nhánh lỗi của `validate_snapshot`, `validate_owner_entries`, `validate_policy_grants`, kiểm role và branch; publish ghi version, bundle đã ký và outbox trong cùng một transaction; bundle giải mã và verify được.
- **RBAC:** assign/revoke với replay idempotency, xung đột idempotency, xung đột version.
- **Tenant:** create, update, chặn activation khi readiness chưa sẵn sàng, delete.
- **Actor-context:** roles và capabilities trả về, giới hạn số lượng, chặn khi query bị revoke.
- Xong khi: mỗi route admin có ít nhất một test, và test chạy trong WP0.1.

#### WP0.6 Golden vector chéo ngôn ngữ (Lane C, M)

- Một công cụ Rust nhỏ (trong `tools/` hoặc test của `bpmp-authz-engine`) sinh `contracts/testdata/authz/v1/`:
  - bundle đã ký bằng seed cố định;
  - các case `evaluate_resource`: allow, deny, từng loại scope (sau WP3.7 bổ sung thêm vector cho attribute predicates), field, DENY, revoke epoch, hết hạn, sai tenant;
  - digest của manifest;
  - actor proof hợp lệ và không hợp lệ (gồm canonical ordering của `attributes` khi WP2.7 hoàn tất);
  - bundle không ở dạng canonical.
- Test Go của authoring và projection chạy trên cùng bộ vector. Các chênh lệch đã biết được liệt kê trong file "expected-diff", mỗi dòng trỏ tới D1 hoặc D2. Test bỏ qua chênh lệch đã liệt kê và fail khi xuất hiện chênh lệch mới.

#### WP0.7 Quy ước PR (Lane A, XS)

- PR template ghi rõ loại thay đổi (cơ học hay ngữ nghĩa), test đã thêm và cách rollback.
- Trong lúc WP2.1 tách `catalog.rs`, các thay đổi khác vào file này phải đi sau WP2.1 để tránh conflict.

### 4.5 Phase 1: Bảo mật (thay đổi hành vi, audit trước)

#### WP1.1 Guard dùng chung (Lane B, S–M, cơ học)

- Thêm `platform/auth.rs` gồm `Principal` (subject, kid, actor_id, tenant_id dạng `Option<Uuid>`, capabilities), `require_capability` và `require_tenant`, cùng một kiểu lỗi 403.
- Thay các bản copy liệt kê ở C2 bằng guard, giữ nguyên hành vi. Nếu client đang dựa vào mã 400 của `/authz/v1/query*` thì giữ mã đó; kiểm bằng E2E.
- Phụ thuộc: WP0.5.

#### WP1.2 Đóng các route đang mở (Lane B, M; xử lý S1)

- Capability mới:
  - `authz.rbac.manage` cho tạo role và permission;
  - `platform.tenant.manage` và `platform.tenant.read` cho tenant registry. Đây là quyền cấp nền tảng, không gắn với một tenant;
  - `authz.check`, `authz.filter`, `authz.explain` cho PDP legacy. Token service trong E2E đã mang sẵn các capability này (`tools/bpmp-e2e-fixtures/src/main.rs:429-432`);
  - `authz.policy.manage` cho `/admin/v1/policies`, hoặc tắt route này vì là legacy.
- Route có `tenant_id` trong body phải khớp với tenant của principal.
- Cờ `AUTHZ_ROUTE_GUARD_MODE=audit|enforce`, mặc định `audit` trong 1 release. Chế độ audit ghi log và metric `authz_guard_violation_total{route}`. Khi metric bằng 0 thì chuyển `enforce`.
- Cập nhật mọi caller: fixture E2E, token service của authz-app, các script vận hành.
- Xong khi: ở chế độ enforce, test âm (thiếu capability, sai tenant) trả 403 và E2E xanh.

#### WP1.3 Workload registry và ủy quyền thay actor (Lane B, M–L; xử lý S2, D9)

- Config mới `AUTHZ_WORKLOAD_REGISTRY_FILE` theo mục 2.2, đọc khi khởi động. Validate rằng không có `kid` nào thuộc hai workload.
- Tương thích ngược: nếu chưa cấu hình file này, dựng registry từ `AUTHZ_OWNER_MANIFEST_KEY_IDS` và `AUTHZ_ACTOR_CONTEXT_AUDIENCES`.
- Route ủy quyền thay actor không tin claim `capabilities` nữa. Capability của actor được tra trong DB bằng truy vấn của `actor_context.rs:79-127`, chuyển vào repository dùng chung.
- Route workload-only yêu cầu capability nằm trong `workload_capabilities` của workload sở hữu `kid`.
- Áp dụng chế độ audit/enforce như WP1.2.
- Phụ thuộc: WP1.1.

#### WP1.4 Siết xác thực JWT (Lane B, M; xử lý S3)

- authz-server và authz-app dùng `bpmp-adapter-identity-jwt`, kèm adapter tải JWKS có cache (TTL, refresh khi gặp `kid` lạ, timeout HTTP).
- Config mới: `JWT_ISSUERS`, `JWT_ALGORITHMS`, `JWT_MAX_TOKEN_BYTES`, `JWKS_REFRESH_SECS`.
- Kiểm trước danh sách issuer và thuật toán đang dùng thực tế. Fixture ký bằng EdDSA; Keycloak thường dùng RS256.

#### WP1.5 Gỡ các đường fail-open (Lane B + C, S; xử lý S4)

- notification-service (đồng bộ với Notification `PR3`): `NewServer` trả lỗi khi verifier là nil. Nếu thật sự có luồng chỉ cần workload, tách thành server hoặc option riêng, đặt tên rõ ràng, với capability riêng.
- authz-app: bỏ cờ `fail_open` trong `authz_adapter.rs`.
- authz-server: bỏ `FAIL_MODE=OPEN`. Release đầu ghi cảnh báo và xử lý như `DENY`; release sau từ chối khởi động nếu còn cấu hình `OPEN`. Bỏ `fail_mode` khỏi `TenantConfig` theo expand/contract (vẫn đọc được nhưng bỏ qua).

#### WP1.6 Gateway lấy capability từ proof đã ký (Lane D, S; xử lý S5)

- Sau khi nhận actor-context, Gateway verify chữ ký proof bằng public key actor-context của Authz (thêm vào config Gateway), rồi lấy capability từ proof. Nếu capability trong envelope khác capability trong proof thì từ chối.

**Xong Phase 1 khi:** ma trận test âm trong `docs/refactor-prototype/authorization-and-data-access.md` (khoảng dòng 298-317) pass cho các route Authz, E2E xanh, và metric audit bằng 0 trước khi bật enforce.

### 4.6 Phase 2: Tái cấu trúc Control Plane và Quản trị User Attributes

#### WP2.1 Tách `catalog.rs` theo hexagonal (Lane B, L)

- `catalog/domain/{manifest.rs, snapshot.rs, validation.rs}`: type chặt, parse một lần, các hàm validate thuần.
- `catalog/application/{register_manifest, create_draft, update_draft, validate_version, publish_version, queries}.rs`: giữ biên transaction và gọi port.
- `catalog/ports.rs`: `CatalogRepository`, `ManifestRepository`, `RoleDirectory`, `BranchDirectory`, `BundleSealer`, `Clock`, `AuditLog`, `Outbox`.
- `catalog/adapters/{pg.rs, sealer.rs, http.rs}`.
- Thứ tự PR: queries → register_manifest → create/update draft → validate → publish. Mỗi PR phải giữ test WP0.5 xanh và JSON response không đổi (snapshot test).
- `#[serde(deny_unknown_fields)]` là thay đổi hành vi. Tách thành PR riêng sau khi kiểm payload thật mà Access UI gửi.

#### WP2.2 Helper dùng chung (Lane B, M)

- `platform/idempotency.rs` (begin/replay/complete, băm request), `platform/audit.rs`, `platform/hex.rs`, `platform/error.rs` (một bảng map `AuthzError` sang HTTP status).
- Áp dụng cho `rbac.rs` và `identity_profile.rs`.

#### WP2.3 Khung outbox và worker (Lane B, L; xử lý O1, O2, C4)

- `platform/outbox.rs`: claim có lease và generation fencing, không giữ transaction khi gửi, retry có backoff, message hỏng vào bảng quarantine kèm metric.
- `platform/kafka.rs`: factory cho producer và consumer.
- Áp dụng lần lượt cho: `policy_bundle_publisher` (O2), ACK consumer (O1: message hỏng thì quarantine rồi commit), readiness consumer, tenant lifecycle publisher, query revoke publisher (đã có fencing, dùng làm mẫu).
- Supervisor khởi động lại worker lỗi với backoff, không kéo sập HTTP server.
- Giữ ràng buộc tenant lifecycle phải có sequence liên tục (`tenant_lifecycle.rs`, kiểm `checkpoint + 1`).

#### WP2.4 Config có kiểu (Lane B, S)

- Gom biến môi trường vào struct có validate, chia nhóm db, jwt, kafka, signing, workers.
- Xoá các biến không được đọc khỏi `.env.example`, viết bảng biến trong README.

#### WP2.5 Health và metrics (Lane B, M)

- Thêm `/healthz`, `/readyz` (DB, signing key, worker còn sống) và `/metrics` (Prometheus) theo `CONTRIBUTING.md`. Giữ `/health` làm alias trong 1 release.
- Metric tối thiểu: `authz_catalog_publish_total{result}`, `authz_bundle_outbox_pending`, `authz_bundle_renewal_failures_total`, `authz_ack_quarantined_total`, `authz_guard_violation_total`, `authz_actor_context_issued_total{audience,result}`.

#### WP2.6 authz-app (Lane B, M)

- Tách phần verify workload proof của hai handler `/internal/v1/branch-scopes/*` thành extractor; đưa logic vào application query.
- Identity middleware dùng verifier chung của WP1.4, không đọc biến môi trường ở mỗi request nữa.
- Outbox: nối Kafka thật cho `OrganizationTreeChanged` theo ADR-012, hoặc tắt worker cho đến khi có consumer. Không đánh dấu "đã xử lý" khi thực tế không gửi đi đâu.

#### WP2.7 Quản trị Dynamic User Attributes và nhúng vào `SignedActorContext` (Lane B, M; theo D12)

- Migration mới thêm bảng `tenant_attribute_definition` (`tenant_id`, `key`, `value_type`, `max_values`, `workflow_namespaces`, `is_active`, `version`, audit columns). Giữ và sử dụng `user_account.attributes`, `user_account.attributes_version` và `user_attribute_history`.
- Proto (additive v1): thêm `ActorAttribute` và `repeated ActorAttribute attributes = 14` vào `SignedActorContext`; cập nhật `ActorProofCodec::seal/verify` trong `bpmp-authz-contracts` kiểm tra canonical order và giới hạn (`max_attributes`, `max_values_per_attribute`, `max_value_bytes`).
- API quản trị trong `authz-server` (`identity/attributes.rs`):
  - `GET/POST/PUT/DELETE /admin/v1/attribute-definitions` (`identity.attributes.schema.manage` / `read`)
  - `GET/PUT/PATCH /admin/v1/users/{id}/attributes` (`identity.attributes.manage` / `read`), kiểm tra khớp `tenant_attribute_definition`, CAS bằng `expected_attributes_version`, idempotency, ghi diff vào `user_attribute_history`.
- `actor_context.rs`: đọc `user_account.attributes` (chỉ các key đang active trong `tenant_attribute_definition`), sort canonical và nhúng vào `SignedActorContext` sau cờ `AUTHZ_ACTOR_ATTRIBUTES_EMBED_ENABLED` (mặc định `true` khi bảng có dữ liệu).

### 4.7 Phase 3: Thư viện tích hợp, thống nhất ngữ nghĩa và Dynamic Attribute Scope

#### WP3.1 ADR về ngữ nghĩa đánh giá quyền (Lane C + B, M)

- Viết `docs/adr/ADR-014-authorization-evaluation-semantics.md`. Số ADR-013 đã được `ADR-013-notification-service.md` dùng.
- Nội dung: bảng quy tắc theo D1/D2/D12, ví dụ cho từng scope (gồm `attribute_predicates` đa workflow), cách ánh xạ giữa chế độ một tài nguyên và chế độ query.
- Cập nhật golden vector và xoá dần danh sách expected-diff.
- Nếu cần sửa Rust (field rỗng nhận mặc định theo capability của grant), sửa trong `bpmp-authz-engine` kèm test. Quyết định của Engine không đổi vì manifest Engine không khai báo field.

#### WP3.2 Viết `go/platform/authz` (Lane C, L)

- Viết mới theo mục 2.4, đủ test và chạy golden vector. Chưa service nào dùng ở bước này.
- Port `Persistence` cho phép mỗi service giữ bảng của mình (`authoring_authorization_bundle_cache`, `projection_authorization_policy_bundle_state`).
- `bundlefeed`: message hỏng được quarantine hoặc bỏ qua theo chính sách cấu hình được, không dừng consumer. Chỉ gửi ACK khi sequence đang cài lớn hơn hoặc bằng sequence của message.
- `manifest.Digest` phải tương thích `serde_json::to_vec` phía Authz: decode số bằng `UseNumber`, từ chối số thực, key trùng và UTF-8 không hợp lệ, không escape U+2028/U+2029.
- Nếu D1 chưa chốt: viết theo ngữ nghĩa Rust hiện tại, các điểm còn tranh luận để dạng option. Không chờ quyết định.

#### WP3.3 Chuyển authoring-service sang thư viện (Lane C, M)

1. Cơ học: dùng `bundle` và `bundlefeed`, consumer ID ổn định theo hostname, gọi `Ready()` trong readiness (xử lý O4, O5).
2. Ngữ nghĩa, chạy shadow: evaluator mới chạy song song evaluator cũ và chỉ ghi chênh lệch vào metric `authz_shadow_divergence_total`, không đổi quyết định. Sau ít nhất 1 tuần không có chênh lệch ngoài dự kiến thì chuyển sang evaluator mới.
3. Xoá code cũ.

#### WP3.4 Chuyển projection-service sang thư viện (Lane C, M–L)

- Làm như WP3.3. Phần biên dịch scope thành điều kiện SQL vẫn nằm trong adapter `queryauth` của projection và dùng `eval.EvaluateQuery`.
- Dùng một bộ kiểm tra khi cài bundle cho mọi consumer, để không còn bundle "hợp lệ ở authoring nhưng hỏng ở projection" (O3).

#### WP3.5 Các service hiện chỉ kiểm capability (Lane C, M)

- **human-runtime:** thay `actorverifier` bằng `actorproof`; lấy revoke floor từ thư viện (sau WP6.5). Tuỳ chọn: enforce manifest `assignment_policy` bằng bundle, vì manifest này đã đăng ký nhưng chưa được enforce.
- **case-service:** dùng `workload.Signer` và `ActorContextClient`; đưa giới hạn vào config.
- **notification-service, cockpit-gateway:** đưa giới hạn vào config, dùng `actorproof` (đồng bộ Notification `PR9`).
- **configuration-service:** dùng `go/platform/jwtauth` thay bản copy.

#### WP3.6 OpenAPI cho REST của Authz (Lane D, M)

- `contracts/openapi/authz-control-plane.v1.yaml` gồm catalog, owner-manifests, identity, user attributes & attribute definitions, actor-context, rbac, tenants và internal branch-scopes.
- CI chạy lint và breaking check cho file này.
- Sinh Go client cho Gateway, case-service và công cụ bootstrap tenant.

#### WP3.7 Dynamic Attribute Scope Evaluation cho đa workflow (Lane B + C, L; theo D12)

- **Proto (additive v1):** thêm `ResourceAttributeScope` vào `AuthorizationResourceDefinition` (field 10) và `AttributeScopePredicate` (`resource_attribute`, `operator`, `value_source`, `literal_values`, `actor_attribute_key`) vào `AuthorizationResourceGrant` (field 19). Chạy `buf lint` và `buf breaking`.
- **Control Plane (`authz-server`):** validator của catalog kiểm tra mọi `attribute_predicates` trong grant đều: (a) khai báo trong `supported_attribute_scopes` của owner manifest, (b) toán tử tương thích kiểu dữ liệu, (c) nếu tham chiếu `actor_attribute_key` thì key đó tồn tại và đang active trong `tenant_attribute_definition`, (d) không kết hợp với `tenant_scope = true`.
- **Rust Oracle (`bpmp-authz-engine`) & Golden Vectors:** mở rộng `ResourceAuthorizationInput` thêm `actor_attributes` và `resource_attributes`; implement đánh giá `attribute_predicates` (fail-closed khi thiếu attribute hoặc kiểu không khớp); sinh bộ golden vector cho cả `LITERAL` và `ACTOR_ATTRIBUTE` trên nhiều kịch bản workflow.
- **Go PEP (`go/platform/authz/eval` & Projection `queryauth`):**
  - `EvaluateResource` khớp 100% golden vector với Rust.
  - `EvaluateQuery` resolve `ACTOR_ATTRIBUTE` thành `QueryScope.AttributePredicates` đã ràng buộc giá trị; adapter SQL của Projection biên dịch sang parameterized predicate trên cột/JSONB thuộc allowlist của owner manifest, kết hợp `AND` với `tenant_guard`, `branch`, `queue`, `own` và `AND NOT explicit_deny`.
- Chạy sau cờ `AUTHZ_ATTRIBUTE_SCOPE_MODE=off|shadow|enforce` trước khi bật mặc định.

### 4.8 Phase 4: Tự động hoá onboard và Gateway dạng khai báo

#### WP4.1 Route registry ở Gateway (Lane D, L)

1. Chạy shadow: thêm registry và proxy dùng chung, chạy song song code hiện tại. Test so sánh từng route (method, path → audience, capability) giữa registry và code cũ, phải khớp hoàn toàn.
2. Chuyển từng nhóm route sang registry theo thứ tự authoring → forms → deployments → case → notification → access/catalog, và xoá code cũ của nhóm vừa chuyển.
3. CI kiểm capability trong registry là tập con của capability trong manifest cộng capability nền tảng.

#### WP4.2 Workload registry dùng chung cho Authz và Gateway (Lane D + B, M)

- Một file cho mỗi môi trường mô tả workload: ID, `kid`, vai trò, audience. Authz (WP1.3) và allowlist audience của Gateway cùng đọc file này. Tuỳ chọn reload mà không cần restart.

#### WP4.3 Đăng ký manifest trong quy trình deploy (Lane A + D, M)

- Helm hook Job (`pre-install`, `pre-upgrade`) chạy `authz-owner-register` bằng secret key của service. Cân nhắc chuyển CLI từ `apps/go/api-gateway/cmd/authz-owner-register` sang `tools/` vì nó không thuộc Gateway.
- E2E đăng ký manifest qua route thật thay cho SQL seed, đủ cả các owner (gồm `notification-service` sau Notification `PR9`).

#### WP4.4 Hoàn thiện API RBAC (Lane B, M)

- `GET/PUT/DELETE /admin/v1/roles/{id}`, `GET /admin/v1/permissions`, `PUT/DELETE /admin/v1/roles/{id}/permissions/{permissionId}`. Tất cả có CAS, idempotency, audit và xoá mềm.
- Expose qua Gateway dưới `/v1/access/roles…` với capability `authz.rbac.manage`.
- API mới dùng tên "capability" thay cho "permission"; bảng `permission` vẫn giữ nguyên.

#### WP4.5 Công cụ bootstrap tenant (Lane D, M–L)

- `tools/authz-tenant-bootstrap` viết bằng Go, dùng client OpenAPI.
- Input là template YAML: role, capability của từng role, attribute definitions (cho các workflow của tenant), grant có placeholder cho branch và `attribute_predicates`.
- Lệnh `plan` (dry-run, in ra diff) và `apply`. `apply` lần lượt: bảo đảm role, permission, attribute definitions và liên kết tồn tại (idempotent); tạo draft từ snapshot đang publish và trộn entry/grant của template; validate; publish kèm reason; chờ ACK theo danh sách consumer bắt buộc (WP4.7).

#### WP4.6 Bảo vệ thao tác publish (Lane B, S–M)

- `POST /admin/v1/catalog/versions?from=published` tạo draft từ snapshot đang publish.
- Validate cảnh báo, hoặc chặn, khi draft làm mất entry/grant của một owner đang có trong bản publish. Muốn bỏ thì phải truyền cờ `allow_owner_removal` kèm reason.
- Kiểm tra theo D10 về transition grants.

#### WP4.7 Danh sách consumer bắt buộc (Lane B, M)

- Cấu hình consumer nào phải ACK cho từng owner. API trạng thái rollout chỉ ra consumer thiếu ACK hoặc lệch sequence. Công cụ bootstrap dùng API này làm điều kiện hoàn thành.

#### WP4.8 Tài liệu và service mẫu (Lane D, S)

- `docs/authz-service-onboarding.md` và một service Go mẫu dùng `go/platform/authz` (có ví dụ khai báo `supported_attribute_scopes`).

### 4.9 Phase 5: Gỡ legacy, đơn giản hoá, nâng phụ thuộc

#### WP5.1 authz-app thôi dùng PDP legacy (Lane B, M)

- Route tổ chức đi qua Gateway với actor-context có audience `authz-app`. authz-app verify proof cục bộ bằng `bpmp-authz-contracts` và kiểm capability `organization:*` (các permission code này đã có trong fixture).

#### WP5.2 Tắt rồi xoá route legacy (Lane B, M)

- Cờ `AUTHZ_LEGACY_PDP_ENABLED` (mặc định `true`) kèm metric số request.
- Khi metric bằng 0 trong 2 release liên tiếp thì xoá: crate authz-engine và authz-sdk; gRPC (`proto/authz.proto`, `grpc/`, tonic-build); handler `check`, `filter`, `explain`, `relations`, `policy`; `query.rs` và `query-revoke-feed`; `NoopJitFetcher`; `EmergencyRevokeCache`; bước warm-up `BundleLoader`.
- Giữ `/admin/v1/query-revoke` và publisher Kafka của nó.

#### WP5.3 Gỡ bảng legacy khỏi DB (Lane B, M)

- Export và backup trước, rồi viết migration tiến (forward-only) xoá các bảng legacy ở mục 1.6. Chỉ làm sau ít nhất 2 release không còn ai dùng. Kiểm trigger trong `008_triggers.sql` trước khi xoá.
- **Giữ nguyên** `user_account.attributes`, `user_account.attributes_version` và `user_attribute_history` (đang phục vụ Dynamic User Attributes từ WP2.7).
- Giữ bảng `permission` và `role_permission`, đánh dấu cột `conditions` là deprecated. `role.parent_role_id` xử lý theo D8.

#### WP5.4 Dọn artifact (Lane B, S)

- Xoá hoặc chuyển đi: `authz-cli` (hoặc thay bằng công cụ bootstrap), `frontend/*-prototype` (chuyển sang `apps/web` hoặc archive), `.augment/`, `skill.md`.
- Chuyển `AuthZ-Platform-Dynamic-5-Layer-Design.md` vào `docs/archive/`.
- Viết lại `docs/OPERATIONS_GUIDE.md` theo code thật; sửa doc comment ở đầu `authz-server/src/main.rs`.

#### WP5.5 Nâng phụ thuộc và gộp workspace (Lane B, L; theo D7)

- Bỏ tonic và prost 0.12; dùng chung prost 0.14 sau khi thêm `tenancy.proto` vào `bpmp-contracts`.
- Nâng axum lên 0.8 (cú pháp path đổi từ `:id` sang `{id}`), sqlx 0.8, reqwest 0.12, rustls 0.23, thiserror 2, edition 2024 và bật clippy pedantic.
- Cuối cùng chuyển các member vào root `Cargo.toml` để dùng chung một Cargo.lock và được Dependabot theo dõi.
- Theo ADR-006, mỗi lần nâng cần bằng chứng qualification (test và E2E).

### 4.10 Phase 6: Dữ liệu và cô lập tenant

#### WP6.1 authz-server thôi đọc `authz_app.org_node` (Lane B, M)

- Port `BranchDirectory` (từ WP2.1) đổi adapter sang gọi API nội bộ của authz-app (mTLS và workload proof). Có thể dùng endpoint descendants hiện có hoặc thêm endpoint validate theo lô.
- Fail-closed: nếu không gọi được thì publish trả lỗi có thể retry.

#### WP6.2 Tách role DB và ledger migration (Lane B, M)

- Mỗi bên có role riêng: authz-server, authz-app và role chạy migration. Cấp quyền tối thiểu.
- authz-app dùng bảng ledger nằm rõ trong schema `authz_app`.

#### WP6.3 Cho RLS có hiệu lực (Lane B, M; xử lý S6)

- `FORCE ROW LEVEL SECURITY` cho `org_aggregate` và `org_node`; role ứng dụng không sở hữu bảng; unit of work set `SET LOCAL app.tenant_id`.
- Test chứng minh truy vấn không có tenant context trả về 0 dòng.
- Tuỳ chọn: RLS cho các bảng theo tenant của authz-server, sau khi đo hiệu năng.

#### WP6.4 Quyền sở hữu migration (Lane B, S; theo D6)

- Production chạy job migration một lần và có cờ tắt migrate lúc khởi động.
- Bỏ Flyway example (hoặc chọn hẳn Flyway); cập nhật `db/README.md`, `db/scripts/verify-schema.sh` và `authz-db/tests/schema_contract.rs` cho khớp.

#### WP6.5 Revoke phía ghi (Lane B + C, L; theo D4, xử lý S7)

- Authz quản lý epoch identity/membership/attributes cho từng actor, phát `AuthorizationRevokeEpochUpdate` có chữ ký khi thay đổi role hoặc user attributes ảnh hưởng quyền, và đưa epoch thật vào actor-context.
- PEP áp floor qua thư viện chung. human-runtime chuyển sang nguồn này thay cho bảng hiện không có nơi ghi.

#### WP6.6 Transition grants của Engine (Lane B, M; theo D10)

- Theo quyết định D10: hoặc catalog hoá các action Engine còn thiếu (case lifecycle) trong manifest Engine và resource context, hoặc thêm transition grants vào snapshot.

### 4.11 Phase 7: Triển khai và tài liệu (liên tục)

- Helm chart cho authz-server và authz-app: image release build với `--locked` và pin digest, job migration, secret qua CSI, mTLS, probe.
- CI build và publish image Authz.
- Dependabot cho Cargo.lock của Authz, gomod và Docker.
- Cập nhật `docs/kafka-topology.md` (topic bundle và ACK), `docs/architecture/traceability-matrix.md` (bằng chứng CI thật), `docs/query-pdp.md`, `contracts/baseline/README.md`, `db/README.md`.

## 5. Backlog tổng hợp

| ID | Hạng mục | Loại | Phụ thuộc | Size | Lane |
|---|---|---|---|---|---|
| WP0.1 | CI cho Authz workspace | Hạ tầng | – | S–M | A |
| WP0.2 | Test/clippy cho crate root liên quan authz | Hạ tầng | – | S | A |
| WP0.3 | Go CI cho module còn thiếu | Hạ tầng | – | S | A |
| WP0.4 | buf lint, breaking, kiểm drift | Hạ tầng | – | S | A |
| WP0.5 | Characterization test authz-server | Test | WP0.1 | M–L | B |
| WP0.6 | Golden vector Rust/Go | Test | WP0.3 | M | C |
| WP0.7 | PR template, quy ước merge | Quy trình | – | XS | A |
| WP1.1 | Guard dùng chung | Cơ học | WP0.5 | S–M | B |
| WP1.2 | Đóng route đang mở | Hành vi (audit → enforce) | WP1.1 | M | B |
| WP1.3 | Workload registry, ủy quyền thay actor | Hành vi | WP1.1, D9 | M–L | B |
| WP1.4 | Siết xác thực JWT | Hành vi | WP0.5 | M | B |
| WP1.5 | Gỡ đường fail-open (gồm notification gRPC) | Hành vi | – | S | B, C |
| WP1.6 | Gateway lấy capability từ proof | Hành vi | – | S | D |
| WP2.1 | Tách `catalog.rs` | Cơ học | WP0.5 | L | B |
| WP2.2 | Helper dùng chung | Cơ học | WP0.5 | M | B |
| WP2.3 | Khung outbox và worker | Hành vi vận hành | WP0.5 | L | B |
| WP2.4 | Config có kiểu | Cơ học | – | S | B |
| WP2.5 | Health, metrics | Hạ tầng | WP2.4 | M | B |
| WP2.6 | authz-app layering, outbox | Cơ học + hành vi | WP1.4 | M | B |
| WP2.7 | Dynamic User Attributes API & `SignedActorContext` | Tính năng | WP1.1, WP2.2, D12 | M | B |
| WP3.1 | ADR ngữ nghĩa evaluator (gồm attribute scope) | Quyết định | D1, D2, D12, WP0.6 | M | C, B |
| WP3.2 | `go/platform/authz` | Cơ học (code mới) | WP0.6 | L | C |
| WP3.3 | Authoring dùng thư viện | Cơ học → hành vi (shadow) | WP3.1, WP3.2 | M | C |
| WP3.4 | Projection dùng thư viện | Cơ học → hành vi (shadow) | WP3.1, WP3.2 | M–L | C |
| WP3.5 | Các service chỉ kiểm capability | Cơ học | WP3.2 | M | C |
| WP3.6 | OpenAPI cho Authz | Hạ tầng | WP0.4 | M | D |
| WP3.7 | Dynamic Attribute Scope Evaluation (Rust + Go + Projection SQL) | Tính năng | WP2.7, WP3.1, WP3.2, D12 | L | B, C |
| WP4.1 | Route registry ở Gateway | Cơ học (shadow) | WP3.5, WP3.6 | L | D |
| WP4.2 | Workload registry dùng chung | Cơ học | WP1.3 | M | D, B |
| WP4.3 | Helm hook đăng ký manifest | Hạ tầng | WP1.3 | M | A, D |
| WP4.4 | Hoàn thiện API RBAC | Tính năng | WP1.2, D8 | M | B |
| WP4.5 | Công cụ bootstrap tenant | Tính năng | WP2.7, WP3.6, WP4.4, D11 | M–L | D |
| WP4.6 | Bảo vệ publish | Hành vi | WP2.1 | S–M | B |
| WP4.7 | Danh sách consumer bắt buộc | Tính năng | WP2.3 | M | B |
| WP4.8 | Tài liệu onboard, service mẫu | Tài liệu | WP3.2, WP3.7, WP4.1 | S | D |
| WP5.1 | authz-app thôi dùng PDP legacy | Hành vi | D3, WP1.6 | M | B |
| WP5.2 | Tắt rồi xoá route legacy | Xoá code | WP5.1 | M | B |
| WP5.3 | Xoá bảng legacy (giữ `user_attribute_history`) | DB contract | WP5.2 | M | B |
| WP5.4 | Dọn artifact | Xoá code | – | S | B |
| WP5.5 | Nâng phụ thuộc, gộp workspace | Hạ tầng | WP5.2, D7 | L | B |
| WP6.1 | Bỏ SQL chéo schema | Hành vi | WP2.1, D5 | M | B |
| WP6.2 | Tách role DB và ledger | Hạ tầng | WP6.1 | M | B |
| WP6.3 | RLS có hiệu lực | Hành vi | WP6.2 | M | B |
| WP6.4 | Quyền sở hữu migration | Hạ tầng | D6 | S | B |
| WP6.5 | Revoke phía ghi (role & attributes) | Tính năng | D4, WP2.7, WP3.2 | L | B, C |
| WP6.6 | Transition grants của Engine | Hành vi | D10 | M | B |
| WP7.x | Helm, image, Dependabot, tài liệu | Hạ tầng | – | Liên tục | A |

## 6. Cơ chế chống tắc nghẽn

### 6.1 Cờ tính năng

| Cờ | Nơi dùng | Giá trị | Dùng cho | Gỡ khi |
|---|---|---|---|---|
| `AUTHZ_ROUTE_GUARD_MODE` | authz-server | `audit` / `enforce` | WP1.2 | Đã enforce ổn định 1 release |
| `AUTHZ_ACTOR_CAPABILITY_SOURCE` | authz-server | `claim` / `audit` / `db` | WP1.3 | Đã dùng `db` ổn định 1 release |
| `AUTHZ_WORKLOAD_REGISTRY_FILE` | authz-server, Gateway | đường dẫn file | WP1.3, WP4.2 | Gỡ biến cũ khi mọi môi trường đã dùng file |
| `AUTHZ_ACTOR_ATTRIBUTES_EMBED_ENABLED` | authz-server | `true` / `false` | WP2.7 | Bật mặc định ổn định 1 release |
| `AUTHZ_ATTRIBUTE_SCOPE_MODE` | authoring, projection, engine | `off` / `shadow` / `enforce` | WP3.7 | Đã enforce ổn định 1 release |
| `AUTHZ_LEGACY_PDP_ENABLED` | authz-server | `true` / `false` | WP5.2 | Code legacy đã bị xoá |
| `AUTHZ_RUN_MIGRATIONS` | authz-server, authz-app | `true` / `false` | WP6.4 | Giữ lâu dài, prod đặt `false` |
| `AUTHZ_EVAL_MODE` | authoring, projection | `legacy` / `shadow` / `new` | WP3.3, WP3.4 | Code cũ đã bị xoá |
| `GATEWAY_ROUTE_REGISTRY_MODE` | api-gateway | `off` / `shadow` / `enforce` | WP4.1 | Mọi nhóm route đã chuyển |

### 6.2 Quy tắc làm việc

- **Thiếu quyết định thì làm phần cơ học trước.** Ví dụ: WP3.2 viết thư viện theo ngữ nghĩa Rust hiện tại, các điểm còn tranh luận để dạng option. Đến khi D1 chốt thì chỉ đổi giá trị mặc định.
- **PR nhỏ, một mục đích.** Gợi ý không quá khoảng 400 dòng diff (không tính test). Mỗi PR có test cho phần thay đổi.
- **Thứ tự merge ở vùng hay đụng nhau:** `catalog.rs` thuộc WP2.1 cho tới khi tách xong; `apps/go/api-gateway/internal/gateway/handler.go` thuộc WP4.1 trong lúc chuyển route.
- **Thay đổi hợp đồng** (proto, OpenAPI, bundle, workload registry) cần thêm một reviewer từ lane khác.
- **Mọi thay đổi hành vi bảo mật** đều có chế độ audit hoặc shadow, metric đi kèm và ngưỡng rõ ràng để chuyển sang enforce.
- **Expand/contract:** thêm cột, field hoặc route mới → chuyển caller → theo dõi metric → gỡ cái cũ sau ít nhất 1 release.

## 7. Rủi ro và cách giảm thiểu

| Rủi ro | Ảnh hưởng | Giảm thiểu |
|---|---|---|
| Bật guard làm hỏng caller chưa được biết tới | Lỗi 403 ở production | Chế độ audit, metric theo route, kiểm kê caller trước WP1.2 |
| Đổi ngữ nghĩa evaluator làm thay đổi ai được truy cập | Lộ dữ liệu hoặc chặn nhầm | Golden vector, chạy shadow, Security review ADR-014 |
| `SignedActorContext` phình to do user có quá nhiều attributes | Vượt giới hạn header HTTP/gRPC, tăng latency ký/verify | Allowlist theo `tenant_attribute_definition`, giới hạn cứng số key/value/bytes, chỉ nhúng attribute active |
| Attribute predicate trên Projection không có index phù hợp | Query chậm khi lọc trên JSONB | Owner manifest chỉ allowlist các thuộc tính có cột hoặc GIN/expression index đã kiểm chứng |
| Tách `catalog.rs` làm đổi thứ tự validate hoặc mã lỗi | Access UI hiển thị sai | Characterization test và snapshot response (WP0.5) |
| Refactor outbox làm mất hoặc gửi trùng message | Bundle không tới consumer, ACK sai | Giữ semantics at-least-once, consumer cài idempotent, test tích hợp Kafka, dùng mẫu `query_revoke_publisher` |
| Consumer ID ổn định làm thay đổi dữ liệu ACK tracking | Dashboard rollout bị nhiễu | Thông báo trước, đối chiếu bằng danh sách consumer bắt buộc (WP4.7) |
| Nâng axum/sqlx gây regression | Lỗi runtime | Làm sau khi gỡ legacy (ít code hơn), qualification theo ADR-006 |
| Xoá bảng legacy làm mất dữ liệu audit | Mất bằng chứng tuân thủ | Export/backup, thống nhất retention, chỉ xoá sau 2 release; giữ `user_attribute_history` |
| CI lần đầu đỏ hàng loạt | Chặn mọi PR | Để job không bắt buộc trong 1 tuần, sửa xong mới chuyển required |
| Engine chặn case lifecycle sau khi publish catalog (O7) | Gián đoạn nghiệp vụ Case | D10, cảnh báo ở bước validate (WP4.6) |
| Không ai chốt D1–D12 | Trễ toàn kế hoạch | Có hạn chót và phương án mặc định |

## 8. Definition of Done

### 8.1 Áp dụng cho mọi WP

- Có test cho hành vi mới hoặc hành vi đã đổi; test cũ vẫn xanh; các CI check bắt buộc đều xanh.
- Fail-closed được chứng minh bằng test âm: thiếu capability, sai tenant, proof hết hạn, bundle lỗi, thiếu attribute bắt buộc.
- Tài liệu liên quan (README, ADR, `docs/`) được cập nhật trong cùng PR.
- Có cách rollback: cờ tính năng hoặc revert an toàn. Migration chỉ đi tiến và chỉ thêm (additive).
- Đường code mới có metric và log; không log token, proof hay key.

### 8.2 Checklist PR

- [ ] Thay đổi cơ học hay thay đổi hành vi? Nếu là hành vi thì cờ nào điều khiển?
- [ ] Đã thêm test gì? Có chạy cả golden vector không, nếu PR đụng tới evaluator?
- [ ] Có đổi hợp đồng (proto, OpenAPI, bundle, registry) không? Nếu có, `buf breaking` hoặc breaking check của OpenAPI có xanh không?
- [ ] Có migration không? Đã theo expand/contract chưa?
- [ ] Cách rollback là gì?

## 9. Việc bắt đầu ngay trong tuần đầu

| Lane | Việc |
|---|---|
| A | WP0.1, WP0.2, WP0.3 (đồng bộ Notification `PR0`), WP0.4 |
| B | WP0.5 cho catalog, rbac và tenant; phần authz-server của WP1.5 (`FAIL_MODE=OPEN`) |
| C | WP0.6; phần notification-service của WP1.5 (sửa nhỏ, mức P0, chốt trước Notification `PR3`) |
| D | Kiểm kê mọi caller của route admin Authz (đầu vào cho WP1.2); lập bảng route Gateway hiện tại (đầu vào cho WP4.1) |
| Chung | Gửi decision log D1–D12 cho người chịu trách nhiệm, hạn 1 tuần sau khi Phase 0 xong |

## 10. Lịch sử thay đổi

| Ngày | Phiên bản | Nội dung |
|---|---|---|
| 2026-11-26 | Draft v1 | Bản đầu tiên, dựa trên phân tích mã nguồn ở mục 1 |
| 2026-11-27 | Draft v2 | Tích hợp Dynamic User Attributes (`key:value`) & Dynamic Attribute Scope cho đa workflow (mục 2.8, D12, WP2.7, WP3.7); giữ `user_attribute_history`; đồng bộ điểm giao với `docs/notification-refactor-plan.md` |
