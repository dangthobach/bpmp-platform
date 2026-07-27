# BPMP Build and Deploy Runbook

## Supported toolchain

Use the pinned or declared versions from the repository:

- Rust `1.91.1` with `rustfmt` and `clippy`;
- Go `1.25.0`;
- Node.js `22` or newer for Cockpit;
- Buf CLI compatible with `buf.yaml` v2;
- Docker Engine with Compose v2 and BuildKit;
- PostgreSQL 17, Kafka-compatible broker, Redis and an OTLP collector for the
  broker-backed process topology.

Do not delete `target/` during normal development. Cargo and the Docker Rust
builder both reuse compiled dependencies. Remove a target directory only after
confirming a stale native artifact or file lock, and never while an Engine or
test process still owns a RocksDB path. Generated E2E runtime material,
`target/`, web `node_modules/` and `dist/` are ignored by Git and Docker.

## Source gates

Run these gates from the repository root before building release images:

```powershell
buf lint
buf breaking --against ".git#branch=main"
buf generate
cargo fmt --all -- --check
cargo clippy --workspace --all-targets -- -D warnings
cargo test --workspace
```

Run Go checks from every module because this repository uses a Go workspace:

```powershell
Get-ChildItem -Recurse -Filter go.mod |
  ForEach-Object {
    Push-Location $_.DirectoryName
    try {
      go test ./...
      go vet ./...
    } finally {
      Pop-Location
    }
  }
```

Run Cockpit checks separately:

```powershell
Set-Location apps/web/cockpit-web
npm ci
npm run typecheck
npm test
npm run build
```

Use an immutable release tag or the production branch instead of `main` for
the Buf breaking baseline when promoting a release. Generated Protobuf output
must produce no source diff after `buf generate`.

## Local process topology

Docker must be running. The harness generates short-lived certificates, keys,
JWT, signed WIR, authorization bundle, SQL seed data and centralized Kafka
configuration under the ignored `platform/e2e/runtime/` directory:

```powershell
.\platform\e2e\run.ps1
```

The successful probe proves all of the following:

- Configuration Service publishes ordered Protobuf invalidations through its
  transactional outbox.
- All three Engine processes, API Gateway and Human Runtime resolve the
  authoritative snapshot over mTLS and commit Kafka offsets only after cache
  installation.
- A workflow start passes through API Gateway and the Raft authoritative path,
  with an idempotent retry returning the original receipt.
- Human Runtime consumes committed events and creates one PostgreSQL work item.
- The bootstrap leader is stopped; the surviving majority completes the work
  item and projects the completion back to PostgreSQL.

Use `-KeepRunning` only for inspection. Use `-SkipBuild` only when the local
images were built from the current source.

## Bootstrap configuration

Kafka connectivity cannot be configured through Kafka itself. Supply broker
addresses, security protocol, CA/certificate/key paths, client ID, topic,
consumer group, message bounds, poll/session/request timeouts and initial
tenant IDs as validated bootstrap configuration.

Topic and group names follow:

`bpmp.<bounded-context>.<purpose>.v<schema>.<environment>`

`platform/e2e/manifest.json` is the single topology source for E2E. Production
deployments must have an equivalent centrally managed inventory. Do not repeat
topic or group literals in application manifests.

Configuration publication is a broadcast invalidation. Every running process
must use its own stable configuration-reloader consumer group, for example:

`bpmp.engine.configuration-reloader-node-2.v1.production`

Sharing one group across replicas distributes partitions and leaves caches on
non-assigned replicas stale. Each process first resolves all configured tenants
at startup, so a new group does not depend on replaying historical publications.

## Deployment order

1. Publish immutable image digests, Protobuf descriptors, signed WIR and policy
   bundles. Mount credentials and private keys from the secret manager.
2. Provision separate PostgreSQL databases and credentials for Configuration
   Service and Human Runtime. Apply their migrations as one-shot jobs.
3. Provision Kafka topics, ACLs and retention from the centralized topology.
   Verify idempotent producer and manual-commit consumer permissions.
4. Start Configuration Service and wait for both HTTP and mTLS gRPC readiness.
5. Publish complete tenant policies for every deployed owner. `policy_version`
   identifies the compatible authorization policy; use immutable
   `config_version` and ordinal for runtime configuration revisions.
6. Start three or five Engine members with separate volumes, TLS identities and
   configuration-reloader groups. Bootstrap membership once and wait for quorum.
7. Start Human Runtime, then API Gateway. Readiness requires PostgreSQL/Redis,
   upstream gRPC, Kafka and all initial tenant cache entries.
8. Enable ingress only after the acceptance transaction and consumer lag checks
   pass.

Projection Service currently contains a projector library but no production
composition root. Governance currently contains the pure Rust domain crate but
no deployable service. Do not deploy placeholder health-only containers or
claim their runtime cache integration is complete; add their real binaries,
owned stores and lifecycle workers before enabling those owners.

## Rollout and rollback

Publish a new immutable configuration version and wait for every process group
to reach zero lag before increasing traffic. Never mutate a published version.
An invalid event, unavailable resolver, hash mismatch or stale ordinal is
fail-closed: the consumer does not commit the record and the process exits.

Rollback configuration through the Configuration Service rollback lifecycle,
which creates another published version. Roll back stateless images only across
compatible Protobuf and database schemas. Preserve Kafka offsets, PostgreSQL
audit/outbox rows and Engine Raft/RocksDB volumes during recovery.

The complete production-HA promotion gates remain in
`docs/production-deployment-process.md`.
