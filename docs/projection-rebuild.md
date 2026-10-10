# Projection Read-Model Rebuild

The Projection Service owns disposable PostgreSQL read models. RocksDB/Raft workflow events and their committed Kafka integration feed remain authoritative.

Use this procedure after adding a projection field that cannot be derived from existing rows, including `initiator_actor_id`, or after changing event-query indexes.

## Preconditions

- The committed-events topic still retains every event required for the rebuild.
- No Projection Service process is consuming or serving traffic.
- The target Kafka consumer group and PostgreSQL schema belong to the same environment.
- A database backup and the current consumer offsets have been recorded.

## Procedure

1. Scale Projection Service to zero and verify its consumer group has no active members.
2. Apply all files under `db/projection-service/migrations` in numeric order.
3. Execute `db/projection-service/operations/rebuild_read_models.sql` in the Projection Service schema.
4. Reset every partition of the configured committed-events consumer group to the earliest retained offset.
5. Start one Projection Service replica and wait until projection lag reaches the configured readiness threshold.
6. Verify row counts, completed-process queries, `initiator_actor_id`, event counts and tenant isolation.
7. Restore the desired replica count.

Example Redpanda offset reset, with environment-specific values supplied explicitly:

```powershell
rpk group seek $env:PROJECTION_CONSUMER_GROUP --to start --brokers $env:KAFKA_BROKERS
```

Do not run the SQL while a consumer is active. Do not synthesize missing initiator identities from later task actors. If Kafka retention is incomplete, restore the committed integration feed from an authoritative export before rebuilding.
