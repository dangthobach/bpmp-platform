# BPMP Cockpit Web

React 19 operational frontend for Human Runtime and public workflow commands.
The browser talks only to API Gateway. It must not call `bpmp-engine` or
`human-runtime` directly.

## Runtime configuration

`public/config.json` is deployment data and must be replaced per environment:

| Setting | Purpose |
| --- | --- |
| `apiBaseUrl` | Public API Gateway origin |
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

The Vite development proxy forwards `/v1` to the local TLS API Gateway. Replace
the proxy target or runtime `apiBaseUrl` for another environment.

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
