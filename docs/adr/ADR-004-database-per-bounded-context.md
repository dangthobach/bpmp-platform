# ADR-004: Database per bounded context and no cross-service SQL

- Status: Accepted
- Date: 2026-10-30

## Context

Shared schemas and cross-service joins hide ownership, couple releases, expand
credential blast radius, and turn local migrations into distributed changes.
They also tempt services to bypass versioned APIs, event semantics, tenant
checks, and the Engine's authoritative transition path.

## Decision

Each stateful bounded context owns its database/schema, credentials, migrations,
retention, backup/restore, and data-access code. Engine workflow state belongs
to RocksDB/Raft. Human, Configuration, Projection, Governance, and Authz own
separate PostgreSQL state. Redis is permitted only for explicitly ephemeral
edge/session/rate-limit state and is not workflow authority.

No service may query, write, join, replicate tables from, or take a runtime
dependency on another service's database. Cross-context commands and reads use
versioned gRPC/REST contracts. Asynchronous propagation uses committed,
versioned events. A service may build a disposable local projection from those
events and must expose staleness where it affects a user decision.

A local state change and its inbox/outbox/audit/checkpoint records commit in one
local transaction where correctness requires it. Cross-service correctness does
not use a distributed database transaction; it uses idempotent commands,
outbox/inbox, sagas, explicit reconciliation, and compensation.

## Failure behavior

- A dependency database outage is surfaced as a typed API/event lag failure; a
  caller never falls back to direct SQL.
- Duplicate event/callback delivery returns the prior effect through inbox/dedup.
- A local deadlock/failover retry is bounded to an idempotent local transaction.
- Projection loss triggers rebuild, not reverse queries into the owner's store.
- Schema migration and rollback remain owned and tested by the deployable whose
  data is changing.

## Consequences

Cross-domain views are eventually consistent and may require composition or a
dedicated projection. Operational cost rises with each real bounded context, so
new databases/services require distinct ownership, lifecycle, scaling, security,
or release-cadence evidence. Shared packages may contain stable technical
plumbing and generated contracts, never a shared business repository/model.
