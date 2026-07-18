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
