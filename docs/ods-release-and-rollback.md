# ODS release and rollback runbook

## Promotion gates

1. Build Shell and each remote independently. Publish only content-addressed container digests and retain provenance attestations.
2. Serve remote manifests from a release-specific URL. Never replace bytes at a URL referenced by a signed catalog.
3. Prepare `shell-config.json` and `mfe-catalog.json`; sign the catalog outside the repository with the active Ed25519 key.
4. Run `node tools/verify-ods-catalog.mjs --config shell-config.json --catalog mfe-catalog.json` immediately before promotion.
5. Run `helm lint` and `helm template` with image digests and the exact verified JSON values.
6. Use `helm upgrade --install --atomic --wait`. Keep the previous chart revision and signed catalog available.
7. Smoke-test Shell bootstrap, Task remote loading, session/CSRF and trace/correlation propagation before widening tenant capability rollout.

The chart refuses tag-only images, fewer than two replicas, missing runtime JSON and unsigned catalog envelopes. Runtime configuration is mounted separately, so catalog promotion does not rebuild Shell.

## Local Docker Desktop release rehearsal

Use this gate when no cloud/staging cluster is available. It verifies signed-catalog promotion, digest-pinned Helm rollout, readiness failure isolation and rollback on the local Kubernetes cluster. Record it as **local release rehearsal**, never as authorized staging or production evidence. Passing it unblocks the prototype's Phase 1 implementation flow; external environment promotion remains a separate deployment gate and must not block local Phase 2 development.

1. Confirm `kubectl config current-context` is `docker-desktop`, the Docker Desktop node is Ready, and the target namespace/release are dedicated to this rehearsal. Do not change Docker contexts or delete unrelated namespaces/releases.
2. Build Shell and Task images and record their OCI manifest digests. Use the immutable digests in Helm values; do not use tags or Docker config IDs as image digests. Ensure the images are available to the Docker Desktop Kubernetes runtime.
3. Generate a short-lived Ed25519 key outside the repository. Sign a fresh local catalog and place only its public JWK in the Shell config. Use a loopback remote origin reachable by the browser and listed exactly in `allowedRemoteOrigins`. Keep the private key outside the repo and delete it after the rehearsal.
4. Run the catalog verifier with the generated config/catalog, then `helm lint` and `helm template` using the exact signed JSON and image digests.
5. Install/upgrade the chart in a dedicated namespace with `helm upgrade --install --atomic --wait`. Capture revision, Ready replicas, `/`, `/mfe-catalog.json`, remote manifest, browser route load and trace/correlation smoke evidence.
6. In a separate canary release in the same local cluster, apply a controlled invalid readiness revision. Confirm the known-good release remains healthy, record readiness failure, and run `helm rollback` on the canary. Verify restored revision, image digests, catalog version and smoke checks.
7. Store the evidence bundle under an ignored local artifacts directory. Include commit, CI run, OCI manifest digests, catalog version/key ID, verifier output, Helm revisions, namespace/context, readiness and smoke results. Do not include private keys or credentials.

Example signing commands in PowerShell (run from the repository root; keep `$evidence` outside the repository):

```powershell
$evidence = Join-Path $env:LOCALAPPDATA 'bpmp\ods-local-rehearsal'
New-Item -ItemType Directory -Force $evidence | Out-Null
openssl genpkey -algorithm ED25519 -out (Join-Path $evidence 'catalog-private.pem')
$commit = git rev-parse --short HEAD
node tools/sign-ods-catalog.mjs `
  --config platform/ods/helm/ods-foundation/ci/shell-config.json `
  --catalog platform/ods/helm/ods-foundation/ci/mfe-catalog.json `
  --key (Join-Path $evidence 'catalog-private.pem') `
  --key-id docker-desktop-local `
  --remote-origin http://127.0.0.1:4302 `
  --out-config (Join-Path $evidence 'shell-config.json') `
  --out-catalog (Join-Path $evidence 'mfe-catalog.json') `
  --version "local-rehearsal-$commit" `
  --lifetime-hours 24
node tools/verify-ods-catalog.mjs `
  --config (Join-Path $evidence 'shell-config.json') `
  --catalog (Join-Path $evidence 'mfe-catalog.json')
```

Use `helm lint`/`helm template` with `--set-file "runtime.shellConfigJSON=$($evidence.Replace('\','/'))/shell-config.json"` and the equivalent catalog path. Supply real local OCI manifest digests through a temporary values file; the checked-in CI values contain example digests and are not deployable. Remove the private key after capturing the key ID and public catalog evidence.

Docker Desktop is a single-node local Kubernetes environment. This rehearsal does not prove multi-node/AZ availability, a real registry promotion, cloud secret-manager integration, production identity, or environment-owner approval. Those checks remain deployment acceptance criteria when an approved target exists.

## Authorized staging rehearsal

Run this deployment gate only when an environment owner has approved the target cluster, namespace, release, maintenance window and controlled readiness-failure test. It is independent from the local Phase 1 release gate above; lack of a cloud account does not block prototype implementation.

1. Record repository commit, hosted CI run, Shell/Task OCI manifest digests, provenance/attestation references, signed catalog version/key ID and verifier output. Never copy signing private keys into the evidence bundle.
2. Record cluster context, namespace, Helm release, environment owner/approver and change window. Verify the target is staging and the release is isolated from production traffic.
3. Promote the known-good signed catalog and digest-pinned images using the gates above. Capture Helm revision, rollout readiness, `/` and `/mf-manifest.json` responses, Task route load, session/CSRF checks, and browser→Gateway trace/correlation evidence.
4. In a dedicated canary release or namespace, apply a controlled invalid readiness revision using the same immutable image/catalog delivery path. Do not break the healthy staging release to create the failure. Capture the failed revision, readiness events and proof that the previous digest remains available.
5. Verify automatic atomic recovery, then explicitly restore the recorded known-good Helm revision/catalog and wait for readiness. Confirm the catalog annotation, image digests, remote manifest and browser trace all match the known-good revision.
6. Attach evidence to the approved change record and record pass/fail for each check. A failed or unrecorded check leaves the staging gate open.

| Evidence field | Required value |
|---|---|
| Change / approval | Change ID, environment owner, approver, approved window |
| Source and supply chain | Commit SHA, CI run URL/ID, image manifest digests, attestation references |
| Signed catalog | Catalog version, signing key ID, verifier output, previous known-good version |
| Staging target | Cluster context, namespace, release, date/time, operator |
| Healthy promotion | Helm revision, Ready replicas, Shell/remote smoke results, session/CSRF and trace evidence |
| Controlled failure | Canary release/namespace, failing revision/digest, readiness events, prior revision availability |
| Recovery | Atomic rollback result, explicit restored revision/catalog, final digest/annotation and smoke evidence |
| Gate decision | Per-check result, exceptions, approver sign-off |

## Rollback

If a remote fails its error/SLO gate, first promote the previous still-valid signed catalog. This removes the remote blast radius without rebuilding Shell. If the Shell or workload deployment is unhealthy, run:

```text
helm history <release> -n <namespace>
helm rollback <release> <known-good-revision> -n <namespace> --wait
```

Confirm the ConfigMap annotation `bpmp.dev/catalog-version`, deployment image digests and browser trace after rollback. Never roll back or delete committed workflow/domain events; this runbook changes only presentation deployment and routing metadata.

## Authoring Service release

Authoring is a separate backend release from the ODS Shell/Task chart. The
service image is digest-pinned and the chart is in
`platform/authoring/helm/authoring-service`; passing ODS frontend promotion does
not prove that Authoring is running or that a runtime workflow deployment works.

The ODS Foundation workflow packages the chart after Authoring quality gates
and container image builds. Non-release runs retain a versioned `.tgz` artifact
for 30 days. An `ods-vX.Y.Z` tag sets chart/app version to `X.Y.Z` and publishes
the chart to `oci://ghcr.io/<owner>/<repo>/charts`. Before installing, supply the
environment's image repository and verified manifest digest; the chart has no
deployable image default. Registry publication is automated, while deployment
values, runtime and migration Secrets, and staging promotion remain environment
owner responsibilities.

1. Use the image digest built by the Authoring quality-gated CI job and run the
   chart lint/render gates. Keep the database schema compatible with the prior
   service while the rollout is in progress.
2. Materialize the `runtimeConfig.secretName` Secret from the approved secret
   manager. Its `config.json` must align `listen_address`/`health_address` with
   chart ports and point migrations/owner manifest at the image paths. Point
   the Gateway's Authoring mTLS client to the chart's ClusterIP Service.
3. If applying migrations, create the separate minimal `migration.secretName`
   Secret containing only `postgres_dsn` and `migration_path`, then enable the
   chart migration hook. Confirm the hook Job succeeds before the Deployment
   rollout. A failed hook aborts the upgrade; use a reviewed forward-fix for
   additive schema changes rather than assuming an application rollback rolls
   back PostgreSQL.
4. Enable NetworkPolicy with explicit API Gateway ingress and PostgreSQL,
   Kafka, Compiler, Engine and DNS egress rules. Never put DSNs or key material
   in Helm values or the release evidence bundle. Increment
   `runtimeConfig.revision` when the external runtime Secret changes.
5. Verify two Ready replicas, mTLS Gateway-to-Authoring gRPC, dependency-aware
   `/health/ready`, deployment create/list/detail/rollback APIs, compiler
   artifact staging, Engine safe-point activation and persisted reconciliation.
   In a dedicated environment, exercise worker termination during ambiguous
   Engine commit, Authoring restart/lease reclaim, Engine restart or leader
   change during partial ACK, and rollback recovery; capture PostgreSQL and
   Engine receipts by deployment ID.
6. Retain previous image digest, chart version and runtime config revision. If
   the rollout is unhealthy, restore the previous application/chart/config
   revision only after confirming the schema remains backward-compatible; do
   not erase deployment history or Engine intents.

The migration-only config parser and chart hook rendering are covered by local
Go and Helm gates. A real database migration run, Authoring/Compiler/Engine
staging run, production secret-manager integration, registry promotion and
restart/failover evidence remain separate release gates.

## Stop conditions

- Signature, key ID, expiry, allowlist or catalog lifetime verification fails.
- Any image is referenced by a mutable tag rather than `sha256` digest.
- Readiness does not converge within the deployment timeout.
- Trace/correlation IDs are lost between browser, Portal Gateway and downstream gRPC calls.
- Error rate, remote-load latency or session failures breach the release gate.
