# Cockpit Web Architecture

## Ownership

`cockpit-web` owns browser presentation, URL state, transient form state,
selection state, bounded batch orchestration and cached server projections. It
does not own transition authorization, workflow interpretation, assignments,
SLA policy, idempotency decisions or authoritative state.

The workflow request path is:

```text
cockpit-web -> api-gateway -> human-runtime / bpmp-engine
```

API Gateway now exposes browser-safe read facades for `GetWorkItem`,
`ListWorkItems`, `GetCase` and `ListAuditRecords`. It verifies the JWT, applies
rate limits, preserves tenant/correlation metadata, and forwards the original
JWT as actor proof. Commands continue to require caller-generated command and
idempotency identities.

Organization control-plane requests use the separately configurable
`organizationApiBaseUrl` and go to `authz-app`. That service verifies the
original end-user JWT and requires the JWT tenant to match `X-Tenant-ID`.
Cockpit never receives or stores the service credential used by
`authz-server /admin/v1`.

The browser-safe organization contract is:

- `GET /api/v1/organizations`
- `GET /api/v1/organizations/{organization_id}`
- `POST /api/v1/organizations`
- `POST /api/v1/organizations/{organization_id}/nodes`
- `POST /api/v1/organizations/{organization_id}/nodes/{node_id}/move`

Raft peer RPCs, engine worker transitions and service-token protected AuthZ
administration routes remain internal. They require a server-side facade and
capability checks before exposure to Cockpit.

## State boundaries

- TanStack Query owns server projection state and keyset page cache.
- Browser history owns navigation state.
- React component state owns forms, dialogs and transient batch progress.
- Selection uses stable work-item IDs and is cleared on page transitions.
- Runtime configuration controls API origins, pagination, request timeout,
  batch chunk size, batch concurrency and cache staleness.
- Configuration administration uses the API Gateway `/v1/configuration`
  facade; the browser never connects to the internal resolver or database.

## Batch processing

Current public contracts expose idempotent single-item commands. The Cockpit
therefore splits selected IDs into bounded chunks and runs only the configured
number of concurrent commands. Each command derives a stable idempotency key
from action, work-item ID and expected version. Results retain successful and
failed item sets independently; one failed item does not retry or roll back
other committed items.

Organization creation uses the same bounded batch executor. Adding multiple
nodes to one organization is intentionally sequential because each command
consumes the version returned by the previous command; concurrent writes to
one aggregate would create avoidable optimistic-lock conflicts.

Server-side bulk command contracts remain the preferred next step for
all-matching selection across very large query result sets. Those contracts
must carry a canonical query signature plus exclusions and execute inside the
owning service, rather than materializing an unbounded ID list in the browser.
