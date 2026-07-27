# Broker-backed process E2E

This harness builds and starts three independent `bpmp-engine` processes,
Redpanda/Kafka, PostgreSQL, Redis, Human Runtime, API Gateway and an OTLP
collector. Runtime keys, certificates, signed WIR/policy artifacts and service
configuration are generated into the ignored `runtime/` directory.

Run from the repository root:

```powershell
.\platform\e2e\run.ps1
```

The probe sends a JWT-authenticated start request to API Gateway configured
against an engine follower, verifies idempotent retry, waits for Kafka-backed
Human Runtime projection, stops the bootstrap leader, completes the work item
through the surviving Raft majority and waits for the committed completion
event to update PostgreSQL.

Use `-KeepRunning` for inspection. Use `-SkipBuild` only when all three local
images already match the current source.
