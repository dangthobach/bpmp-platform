# Case Service foundation

This module owns the customer Case aggregate. It does not use Human Runtime's
`human_cases` projection as the business record. Run
`db/case-service/migrations/001_case_foundation.sql`,
`002_outbox_delivery.sql`, and `003_case_search.sql` in order with a Case-only PostgreSQL database and role; do not
run them in the Human Runtime database.

`application.Service.Create` requires a trusted tenant and actor from the
calling boundary. It commits one Case, one create receipt, one `CaseCreated`
outbox event and one workflow-start job in a local transaction. Concurrent
retries with the same tenant, actor and idempotency key return the same Case;
changing the normalized intent returns a conflict. The Case ID is also the
stable Engine instance, command and idempotency identity for the start job.

`application.StartRunner` claims a durable job with a 30-second lease, calls a
`StartPort`, and reconciles success or ambiguous failure. The port contract
requires a fresh trusted actor proof and the same command identity on every
Engine retry. A failed call moves the Case to `RECONCILING`, schedules bounded
backoff, and stops automatic attempts after eight failures for manual review.
An Authz/metadata/proof failure before submission leaves the Case in
`START_PENDING` and retries without claiming an ambiguous Engine outcome.
Lease tokens fence stale workers. A committed start moves the Case to `ACTIVE`
and emits `CaseWorkflowStarted` in the same transaction.
After eight failed attempts the Case exposes `START_FAILED` and emits
`CaseWorkflowStartFailed` for manual review.

`cmd/case-outbox` runs the outbox scheduler and Kafka publisher. Supply a JSON
configuration using `-config` or `CASE_OUTBOX_CONFIG`; see
`case-outbox.example.json`. `-once` processes at most one ready event. The
producer requires `ALL` acknowledgements and idempotence, and its message
deadline is capped at 20 seconds, below the 30-second PostgreSQL lease. The
event value is a JSON envelope with tenant, event, aggregate, version,
correlation, causation, timestamp, producer and payload; the partition key is
`tenant_id:case_id`. Lease token and attempt count never enter Kafka.

The worker acknowledges the event in PostgreSQL only after broker ACK.
PostgreSQL leases fence stale workers; retries keep the same event ID and use
bounded backoff. Events for one Case are claimed in aggregate-version order.
Delivery is at least once: consumers must deduplicate `(tenant_id, event_id)`.
The event table has no cleanup job yet, so published rows must be retained
until an approved retention policy and consumer replay window are defined.
The JSON envelope and topic remain provisional until the Case event contract
and Protobuf compatibility gate are approved; do not promote this topic to a
shared production contract yet.

The Case read store provides tenant-scoped detail, a bounded event timeline,
and cursor-based search by exact customer reference. Its `published_version`
records broker delivery of an owned event, not consumer projection freshness.
`cmd/case-local` is a loopback-only development HTTP boundary for the Case MFE.
Set `CASE_LOCAL_POSTGRES_DSN`, `CASE_LOCAL_TENANT_ID`, `CASE_LOCAL_ACTOR_ID`,
and `CASE_LOCAL_ALLOWED_ORIGIN=http://127.0.0.1:4300`, then run
`go run ./cmd/case-local`. The shell proxies `/v1/customer-cases` to this
service at `127.0.0.1:8085`; browser commands from `localhost:4300` are also
accepted. It uses a fixed development identity and must
never be exposed outside the local host or used as a production BFF.

The live Engine connection, workflow-start job scheduler, authoritative
workflow metadata lookup, outbox cleanup, inbox consumer, and authenticated
Gateway BFF are not wired yet. Do not treat the in-process `StartPort` test as
a live Engine integration.

`internal/engineadapter` maps a Case start job to Engine's `HandleCommand`
contract. It resolves credentials on every attempt, checks tenant/actor/command
and workload audience bindings plus expiry, and keeps the Case ID as the stable
instance, command and idempotency key. It accepts success only for a matching
committed Engine receipt. Its Authz credential provider requests a fresh signed
actor context through the internal HTTPS endpoint using a signed service
delegation token, signs a separate short-lived workload proof, and checks the
returned scope. The HTTP client supplied at deployment must use the approved
mTLS trust material. `WorkflowMetadata` must resolve the deployed definition's
`start_node_id` and current tenant `encryption_key_scope` from an authoritative
owner API; it must not read another service's database. The adapter and provider
are not yet connected to a running start worker or Engine.

Run unit checks with `go test ./...` and `go vet ./...`. Set
`CASE_SERVICE_POSTGRES_DSN` to a disposable PostgreSQL database to run the
integration test; it creates and drops its own schema. The test covers eight
concurrent create attempts, one receipt/event/job, tenant isolation, changed
intent rejection, lease fencing, start reconciliation, terminal manual-review
state, ordered outbox claims, retry and stale-worker rejection, detail,
timeline, search pagination and delivery watermark.
Set `CASE_SERVICE_KAFKA_BROKER` to a disposable Kafka-compatible broker to run
the broker round trip and combined PostgreSQL → outbox worker → broker ACK test.
