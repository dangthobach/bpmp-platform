# Cockpit Gateway

Cockpit Gateway is the browser-facing realtime edge. It consumes committed
Engine integration events from Kafka and emits bounded SSE notifications.
Notifications are reconciliation hints only; Cockpit Web reloads versioned
query state from the public API and never treats SSE payloads as authoritative.

## Runtime contract

- `GET /realtime/v1/events?names=<comma-separated-signals>`
- Required headers: `Authorization: Bearer ...`,
  `X-BPMP-Tenant-ID`, and `X-Correlation-ID`.
- Optional reconnect header: `Last-Event-ID`.
- Supported signal names are deployment configuration.
- `/livez` and `/readyz` are exposed on the separate health listener.

The server validates the JWT against a bounded JWKS, issuer, audience,
algorithm and tenant. Subscription count, connections, names per connection,
header size, outbound queue, replay streams, replay depth and heartbeat are all
bounded by configuration. A slow consumer is disconnected. An expired or
unknown cursor receives `resync-required`.

## Kafka topology

Each running Cockpit Gateway replica must have its own stable consumer group.
Kafka consumer groups distribute records; sharing one group between gateway
replicas would prevent every replica from receiving the broadcast feed.
Reconnect to a replica without the cursor in its bounded replay ring safely
degrades to `resync-required`.

Topic and group names follow the platform convention:

```text
bpmp.<bounded-context>.<purpose>.v<schema>.<environment>
```

## Verification

```powershell
go test -race ./...
go vet ./...
```
