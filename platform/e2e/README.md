# Broker-backed process E2E

This harness builds and starts three independent `bpmp-engine` processes,
Redpanda/Kafka, isolated Human Runtime, Configuration, Projection and
Governance PostgreSQL databases, Redis, Human Runtime, Configuration Service,
Projection Service, Governance Service, API Gateway, Cockpit Gateway, Cockpit
Web and an OTLP collector. Runtime keys, certificates, signed WIR/policy artifacts and
service configuration are generated into the ignored `runtime/` directory.
Kafka topics and consumer groups are declared once in `manifest.json`; the
fixture generator emits the topic provisioning script and every service view.

Run from the repository root:

```powershell
.\platform\e2e\run.ps1
```

The probe sends a JWT-authenticated start request to API Gateway configured
against an engine follower, verifies idempotent retry, waits for Kafka-backed
Human Runtime projection, stops the bootstrap leader, completes the work item
through the surviving Raft majority and waits for the committed completion
event to update PostgreSQL.

Before leader loss, the probe opens an authenticated Cockpit SSE connection
and requires the committed workflow and work-item notifications for the new
instance. This verifies the real Kafka consumer, tenant fan-out and browser
transport contract rather than only exercising the hub in memory.
It also loads the production Cockpit Web image and its generated runtime
configuration through the same-origin Nginx edge.

The configuration probe publishes a workflow-scoped Engine profile and new
versions for the seeded API Gateway and Human Runtime profiles. Configuration
Service emits them through its ordered outbox. All three Engine node groups,
the Gateway group and the Human Runtime group must reach zero lag; each owner
resolves and installs its own authoritative snapshot before committing.

Before issuing business commands, the probe validates the embedded OpenAPI 3.1
contract and discovers the Governance API through authenticated gRPC
reflection. This catches missing documentation assets and descriptor wiring in
the deployed process topology.

Use `-KeepRunning` for inspection. Use `-SkipBuild` only when all local images
already match the current source. The operational build and deployment
sequence is documented in `docs/build-and-deploy.md`.
