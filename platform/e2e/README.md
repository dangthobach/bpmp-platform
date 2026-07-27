# Broker-backed process E2E

This harness builds and starts three independent `bpmp-engine` processes,
Redpanda/Kafka, isolated Human Runtime and Configuration PostgreSQL databases,
Redis, Human Runtime, Configuration Service, API Gateway and an OTLP
collector. Runtime keys, certificates, signed WIR/policy artifacts and service
configuration are generated into the ignored `runtime/` directory.
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

The configuration probe also publishes a workflow-scoped Engine profile.
Configuration Service emits it through its ordered outbox and all Engine nodes
consume the publication before acknowledging their Kafka offsets.

Use `-KeepRunning` for inspection. Use `-SkipBuild` only when all three local
images already match the current source.
