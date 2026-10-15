# RocksDB Durability and Operations

RocksDB is embedded in each Linux Engine process. Every Raft member owns its
own database directory and persistent volume. Never share a writable directory
between processes or replace it while an Engine is running. Kafka and service
PostgreSQL databases are not substitutes for the authoritative event log.

## Configuration and Admission

`rocksdb.write_buffer_size_bytes` applies to **each named column family**, not
to the aggregate database. Native OPTIONS-file regression tests verify its
effective value. Total memory also includes multiple memtables, block cache,
Raft proposals, snapshots and worker buffers. This field is not an RSS limit.

`max_open_files`, `max_background_jobs` and the replay bound remain explicit
bootstrap configuration. Tune them against the target disk and workload; do
not derive a 300k-CCU capacity claim from these knobs alone.

Engine bootstrap Raft configuration now requires:

```json
{
  "max_inflight_proposals": 16,
  "max_prepare_attempts": 8
}
```

These are example values inside the existing `raft` object, not a complete
runtime configuration. Update all Engine configs before rolling out; missing
fields fail configuration validation. Bounds are 1..4096 in-flight proposals
and 1..20 preparation attempts. Admission rejects excess work immediately;
there is no hidden unbounded mutex wait queue. Changing these bootstrap limits
requires a rolling restart; do not confuse them with hot-reloaded tenant policy.

Independent proposals no longer hold one global lock across quorum fsync/apply.
The state machine still checks deterministic preconditions and applies one sync
WriteBatch. Workflow preparation briefly locks local state to read a coherent
outbox cursor; encryption and quorum wait remain outside that lock. Only a CAS
failure at the outbox tail may trigger bounded re-preparation. Business version,
event identity, audit and idempotency conflicts are not silently retried.
Pre-materialized governance/remote-task proposals retain their original CAS
semantics and are not automatically reconstructed.

Outbox allocation remains global within one Raft group. Concurrent proposal
admission reduces unnecessary serialization, but cursor contention can still
consume retries. Benchmark before increasing concurrency; independent Raft
groups/sharding remain necessary for high write scale.

## Snapshots

Export uses one RocksDB MVCC snapshot across application CFs and Raft applied
metadata. A delayed OpenRaft snapshot builder uses metadata from that read point,
not stale metadata cached before newer entries applied. Export does not hold
the state write mutex; Raft log mutations use a separate lock on disjoint CFs.

Records are serialized incrementally into a bounded byte writer without first
materializing a second vector of all keys/values. The bound is checked on actual
JSON bytes, including byte-array expansion. Export is dispatched through the
blocking pool instead of scanning RocksDB on the async executor thread.

The OpenRaft snapshot transport still uses an in-memory Cursor/Vec and a JSON
envelope. Peak memory can exceed the inner application-snapshot byte limit due
to transport encoding/copies. It is not a file-streaming snapshot protocol.
Snapshot install still requires exclusive state replacement. Large-state
streaming install and fully disk-backed snapshot transfer remain future work;
keep configured limits within a measured per-node memory budget.

## Offline Check and Backup

Stop one Engine member gracefully and verify the other members still have
quorum. Use the same image, configuration, volume and key references:

```sh
bpmp-engine --config /runtime/engine-1.json --storage-check
bpmp-engine --config /runtime/engine-1.json --storage-checkpoint /backups/node-1-checkpoint
```

The process does not start Raft, Kafka or dispatch workers in maintenance mode.
It requires an existing CURRENT file instead of creating an empty database.
The check opens/replays the database and reads applied metadata plus native
background-error, write-stopped, compaction and memtable counters. Unknown
properties remain null, not fabricated zeroes. A background error/write stop
fails the check. This is not an exhaustive SST checksum scrub.

The checkpoint destination must be a new directory outside the live database;
its parent must exist. Native RocksDB checkpoint includes all CFs, WAL/log and
Raft metadata. Existing destinations and nested paths are rejected. A live
Engine holds RocksDB's file lock, so the separate offline CLI fails while that
Engine is using the volume. Restart the member after the operation and verify
it rejoins/catches up before maintaining another member.

Checkpoints can contain hard links to immutable SST files. Copy the entire
completed checkpoint to separate backup storage; a checkpoint on the same
failed disk is not disaster recovery. Retain an integrity manifest, encryption
key/version references, Engine/storage schema version and Raft node identity
alongside the backup. Restrict access; metadata may contain tenant/actor IDs.
Do not put KMS secrets or decrypted payloads in the manifest.

## Restore Drill

1. Provision an isolated volume and matching Engine/storage version. Preserve
   the original backup; restore a working copy to the new volume.
2. Point a copy of the explicit runtime config to that volume and run
   `--storage-check`. Confirm expected last-applied position/membership.
3. Restore required encryption key versions through the approved key resolver.
   Reopen/replay encrypted events and verify expected workflow version plus
   original idempotency outcomes. Never replace revoked keys with plaintext.
4. For a single member, retain its approved identity and join the original
   cluster under the membership procedure. Do not set bootstrap=true against
   surviving production members. Let Raft catch up before serving commands.
5. A total-cluster disaster requires a separately approved quorum/membership
   recovery procedure. Restoring a stale single-member checkpoint and forcing
   a new production cluster is not automatically safe.
6. Reconcile integration outbox and Kafka consumer checkpoints. Duplicate
   publication is expected; subscriber effects must remain idempotent.

Automated tests create a native checkpoint and reopen it, checking encrypted
workflow state/idempotency and publisher checkpoint. Run representative-volume
restore drills with external backups and key infrastructure before promotion.

## Failure Handling and Monitoring

Alert on native `rocksdb.background-errors`, `rocksdb.is-write-stopped`, pending
compaction bytes, memtable bytes, disk free space/inodes, WAL/fsync latency,
proposal admission rejection, outbox lag and snapshot failures. Offline JSON
diagnostics expose native counters; continuous metrics export/alerts must be
connected to the deployment's monitoring stack.

For ENOSPC or I/O errors, reject the command; never acknowledge before durable
write. Restore disk headroom or replace the failing volume, then reopen and
verify metadata before resuming. Do not delete WAL, MANIFEST or LOCK files to
"unstick" a process. A corrupted/missing MANIFEST fails closed.

Linux CI has a dedicated 16MiB tmpfs fault test. A 20MiB atomic state batch
cannot fit; state and applied metadata must remain unchanged. The test frees
its reserved space, reopens the store and checks no partial event appeared.
It is intentionally ignored in normal suites and must run explicitly on the
isolated fault filesystem, never on a shared workspace or production volume.

```sh
BPMP_ROCKSDB_FAULT_DIR=/fault cargo test --locked -p bpmp-adapter-rocksdb \
  --lib disk_full_write_batch_preserves_state_and_applied_metadata -- --ignored
```

Build caches are disposable; authoritative RocksDB volumes are not. Keep them
outside source/target directories and backups outside the live database.

## Verification on 2026-10-15

- Linux `cargo test --locked -p bpmp-adapter-rocksdb -p bpmp-raft-state-machine
  -p bpmp-engine-server`: 39 tests passed, one dedicated disk-full test ignored
  in the normal suite. This includes real RocksDB restart/checkpoint recovery,
  native CF options, snapshot lock/bound checks, delayed metadata regression,
  three-node peer RPC with concurrent workflow commits, idempotency, same-stream
  version contention, partition chaos and the bounded three-node safety model.
- The ignored disk-full test was executed separately in a dedicated 16MiB
  tmpfs and passed. No mock replaced the failing native disk write.
- Strict Clippy for those three packages/all targets passed with `-D warnings`.
  Targeted Rust formatting and fixture-generator tests also passed.
- No full broker/PostgreSQL process E2E or production capacity benchmark was
  rerun for this storage change. Continuous metrics/alert integration, external
  backup restore drills with real KMS and disk-backed snapshot transfer remain
  deployment/scale work, not claims established by the local tests.
