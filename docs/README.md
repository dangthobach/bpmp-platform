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

## Query and authorization

- [Typed query PDP and revocation](query-pdp.md)
- [Query authorization progress](query-authorization-progress.md)
- [Signed query revoke Kafka feed](query-revoke-kafka.md)
- [Query API and exports](query-api.md)

## ODS refactor

- [ODS plan](ods-microfrontend-microservices-refactor-plan.md) — target/mixed
- [ODS current progress](ods-refactor-progress.md) — current evidence snapshot
- [ODS browser session contract](ods-browser-session-contract.md)
- [ODS release and rollback](ods-release-and-rollback.md)

## Prototype-driven architecture proposal

- [BPMP prototype audit, target architecture, ERD and component system](refactor-prototype/README.md) — proposed; source inventory and logical design, not delivered runtime functionality.

- [MDM Service: centralized administration and versioned local reads](refactor-prototype/mdm-service.md) — proposed; configurable models, publication bundles, batch resolution and deterministic replay evidence.

- [Service–database ownership and Reporting data flows](refactor-prototype/databases-and-reporting.md) — current storage inventory plus proposed Report contracts, event sources, large-volume design and recovery gates.

- [Entity audit, soft-delete and optimistic locking conventions](refactor-prototype/entity-conventions.md).
- [BPMP API Specification and UI action traceability](refactor-prototype/api-spec.md) — current Gateway snapshot plus proposed target API catalog, not a deployed contract.

- [Backend standards: UUID IDs, healthz/metrics and scheduled outbox/log retention](refactor-prototype/backend-operations.md) — required target behavior; per-service implementation evidence remains a rollout gate.
