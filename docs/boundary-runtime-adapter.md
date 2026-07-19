# Boundary Runtime Adapter

The boundary runtime is an application/I/O adapter around the deterministic
workflow evaluator. It does not evaluate BPMN itself and does not mutate an
instance outside `Engine::handle`.

## Processing Contract

1. `project_once` reads committed outbox events in cursor order and atomically
   applies subscription mutations with an independent RocksDB checkpoint.
2. `dispatch_due_timers_once` claims a bounded due-time range using a durable
   lease. A crash leaves the claim reclaimable after `lease_duration_ms`.
3. Transport consumers construct a `BoundarySignal` and call `enqueue_signal`.
   They acknowledge the source only after the durable write succeeds.
4. `dispatch_correlations_once` claims persisted signals, resolves exactly one
   tenant/instance subscription, and submits a deterministic command identity.
5. Success atomically completes or rearms the adapter record. Failure schedules
   a bounded retry and moves the record to dead-letter state at the configured
   attempt limit.

All batch sizes, lease/retry durations, timer limits, worker identity, ingress
identifier limits, and per-instance correlation scan limits are supplied by
`BoundaryRuntimePolicy`. A deployment must resolve the policy from the same
pinned configuration snapshot used for the workflow version.

## Authorization Boundary

Message and error ingress stores an opaque `authorization_context_ref`, not an
unverified actor identity. `BoundaryDispatchCredentialsPort` receives the final
deterministic request so a trusted identity adapter can mint command-bound actor
and workload proofs. Timer requests have no actor-context reference and must be
mapped to an explicitly configured system capability. Empty credentials,
definition scope mismatch, invalid proof, and ambiguous correlation fail closed.

## Timer Profile

The scheduler accepts RFC 3339 `timeDate`, exact non-calendar ISO 8601 durations
using week/day/hour/minute/second units, and `R[n]/duration` or
`R[n]/start/duration` cycles. Calendar years/months are intentionally rejected
because they require a separately configured calendar/time-zone policy.
