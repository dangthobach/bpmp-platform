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

## Completed Process Query

`POST /v1/process-instances/completed/query` queries the Projection Service's
tenant-owned read models. It never scans the Engine event store and does not
join another bounded context's database. Filters are a typed Boolean tree;
field names and operators are Protobuf enums, and nested event conditions are
compiled to parameterized `EXISTS` predicates.

```powershell
curl.exe -k -X POST "https://localhost:14173/v1/process-instances/completed/query" `
  -H "Authorization: Bearer $token" `
  -H "X-BPMP-Tenant-ID: tenant-e2e" `
  -H "X-Correlation-ID: completed-query-1" `
  -H "Content-Type: application/json" `
  --data-binary '{
    "filter": {
      "group": {
        "operator": "LOGICAL_OPERATOR_AND",
        "expressions": [
          {"predicate": {
            "field": "PROCESS_FIELD_WORKFLOW_TYPE",
            "operator": "COMPARISON_OPERATOR_EQUAL",
            "values": [{"string_value": "approval"}]
          }},
          {"event_relation": {
            "quantifier": "RELATION_QUANTIFIER_EXISTS",
            "expression": {"predicate": {
              "field": "EVENT_FIELD_EVENT_TYPE",
              "operator": "COMPARISON_OPERATOR_EQUAL",
              "values": [{"string_value": "user_task_completed"}]
            }}
          }}
        ]
      }
    },
    "sort": [
      {"field": "PROCESS_FIELD_COMPLETED_AT", "direction": "SORT_DIRECTION_DESCENDING"},
      {"field": "PROCESS_FIELD_DURATION_MS", "direction": "SORT_DIRECTION_ASCENDING"}
    ],
    "page_size": 50
  }'
```

Use the returned `next_page_token` unchanged on the next request. The token is
HMAC signed, key-versioned, and bound to the tenant, actor subject, effective
authorization policy, filter and sort fingerprint; changing any of them
invalidates the token. The Projection Service verifies the forwarded actor JWT
again. The dynamic `query_all_capability` permits every matching row in the
tenant; `query_own_capability` adds a typed
`PROCESS_FIELD_INITIATOR_ACTOR_ID = actor_id` predicate. That authorization
predicate is combined with the client filter using `AND` before validation and
SQL compilation. Query limits are resolved per tenant from `ProjectionPolicy` at the
start of each request: page size, timeout, filter depth, predicate count, sort
count, relation joins, `IN` cardinality and string length. Raw SQL, arbitrary
columns and cross-service joins are not part of this API.

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
