# Case Service foundation

This module owns the customer Case aggregate. It does not use Human Runtime's
`human_cases` projection as the business record. Run
`db/case-service/migrations/001_case_foundation.sql`,
`002_outbox_delivery.sql`, `003_case_search.sql`, and `004_engine_event_inbox.sql` in order with a Case-only PostgreSQL database and role; do not
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

The Case read store provides tenant-scoped detail, a bounded Case event timeline,
a separately paginated Engine process timeline, and cursor-based search by exact customer reference. Its `published_version`
records broker delivery of an owned event, not consumer projection freshness.
Migration `004_engine_event_inbox.sql` adds the durable deduplication and
allowlisted metadata projection boundary for committed Engine events. It binds
the Engine instance UUID to a Case within the event tenant, verifies workflow
type/version, rejects event-ID payload changes and sequence collisions, stores
only event ID/type/sequence/time metadata, and advances orchestration state
only when a higher Engine sequence arrives. General task/activity payloads and
Case business status are not inferred from Engine events. The broker consumer
is implemented; its topic, schema rollout and inbox retention/replay window
require owner approval before deployment.
`cmd/case-local` is the local Case API runtime used by the E2E stack. It no
longer accepts a fixed tenant/actor or browser traffic directly. The Shell
same-origin development proxy routes `/v1/customer-cases` through API Gateway;
Gateway resolves the Keycloak session, issues a short-lived signed actor
context for workload `case-service`, and forwards it over mTLS. Case API
verifies the signature, tenant, command binding and `case.read`/`case.create`
capability before calling the Case application service. The container has no
published API port and is reachable only on Docker networks. The development
TLS certificate uses the existing trusted E2E certificate identity because the
fixture CA private key is intentionally unavailable after trust installation.

Run the complete local stack from the repository root with
`./platform/e2e/run.ps1 -KeepRunning`. After the API smoke checks pass, the
ODS Shell is at `http://localhost:4300/` and the Case remote manifest at
`http://127.0.0.1:4303/mf-manifest.json`. The Shell same-origin path is
authenticated by the API Gateway Keycloak session; it exposes search, create,
detail, Case timeline and Engine process events through the Case BFF. Case API
is Docker-network-only and verifies the short-lived tenant/actor proof and
capability issued by Gateway.

`cmd/case-start` is running in the local E2E Compose stack. It claims durable
start jobs, obtains fresh Authz actor/workload proof and sends the stable Case
command to Engine over mTLS. `cmd/case-engine-events` consumes committed Engine
Protobuf events, commits the Case inbox/projection before Kafka offsets, and
drives the Case orchestration status and freshness watermarks. The local
acceptance showed existing Cases at `ACTIVE`, start jobs `STARTED`, and Case
360 reporting Engine sequence 2 with both lifecycle events applied. This is a
Docker Desktop fixture acceptance, not cloud/staging evidence.

The worker currently resolves `start_node_id` and `encryption_key_scope` from
`runtime/case-workflow-metadata-e2e.json`, an immutable local fixture generated
for the E2E workflow. Replace this provider with the authoritative workflow
owner API before production deployment; never query another service's
database. The E2E TLS identity reuses the already-trusted certificate because
the fixture CA private key is unavailable; issue a named `case-local` server
certificate and approved client trust configuration for a production-like
deployment.

`cmd/case-engine-events` consumes the configured committed Engine Protobuf
topic using a dedicated consumer group. It commits offsets only after the
Case database transaction, so a crash before the offset commit safely replays
through `case_inbox`. Non-Case instance IDs are ignored; malformed messages or
Case identity/sequence conflicts stop the worker without committing past the
bad record. The example config uses the local E2E broker in plaintext and is
not a staging/production config. Deployment still requires the owner-approved
topic/schema, durable inbox retention and replay window, and production TLS
material.

`internal/engineadapter` maps a Case start job to Engine's `HandleCommand`
contract. It resolves credentials on every attempt, checks tenant/actor/command
and workload audience bindings plus expiry, and keeps the Case ID as the stable
instance, command and idempotency key. It accepts success only for a matching
committed Engine receipt. Its Authz credential provider requests a fresh signed
actor context through the internal HTTPS endpoint using a signed service
delegation token, signs a separate short-lived workload proof, and checks the
returned scope. This adapter/provider is connected to the local `case-start`
worker and Engine over mTLS. The local provider uses the E2E TLS identity; a
deployment must use approved mTLS trust material. `WorkflowMetadata` must
resolve the deployed definition's `start_node_id` and current tenant
`encryption_key_scope` from an authoritative owner API; it must not read another
service's database. The current E2E runtime uses a fixture catalog and is not
the production metadata integration.

Run unit checks with `go test ./...` and `go vet ./...`. Set
`CASE_SERVICE_POSTGRES_DSN` to a disposable PostgreSQL database to run the
integration test; it creates and drops its own schema. The test covers eight
concurrent create attempts, one receipt/event/job, tenant isolation, changed
intent rejection, lease fencing, start reconciliation, terminal manual-review
state, ordered outbox claims, retry and stale-worker rejection, Engine event
deduplication, changed-payload conflict, out-of-order state protection,
workflow identity checks, detail, timeline, search pagination and delivery watermark.
Set `CASE_SERVICE_KAFKA_BROKER` to a disposable Kafka-compatible broker to run
the broker round trip and combined PostgreSQL → outbox worker → broker ACK test.
