# Signed Query Revoke Kafka Feed

## Ownership and Durability

Authz Control Plane owns actor epochs and the immutable `query_revoke_feed`.
Revoke/restore advances the actor epoch and appends the feed in the same local
transaction. That feed is the publisher outbox; publishing is not part of the
administrative request's Kafka availability requirement.

The publisher claims an ordered bounded batch using a PostgreSQL lease and
generation fence. It signs Protobuf events with Ed25519 and SHA-256, publishes
with tenant keys, idempotence and all-ISR acknowledgements, then checkpoints
each acknowledged sequence. Sequence gaps are valid. Lease expiry fences
renewal/checkpoint; another publisher replays uncheckpointed events. Delivery
is at-least-once, not exactly-once across PostgreSQL and Kafka.

Projection verifies schema, UUIDs, tenant key, numeric bounds, content hash and
signature before installing an epoch. It never accesses the Authz database.
Actor updates serialize through a local advisory transaction lock. Duplicate
events are idempotent; older epochs cannot overwrite newer epochs. Installation
and source checkpoint are atomic in Projection PostgreSQL; Kafka offset commit
occurs afterward. PostgreSQL/ACK/offset failures therefore replay safely.

All affected Go readers use shared fetch-error classification. Group rejoin,
coordinator changes and retryable transport failures retain the client session
and successful partitions from the same fetch; fatal authorization/configuration
errors still stop the worker. Client recovery backoff derives from centralized
`poll_timeout_ms`. This prevents broker outage from being misclassified as a
terminal service failure without hiding invalid records or changing offsets.

The durable gate is checked before the online PDP. Restore requires a new
actor token with the advanced epoch. A Kafka lag/outage never permits a token
rejected by the authoritative PDP. Signed internal actor context and external
JIT providers are separate features and remain unsupported/fail-closed.

## Centralized Configuration

`platform/e2e/manifest.json` is the E2E topology source; the fixture generator
emits topic provisioning, Authz bootstrap and Projection bootstrap consistently.
Names follow `bpmp.<owner>.<purpose>.v<positive-version>.<environment>`:

| Role | E2E Name |
|---|---|
| Revoke topic | `bpmp.authz.query-revocations.v1.e2e` |
| DLQ topic | `bpmp.authz.query-revocations-dlq.v1.e2e` |
| Consumer group | `bpmp.projection.query-revocations.v1.e2e` |

Topic partitions, replication, minimum ISR, retention and message bounds are
manifest fields. Production must provision topics explicitly and choose
replication/minimum ISR for its broker count. Never enable topic auto-creation
as a recovery path. The single-broker E2E does not prove broker HA.

Authz uses the existing `KAFKA_*` bootstrap for brokers, client identity,
TLS CA/certificate/private-key references and maximum payload size.
`QUERY_REVOKE_KAFKA_CONFIG` points to a JSON document with:

```json
{
  "topic": "bpmp.authz.query-revocations.v1.production",
  "worker_id": "authz-revoke-publisher-1",
  "batch_size": 64,
  "lease_ms": 10000,
  "poll_ms": 250,
  "delivery_timeout_ms": 2000,
  "retry_backoff_ms": 500,
  "max_retry_attempts": 3,
  "signing_key_id": "query-revoke-v1",
  "signing_key_path": "/secrets/query-revoke-v1.key"
}
```

These are deployment examples, not source-coded tenant policy defaults. Each
publisher snapshots this document at the beginning of a batch. Lease must
exceed twice delivery timeout. Transport, signing key, delivery timeout or
queue-bound expansion require a rolling restart rather than an unsafe partial
reload. Each publisher instance needs a distinct worker ID.

Projection `query_revoke` contains `consumer`, `dlq` and `trust_keys`. Consumer
and producer use common `go/platform/kafkaconfig` validation; producer enforces
ALL ACK/idempotence and bounded buffering. TLS uses external key references;
PLAINTEXT is only appropriate for isolated development networks. Broker ACLs
must grant Authz write on source, Projection group/read on source and write on
DLQ, and operators read on DLQ/source; do not give arbitrary clients write on
the security feed. Query workload and actor credentials remain distinct.

Database-backed ProjectionPolicy controls `query_revoke_retry_attempts`,
`query_revoke_retry_backoff_ms`, `query_revoke_operation_timeout_ms` and consume
batch size. Missing policy fails closed. Workers snapshot at batch boundaries,
not between database effects and offset commit. Transport/trust bootstraps must
exist before the configuration consumer can run and require rolling restart.
Update existing profiles with all three positive retry fields before rollout.
The same bounded retry budget protects workflow projection offset commits.
Checkpoint flush deadlines also bound polling, so a partial batch is committed
without waiting for a new workflow event.

## DLQ and Recovery

Malformed, untrusted or conflicting events are not silently skipped:

1. Insert an immutable incident and activate a durable query fence.
2. Publish the typed dead letter and require broker ACK.
3. Persist DLQ acknowledgement and source checkpoint atomically.
4. Commit the source Kafka offset.

DLQ contains source topic/partition/offset, SHA-256, byte count, reason and a
stable incident key. It deliberately excludes untrusted raw payload and secrets.
Crash after broker ACK but before database ACK may duplicate a DLQ record;
deduplicate by incident ID. Reusing source coordinates with a different payload
is rejected, so do not delete/recreate security topics under the same name.

An unidentifiable poison record fences all queries served by the affected
Projection database, not just an unverified tenant claim. This availability
tradeoff is deliberate. Resolving an incident requires operational review; it
is never automatically resolved by the arrival of a newer event.

Inspect the source using its coordinates within retention, verify the stored
digest, repair trust/configuration if needed, and replay authoritative signed
events. Do not forward the metadata-only DLQ envelope back into the source.
For publisher replay, stop all publishers, record their checkpoint, set the
technical checkpoint to an approved earlier sequence and clear expired leases,
then restart. Never mutate the immutable feed or manually lower actor epochs.

After reviewing/reconciling the source and confirming durable DLQ ACK, use
the owning Projection database with an audited operator identity:

```sql
SELECT resolve_query_revoke_incident(
  $1, -- incident ID
  $2, -- exact expected SHA-256 bytes from reviewed source
  $3, -- operator reference
  $4  -- reviewed reconciliation reason
);
```

The function rejects mismatched hashes or missing ACK; repeated resolution is
idempotent. Incidents and resolutions reject UPDATE/DELETE. Restrict direct
table writes and function execution to appropriate database roles. Workflow
read-model rebuilds must not truncate revoke epochs, checkpoints or incident
state. Restore backups/replay the authoritative feed before serving queries
from a new Projection database.

For key rotation, first install both old and new public-key IDs on all readers,
restart readers, rotate publisher key/ID and roll publishers. Keep old public
keys for the entire feed replay/backup retention window. Private keys are raw
32-byte Ed25519 seeds in secret storage, never browser config or git.

## Observability and Verification

Publisher warnings include sequence/attempt without payload; stable event
correlation IDs are sent as Kafka headers. Projection consumes/injects shared
trace metadata and logs quarantines by incident/topic/partition/offset.
Monitor publisher checkpoint vs committed feed sequence, lease failures,
consumer lag, unresolved incident count and unacknowledged DLQ state. Alert on
any unresolved incident. Exhausted worker retries fail the process; deployment
supervision must restart it, with offsets/checkpoints retained. Never swallow
the failure or change to an authorization allow fallback.

Run the isolated process suite with existing images and the local generator:

```powershell
$env:COMPOSE_PROJECT_NAME = 'bpmp-revoke-kafka-e2e'
./platform/e2e/run.ps1 -SkipBuild -FixtureGeneratorPath ./target/debug/bpmp-e2e-fixtures.exe
```

Startup is bounded by `StartupTimeoutSeconds`; revoke probes have deadlines
and dump service logs on failure. The suite checks three-engine leader failover,
real workflow/query paths, broker outage without premature publisher checkpoint,
signed revoke/restore, old-token denial, fresh-token success, outbox replay,
actual broker DLQ record, durable fence across Projection restart and reviewed
resolution. Unit/integration tests cover signature tamper, unknown fields/keys,
oversized envelopes, PostgreSQL replay and append-only audit, DLQ ACK failures,
lease takeover/generation fencing, and decoder fuzzing.

This suite does not establish 300k-CCU capacity, multi-broker ISR failure,
production TLS certificate rotation under load or all platform chaos scenarios.

### Verified on 2026-10-15

- Broker-backed process E2E passed on Linux containers with Kafka/Redpanda,
  PostgreSQL, three engine processes (leader-loss path), Human Runtime, API
  Gateway, Projection, Configuration, Governance and Cockpit.
- Infrastructure startup took 5.3 seconds and application startup 11.0 seconds
  on the final run. No target/cache deletion was needed.
- The 10-second broker pause preserved the Authz commit and publisher checkpoint;
  readers recovered without manual process restart. Restore reached epoch 2,
  old tokens were denied and a fresh epoch-2 token could query.
- Publisher replay drained; a real poison record reached broker DLQ, persisted
  its ACK/checkpoint, fenced queries across Projection restart, and required
  reviewed resolution before query access returned.
- Go race tests passed for all packages in Projection, API Gateway, Human
  Runtime, Cockpit Gateway and Configuration Service, plus shared Kafka/runtime
  configuration. Projection persistence tests used real PostgreSQL; integration
  suites requiring other unset external DSNs were not claimed as executed.
- Go vet, Buf lint and Buf breaking comparison against HEAD passed. Rust
  publisher tests passed with real PostgreSQL lease expiry/takeover fencing.
  Three fixture validation tests passed. Decoder fuzzing executed 24,400 cases
  in five seconds without finding a failure.

Fault injection identified and fixed terminal handling of transient group
errors and unbounded polling that delayed partial-batch checkpoint flushes.
The final E2E run used rebuilt affected service images; unchanged engine images
were reused. Existing sqlx 0.7.4 future-compatibility warnings remain a separate
dependency maintenance issue.
