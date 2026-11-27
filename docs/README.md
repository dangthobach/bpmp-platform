# Documentation Index

The architecture baseline remains at the repository root for stable links used
by engineering tools:

- `../requirements.md`
- `../design.md`
- `../microservices-architecture.md`

Architecture decisions are in `docs/adr/`. Operational and contribution
documents are linked from the root (`SECURITY.md`, `CONTRIBUTING.md`, and
`CODE_OF_CONDUCT.md`). New topic-specific documentation should be added under
`docs/`; moving the baseline files requires a separate link-migration change.

## Architecture navigation

- [Architecture map and document status](architecture/README.md)
- [Service workflows](architecture/service-workflows.md)
- [Identity and trust flows](architecture/identity-and-trust-flows.md)
- [Background processing catalog](architecture/background-processing-catalog.md)
- [Architecture traceability matrix](architecture/traceability-matrix.md)

## Runtime and operations

- [API integration](api-integration.md)
- [API and observability contract](api-and-observability-contract.md)
- [Kafka topology](kafka-topology.md)
- [Dynamic configuration P1](dynamic-configuration-p1.md)
- [Boundary runtime adapter](boundary-runtime-adapter.md)
- [Projection rebuild](projection-rebuild.md)
- [Build and deploy](build-and-deploy.md)
- [Production deployment process](production-deployment-process.md)
- [RocksDB operations](rocksdb-operations.md)
- [Vault/KMS operations](vault-kms-operations.md)
- [Dependency management](dependency-management.md)
- [OIDC PKCE and browser session contract](contracts/api-gateway-oidc-pkce-session.md)

## Current implementation gaps and evidence

- [Repository gap remediation](repository-gap-remediation.md) — current code evidence, P0 blockers, and release gates.
- [BPMN element coverage](bpmn-element-coverage.md) — compiler support versus end-to-end runtime behavior.
- [Requirement 1 compliance](requirement-1-compliance.md) — executable evidence with its verification date and remaining profile gaps.
- [300k CCU readiness review](300k-ccu-readiness.md) — capacity risks and dated implementation corrections; not a capacity certification.

## Query and authorization

- [Authz and multi-tenant refactor plan](authz-refactor-plan.md) — Draft v2, including Dynamic User Attributes and cross-plan alignment with Notification.
- [Notification service refactor plan](notification-refactor-plan.md) — proposed PR roadmap aligned with Authz.
- [Typed query PDP and revocation](query-pdp.md)
- [Query authorization progress](query-authorization-progress.md)
- [Signed query revoke Kafka feed](query-revoke-kafka.md)
- [Query API and exports](query-api.md)

## ODS refactor

- [ODS plan](ods-microfrontend-microservices-refactor-plan.md) — target/mixed
- [ODS current progress](ods-refactor-progress.md) — current evidence snapshot
- [Prototype porting plan](ods-prototype-porting-plan.md) — implementation plan; current phase status is maintained in the progress ledger.
- [ODS browser session contract](ods-browser-session-contract.md)
- [ODS release and rollback](ods-release-and-rollback.md)
- [ODS operations and on-call runbook](ods-operations-runbook.md)
- [ODS cutover, DR drills and decommission](ods-cutover-dr-and-decommission.md)

## Prototype-driven architecture proposal

- [BPMP prototype audit, target architecture, ERD and component system](refactor-prototype/README.md) — proposed; source inventory and logical design, not delivered runtime functionality.

- [MDM Service: centralized administration and versioned local reads](refactor-prototype/appendix/mdm-service.md) — proposed; configurable models, publication bundles, batch resolution and deterministic replay evidence.

- [Notification Service: versioned templates, multi-channel routing and delivery runtime](refactor-prototype/appendix/notification-service.md) — proposed ([ADR-013](adr/ADR-013-notification-service.md)); immutable template releases, safe preview, dynamic provider profiles, circuit breakers and workflow trigger rules.

- [Service–database ownership and Reporting data flows](refactor-prototype/appendix/databases-and-reporting.md) — current storage inventory plus proposed Report contracts, event sources, large-volume design and recovery gates.

- [Entity audit, soft-delete and optimistic locking conventions](refactor-prototype/appendix/entity-conventions.md).
- [BPMP API Specification and UI action traceability](refactor-prototype/api-spec.md) — current Gateway snapshot plus proposed target API catalog, not a deployed contract.

- [Backend standards: UUID IDs, healthz/metrics and scheduled outbox/log retention](refactor-prototype/appendix/backend-operations.md) — required target behavior; per-service implementation evidence remains a rollout gate.
