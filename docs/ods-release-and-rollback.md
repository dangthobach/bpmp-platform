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
