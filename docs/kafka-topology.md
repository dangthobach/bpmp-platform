# Kafka Topology

Kafka is an integration feed. RocksDB/Raft and each bounded context database
remain authoritative. Kafka downtime never rolls back a committed command or
configuration publication.

## Naming

Topics and consumer groups use:

`bpmp.<bounded-context>.<stream-or-purpose>.v<schema-version>.<environment>`

Names are lowercase ASCII with digits and hyphens inside each segment. Runtime
validation rejects missing schema/environment segments, uppercase names,
underscores, URLs in broker addresses, empty client IDs, non-idempotent
configuration producers, or acknowledgements weaker than `ALL`.

The E2E topology is declared once in `platform/e2e/manifest.json`. The fixture
generator derives Engine, API Gateway, Human Runtime and Configuration Service
configs plus `kafka-topics.sh`; compose contains no independent topic names.

## Bootstrap Boundary

Broker addresses, TLS client material references, topic names, consumer groups,
message bounds, and connection deadlines are bootstrap configuration. They
cannot be delivered through the Kafka channel they are required to establish.
Secrets remain external files or secret-store references.

Rate limits, retry/backoff, batching, leases, circuit breakers, SLA,
projection, KMS, and governance policy belong to versioned Configuration
Profiles. They are not embedded in topic names, handlers, or manifests.

## Configuration Publication

Configuration Service allocates `event_sequence` through a transactional
PostgreSQL counter, claims one globally ordered leased batch, and publishes
deterministic `bpmp.configuration.v1.ConfigurationPublicationEvent` messages.
The tenant ID is the partition key, preserving per-tenant ordering.

Runtime consumers disable auto commit and offset store. They validate the event,
resolve the latest snapshot over mTLS gRPC, install it atomically, then commit
the Kafka offset synchronously. Engine additionally waits for its exclusive
runtime safe point and replaces all affected registry entries as one batch.
Invalid or unavailable configuration stops the reloader and fails the process
rather than skipping a publication.

Configuration publications are broadcast invalidations, not work queue items.
Every process replica requires a distinct stable reloader group. The three E2E
Engine nodes therefore use three node-specific groups. API Gateway and Human
Runtime use one group each in E2E because each has one process.
