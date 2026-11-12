# BPMP Authorization Control Plane

This deployable owns PostgreSQL-backed authorization administration, policy
lifecycle, audit, publication, and rollback. It does not make authoritative
workflow transition decisions.

Runtime boundaries:

- `bpmp-authz-contracts`: actor proof and signed policy bundle contracts.
- `bpmp-authz-engine`: pure deterministic evaluator embedded in `bpmp-engine`.
- `bpmp-adapter-policy-bundle`: verified signed bundle and revoke-epoch cache.
- `bpmp-adapter-identity-jwt`: JWT verification against injected JWKS snapshots.

The current HTTP router exposes protected `/authz/v1/check`, `/filter`, `/query`,
`/query-revoke-feed`, and `/explain` routes as well as `/admin/v1` and health
routes. These HTTP decision/filter routes are not required to become synchronous
runtime dependencies for every service. The target direction is policy
administration and signed bundle publication in this control plane, embedded
evaluation for Engine transitions, and owner-local PEP/evaluation for data
queries. Migrate consumers before retiring or narrowing the current routes.

## Dynamic Tenant Registry

The authenticated PostgreSQL-backed tenant API exposes:

- `POST /admin/v1/tenants`
- `GET /admin/v1/tenants?after_code=<code>&limit=<1..200>`
- `GET /admin/v1/tenants/:id`
- `PUT /admin/v1/tenants/:id`
- `PUT /admin/v1/tenants/:id/status`
- `DELETE /admin/v1/tenants/:id?expected_version=<version>`

Create accepts a stable tenant code, display name, and typed configuration.
Updates and deletes require `expected_version` and use a short `FOR UPDATE`
transaction. Delete is soft-delete and tenant codes are never reused. Every
mutation records the verified service subject, request ID, previous/current
value, and committed version in immutable `tenant_audit_log` atomically with
the tenant row.

## Authorization Catalog and signed publication

The catalog snapshot store and first administrative operations are added under
`/admin/v1/catalog/versions`:

- `GET /admin/v1/catalog/versions?tenant_id=<uuid>` lists the latest 100 versions.
- `POST /admin/v1/catalog/versions` creates a `DRAFT` snapshot and audit record.
  The body contains `tenant_id`, `reason`, and `snapshot.entries`; each entry must identify
  `owner_id`, `resource_type`, `action`, and `capability`.
- `PUT /admin/v1/catalog/versions/:id` updates only a `DRAFT`. Send the current
  `draft_revision` as `expected_draft_revision`, with a `reason`; successful
  writes increment it, and stale writes return a version conflict. Each edit is audited.
- `GET /admin/v1/catalog/audit?tenant_id=<uuid>&limit=<1..200>` returns the
  latest catalog lifecycle changes for that tenant, including actor, reason,
  request ID, action, and version.

The API also provides `GET /admin/v1/catalog/entries`, owner-only
`POST /admin/v1/catalog/owner-manifests`, version `POST .../:id/validate`,
`POST .../:id/publish`, and `GET .../:id/bundle`. The tenant-bound principal
must carry the specific `authz.catalog.*` capability. Configure
`AUTHZ_POLICY_SIGNING_KEY_ID` and `AUTHZ_POLICY_SIGNING_KEY_PATH` together;
the signing seed must be exactly 32 bytes. Catalog publication requires a
reason, atomically stores the signed bundle and an outbox record, then the
publisher retries delivery to `AUTHZ_POLICY_BUNDLE_TOPIC`. Duplicate Kafka
delivery is expected and evaluator installation is idempotent.
Owner registration is bound to the JWT signing key as well as `sub`: configure
`AUTHZ_OWNER_MANIFEST_KEY_IDS` as a JSON map from owner ID to the owner’s
allowed JWK `kid` values. The map rejects key IDs shared by multiple owners.
Keep each owner’s public key in the configured JWKS and its private key in that
owner’s workload secret; a trusted gateway key alone cannot register another
service’s manifest.
The first catalog publication uses bundle sequence 2 because sequence 1 is
reserved for the legacy bootstrap policy artifact during the migration.

## Organization branch scope resolver

`GET /api/v1/branch-scopes/:branch_id` remains the user-facing lookup and
requires `organization:read`. Engine must use the separate
`GET /internal/v1/branch-scopes/:branch_id` route. The internal route does not
accept a browser JWT or a tenant supplied by the caller: it derives tenant from
`x-bpmp-workload-proof`, verifies the signed workload ID, command ID, signature,
and bounded proof lifetime, then checks that the branch is active in that
tenant. It returns the canonical root-to-branch ancestry and organization
revision. Requests fail closed when `BRANCH_SCOPE_WORKLOAD_KEYS_FILE` is not
configured.

Projection uses `POST /internal/v1/branch-scopes/descendants` with up to 100
unique branch roots. Authz returns each active root's current branch IDs in the active subtree and organization revision in one tenant-scoped operation. The same
signed workload proof authenticates this route; the body cannot select a tenant
or provide ancestry. The endpoint bounds the total descendant result, and
Projection fails closed on missing roots, inactive branches, invalid proof,
oversized results, or resolver errors. Projection filters rows by the
authoritative `resource_branch_id` against this current set, so hierarchy
reparenting changes query visibility without trusting a stored ancestry snapshot.

The `authz-app` organization owner exposes the internal resolver on a dedicated
mTLS HTTPS listener. Configure its `HTTPS_PORT`, certificate, private key, and
client CA together; when `BRANCH_SCOPE_WORKLOAD_KEYS_FILE` is configured the
listener is required. Keep the HTTP listener for the authenticated Cockpit/BFF
path. Projection signs a short-lived, command-bound workload proof for each
descendant lookup from its trusted tenant context; it does not forward
browser-selected ancestry or rely on the API Gateway identity.

```json
"authorization_branch_scope_resolver": {
  "base_url": "https://authz-app.internal.example:9443",
  "ca_path": "/run/secrets/authz-app-ca.pem",
  "timeout_ms": 1500,
  "workload_id": "projection-service",
  "signing_key_id": "projection-branch-scope-k1",
  "signing_key_path": "/run/secrets/projection-branch-scope-k1.key",
  "proof_ttl_ms": 30000
}
```

The mounted JSON file has this shape (public key files contain exactly 32 raw
Ed25519 public-key bytes):

```json
{
  "workloads": [
    {
      "workload_id": "projection-service",
      "keys": [
        { "key_id": "projection-branch-scope-k1", "public_key_path": "/run/secrets/projection-branch-scope-k1.pub" }
      ]
    },
    {
      "workload_id": "bpmp-engine",
      "keys": [
        { "key_id": "bpmp-engine-workload-k1", "public_key_path": "/run/secrets/bpmp-engine-workload-k1.pub" }
      ]
    }
  ],
  "max_proof_bytes": 4096,
  "max_proof_ttl_ms": 30000,
  "clock_skew_ms": 5000
}
```

Each `workload_id` must match the identity that signs its proofs. Projection signs descendant queries; Engine signs internal branch-scope lookups for boundary/local/remote worker dispatch. The Engine entry must match `authorization.internal_dispatch.workload_id` and its public key.
Keep the internal route on the dedicated mTLS listener; the signed proof is the application-level identity and command binding. Engine
resolves the branch from the workflow-configured variable before authorization
and commit, then persists the verified branch ID, canonical ancestry, and
organization revision in the start event and snapshot. Projection stores those
fields and applies branch grants in its local query evaluator. The Engine's encrypted authorization audit records the resolved branch scope and organization revision. Stored event/snapshot ancestry is historical. Projection resolves the current branch subtree per query; Engine authoritative commands and boundary/local/remote worker dispatch resolve it before resource authorization. Each path fails closed on resolver failure instead of falling back to the snapshot.

Engine runtime must configure `branch_scope_resolver` with this Authz server's HTTPS base URL and a bounded timeout. The resolver uses Engine's configured client certificate/key and `tls.client_ca` for mTLS/server verification; the workload signature remains a separate application-level command identity. Its command handler verifies actor and
workload proofs before loading an existing instance, resolves the current branch
path and supplies that path to the local resource evaluator. Boundary/local/remote worker dispatch also loads the instance branch, resolves its current ancestry using an Engine-signed command-bound proof, and passes that result to Engine. Missing resolver or inactive branch fails the command closed.

Engine runtime configuration example:

```json
"branch_scope_resolver": {
  "endpoint": "https://authz.internal.example",
  "timeout_ms": 1500
}
```

Engine runtime configuration must trust the matching public key in
`authorization.policy_keys`, subscribe to the same policy bundle topic, and
use a distinct consumer group for each Engine node so every node sees every
publication. A publication is effective at an Engine node only after local
signature verification and installation, before committing Kafka offsets. Authz
validates each acknowledgement against its published bundle hash and exposes
recorded consumers through `GET /admin/v1/catalog/acknowledgements`. This is
operational evidence from Kafka-authorized workloads, not cryptographic attestation
of process memory. Query-service PEP rollout and an expected-consumer inventory
remain separate integration gates.
