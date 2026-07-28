# BPMP API Integration

BPMP exposes two contract-first integration surfaces:

- Third-party and browser clients use the API Gateway HTTP API.
- Trusted service clients use versioned Protobuf contracts and gRPC.

## Public HTTP API

The API Gateway embeds its OpenAPI 3.1 contract in the deployed binary. When
API documentation is enabled, the default E2E paths are:

- `/openapi/v1.json`: machine-readable OpenAPI contract.
- `/docs`: interactive Scalar API reference.

These paths and the pinned Scalar browser artifact are bootstrap configuration,
not source-code constants:

```json
{
  "api_docs": {
    "enabled": true,
    "openapi_path": "/openapi/v1.json",
    "reference_path": "/docs",
    "scalar_script_url": "https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.63.0"
  }
}
```

Production deployments may mirror the exact Scalar artifact into an approved
internal CDN and set `scalar_script_url` to that immutable HTTPS location.
Documentation paths cannot overlap health routes or the `/v1` API namespace.

The public contract covers workflow start, work-item query/completion/delegation,
case and audit query, and the complete runtime-configuration lifecycle. A
contract drift test compares every HTTP method, route and `operationId` against
the API Gateway route registry.

All business operations require a bearer JWT, `X-BPMP-Tenant-ID`, and
`X-Correlation-ID`. Commands additionally require `X-Command-ID` and
`Idempotency-Key`. The API Gateway preserves these identities when forwarding
to authoritative services.

## Internal gRPC APIs

The canonical schemas remain under `contracts/proto`. Buf lint and breaking
checks are the compatibility gate. Public-facing gRPC composition roots support
gRPC Server Reflection v1 when `grpc.reflection_enabled` is true:

- Engine command and governance APIs.
- Human Runtime API.
- Configuration resolver API.
- Projection query API.
- Governance approval API.

The Engine Raft peer listener intentionally does not register reflection.
Reflection is an operational discovery feature and does not bypass mTLS or
application authorization.

An enabled endpoint can be inspected without a local schema:

```powershell
buf curl --protocol grpc --list-services `
  --cacert ca.pem --cert client.pem --key client-key.pem `
  --servername governance-service https://localhost:17501
```

Set `reflection_enabled` explicitly in every environment fixture. Disable it
where service discovery is prohibited, while continuing to distribute
descriptor sets through the versioned contract build.

## Compatibility Workflow

1. Change the Protobuf or OpenAPI contract before changing service behavior.
2. Run Buf lint and breaking checks for Protobuf changes.
3. Run API Gateway contract drift tests for HTTP changes.
4. Generate or update client SDKs from `/openapi/v1.json` or the Buf module.
5. Deploy additive changes before clients; remove fields only through a
   versioned breaking-change process.
