# BPMP Cockpit Web

React 19 operational frontend for Human Runtime, public workflow commands and
the organization control plane. The browser talks only to browser-safe public
services. It must not call `bpmp-engine`, `human-runtime`, Raft peers or
service-token administration endpoints directly.

## Runtime configuration

`public/config.json` is deployment data and must be replaced per environment:

| Setting | Purpose |
| --- | --- |
| `apiBaseUrl` | Public API Gateway origin |
| `organizationApiBaseUrl` | Public `authz-app` organization API origin |
| `realtimeBaseUrl`, `realtimePath` | Cockpit Gateway SSE endpoint |
| `realtimeSignalNames` | Versioned notification names subscribed by this UI |
| `realtimeReconnectInitialMs`, `realtimeReconnectMaxMs` | Bounded reconnect backoff |
| `defaultPageSize`, `maxPageSize` | Keyset page bounds |
| `batchChunkSize` | Maximum items retained in one client-side batch chunk |
| `batchConcurrency` | Maximum concurrent commands within a chunk |
| `requestTimeoutMs` | Per-request abort deadline |
| `staleTimeMs` | React Query reconciliation interval |

No tenant, access token, workflow version, assignment, decision, retry, page, or
batch value is embedded in application logic. Tenant and token are held in
`sessionStorage` and cleared by Disconnect.

## Development

```powershell
npm install
npm run dev
```

The Vite development proxy forwards `/v1` to the local TLS API Gateway and
`/api/v1` to local `authz-app`. Replace the proxy targets or runtime origins for
another environment.

## Verification

```powershell
npm run typecheck
npm test
npm run build
npm audit --audit-level=high
```

Batch actions reuse the existing idempotent single-item API. They process
stable work-item IDs in configured chunks and bounded concurrency, retain
per-item failures, and reconcile the keyset query after completion.

Realtime uses streaming `fetch` so the browser can preserve the Bearer token
and tenant header. Notifications only invalidate React Query state. Cursor
expiry, gateway restart or replica changes trigger a full query resync.
