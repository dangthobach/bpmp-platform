# ADR-008: Embedded authoritative transition authorization

- Status: Accepted
- Date: 2026-07-18

## Context

BPMP must re-authorize the end-user actor before idempotency lookup and every
workflow transition. A trusted workload identity cannot substitute for actor
identity. A remote PostgreSQL-backed decision service on the command critical
path would add availability, latency, and stale-cache failure modes.

## Decision

The policy control plane owns PostgreSQL policy administration and publishes
signed, immutable, tenant-scoped policy bundles. The authoritative data plane
is embedded in `bpmp-engine` and evaluates a verified actor proof against a
locally loaded bundle. It is fail-closed and performs no database, network,
clock, or external-attribute I/O while evaluating a transition.

`bpmp-authz-engine` is the pure decision boundary. Its complete input is the
verified bundle, actor roles/capabilities, transition resource, injected
evaluation timestamp, proof revoke epoch, and current in-memory revoke floors.
It has no async runtime, database, network, clock, or randomness dependency.

Actor and workload identity are verified independently. Both short-lived
signed contexts are bound to tenant and command identifiers; the actor context
is additionally audience-bound to the verified workload identifier. Proof
validity is checked against an explicitly injected evaluation timestamp, and
proof byte/role/capability bounds come from deployment configuration. The
verified actor, never a caller-supplied actor field, scopes idempotency and
event metadata.

Bundles and revoke updates use canonical Protobuf ordering, SHA-256 content
hashes, Ed25519 signatures, rotation-safe key identifiers, monotonic bundle
sequences, and monotonic tenant/actor revoke epochs. The engine rejects unknown
keys, tampering, non-canonical artifacts, sequence rollback, epoch rollback,
and revoke updates targeting a different bundle sequence. Artifact byte/grant
limits are injected from deployment configuration rather than fixed in logic.

Authorization ALLOW metadata is committed atomically with workflow events,
idempotency results, and outbox entries. Security fail-closed behavior is an
invariant, not a tenant-configurable fail-open option.

The audit record is append-only and keyed by tenant plus command identifier.
It includes the verified actor roles, workload, concrete transition selector,
matched grants, bundle sequence, revoke epoch, and configuration/policy
versions. Its payload is encrypted under the compliance-audit key scope from
the resolved Configuration_Profile and is written in the same RocksDB
`WriteBatch` as events, dedup markers, outbox entries, stream metadata, and the
idempotency result. A duplicate result without its audit record is treated as
corrupt durable state.

## Entity and locking conventions

Mutable control-plane entities use `version` for optimistic compare-and-swap
and `SELECT ... FOR UPDATE` for short pessimistic critical sections. Soft-delete
columns are `is_deleted`, `deleted_at`, and `deleted_by`. Immutable event,
decision-audit, history, and shadow-log records are append-only and never gain
soft-delete or mutable version columns.

## Consequences

`authz-control-plane` and its PostgreSQL pipeline are not transition
authorities. Policy publication, rollback, administration audit, and signed
bundle delivery remain control-plane responsibilities. The pure evaluator,
authorization contracts, JWT verification, and bundle cache live in separate
root workspace crates so database/network dependencies cannot enter the
embedded transition decision path.
