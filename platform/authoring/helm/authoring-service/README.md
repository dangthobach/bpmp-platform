# Authoring Service Helm chart

The chart deploys the Authoring gRPC service and its durable runtime-deployment
worker as one horizontally scalable workload. The Service is ClusterIP only;
the API Gateway is expected to call its mTLS gRPC listener.

## Runtime Secret contract

Create the Secret named by `runtimeConfig.secretName` before installing the
chart. It must contain `config.json` (or `runtimeConfig.fileName`) and every
file referenced by that JSON: server certificate/private key and client CA,
Compiler and Engine trust/client certificates, Kafka CA/client certificate/key,
actor JWKS/internal proof public keys, and authorization bundle signing public
keys. Use a secret manager/CSI or external-secrets controller to materialize it;
do not put key material in Helm values. The configuration must set
`listen_address` to `0.0.0.0:<service.grpcPort>`, `health_address` to
`0.0.0.0:<service.healthPort>`, `migration_path` to
`runtimeConfig.migrationPath`, and `owner_manifest_path` to
`runtimeConfig.ownerManifestPath`. Database, Kafka, Compiler, Engine addresses
and TLS server names must match the target environment.

Increment `runtimeConfig.revision` whenever the referenced Secret changes so
Kubernetes performs a rollout. Secret contents are mounted read-only. Migrations
are shipped in the image. Keep `apply_migration` false in the service runtime
config and enable the chart's `migration.enabled` pre-install/pre-upgrade hook
for a controlled migration-only Job. Create the separate Secret named by
`migration.secretName`; its `migration.fileName` JSON has only `postgres_dsn`
and `migration_path` fields. Give the migration role only the database rights
required by the versioned migrations. This Job applies migrations under the
existing PostgreSQL advisory lock and exits before loading service credentials
or starting Kafka, Compiler, Engine or request-serving workers.

## Network policy and rollout

The chart creates a mTLS gRPC ClusterIP Service, a dependency-aware readiness
probe, a process liveness probe, two replicas by default, topology spreading and
a disruption budget. Production values must enable `networkPolicy` and provide
narrow API Gateway ingress plus PostgreSQL, Kafka, Compiler, Engine and DNS
egress rules for the service, plus PostgreSQL and DNS egress for the migration
Job. The chart does not create credentials, databases, Kafka topics,
certificates, or an ingress route. Pin `image.digest` to a verified OCI manifest
digest; mutable tags fail schema validation.

The runtime worker participates in the same deployment and scales with service
replicas. PostgreSQL lease/`SKIP LOCKED` ownership prevents two workers from
owning one live job; lease reclaim is the recovery mechanism after pod loss.
