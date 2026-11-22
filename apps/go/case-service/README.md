# Case Service foundation

This module owns the customer Case aggregate. It does not use Human Runtime's
`human_cases` projection as the business record. Run
`db/case-service/migrations/001_case_foundation.sql` with a Case-only PostgreSQL
database and role; do not run it in the Human Runtime database.

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
Lease tokens fence stale workers. A committed start moves the Case to `ACTIVE`
and emits `CaseWorkflowStarted` in the same transaction.

The live Engine adapter, actor-proof acquisition, job scheduler, outbox
publisher/cleanup, inbox consumer, Gateway BFF and Case MFE are not wired yet.
Do not expose this module directly to browsers or treat the in-process
`StartPort` test as a live Engine integration.

Run unit checks with `go test ./...` and `go vet ./...`. Set
`CASE_SERVICE_POSTGRES_DSN` to a disposable PostgreSQL database to run the
integration test; it creates and drops its own schema. The test covers eight
concurrent create attempts, one receipt/event/job, tenant isolation, changed
intent rejection, lease fencing and start reconciliation.
