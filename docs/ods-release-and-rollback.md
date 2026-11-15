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

## Authorized staging rehearsal

Run this gate only in an environment whose owner has approved the target cluster, namespace, release, maintenance window and controlled readiness-failure test. The current local Docker Desktop rehearsal is not a substitute for this gate.

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

## Stop conditions

- Signature, key ID, expiry, allowlist or catalog lifetime verification fails.
- Any image is referenced by a mutable tag rather than `sha256` digest.
- Readiness does not converge within the deployment timeout.
- Trace/correlation IDs are lost between browser, Portal Gateway and downstream gRPC calls.
- Error rate, remote-load latency or session failures breach the release gate.
