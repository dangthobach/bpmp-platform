# ADR-005: Engine owns governance atomicity and the Compensation Ledger

- Status: Accepted
- Date: 2026-10-30

## Context

Retention, erasure, dual-control approval, and KMS operations span Governance,
Engine, and an external key manager. If Governance writes workflow storage
directly or shreds a key before the workflow reaches a durable compliant state,
the platform can lose replay data while side effects or compensation obligations
remain active.

## Decision

The Engine is the only owner of authoritative workflow events, the Compensation
Ledger, compliance terminal state, reconciliation work items, transition audit,
and their atomic commit. Governance owns policy/approval workflow and KMS
orchestration. It sends a signed, state-bound command/proof to the Engine and
never writes the Event Store or Compensation Ledger directly.

The default policy is `CompensationBeforeErasure`: pending side effects remain
visible in the ledger, compensation continues, and key shredding waits until all
required entries are durably `Compensated`.

`AbortAndReconcile` is an explicit legal override. The Engine revalidates
distinct maker/checker actors, capability, fresh-auth assurance, tenant, expiry,
policy, legal deadline, key epoch, and current sorted pending-ledger digest. It
then atomically commits `TerminatedForCompliance`, ledger status
`ReconciliationRequired`, one durable non-PII reconciliation work item per
pending side effect, audit, idempotency result, and outbox records. Missing
opaque operation references or stale proof state rejects the transition.

Only after that commit may Governance claim the durable key-lifecycle job,
obtain the Engine key-cache revocation barrier acknowledgement, call KMS shred,
and mark the job complete. Each step is bounded, leased, retryable, and audited.

## Failure behavior

- Approval or state digest mismatch leaves workflow state and keys unchanged.
- Engine commit failure prevents barrier and KMS calls.
- Barrier/KMS failure leaves a durable retryable job and never enables a
  plaintext or stale-key fallback.
- Governance crash after Engine commit resumes from its durable job.
- Governance crash after KMS acknowledgement reconciles the job using the same
  stable request/key identity; it does not issue an unrelated workflow commit.

## Consequences

Governance availability may delay erasure but cannot create partial workflow
truth. Engine and KMS protocols require failure/chaos tests for stale proofs,
partition, leader change, key rotation/revocation, cache eviction, retry, and
recovery. Operations must monitor legal deadlines, pending compensation,
reconciliation debt, barrier failures, KMS failures, and leased-job age.
