# BPMP Authorization Control Plane

This deployable owns PostgreSQL-backed authorization administration, policy
lifecycle, audit, publication, and rollback. It does not make authoritative
workflow transition decisions.

Runtime boundaries:

- `bpmp-authz-contracts`: actor proof and signed policy bundle contracts.
- `bpmp-authz-engine`: pure deterministic evaluator embedded in `bpmp-engine`.
- `bpmp-adapter-policy-bundle`: verified signed bundle and revoke-epoch cache.
- `bpmp-adapter-identity-jwt`: JWT verification against injected JWKS snapshots.

Only `/admin/v1` and health routes are exposed by this control-plane server.
Legacy decision/filter modules remain temporarily for migration but are not
routed or started as a gRPC decision service.

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
