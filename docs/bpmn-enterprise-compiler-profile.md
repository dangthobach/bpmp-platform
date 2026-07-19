# BPMN Enterprise Compiler Profile

This profile defines the enterprise BPMN constructs accepted by the AOT
compiler and the exact failure behavior for combinations that are not yet
executable. Unsupported combinations fail during compilation and never enter a
signed WIR artifact.

## Implemented

- Embedded sub-processes with one outer entry/exit and one inner start/end are
  normalized into a flat canonical graph. Nested sub-processes are normalized
  from the innermost scope outward.
- Call activities retain `calledElement` and optional `calledVersion` in a
  dedicated WIR node. The deterministic core exposes them through the external
  task completion protocol while child-instance orchestration remains an
  application-layer responsibility.
- Sequential/parallel multi-instance definitions retain collection, item,
  cardinality, and bounded parallelism metadata. Runtime cardinality and default
  parallelism ceilings come from the pinned resolved configuration, not constants.
- Interrupting/non-interrupting timer, error, and message boundary events retain
  typed triggers and participate in compiler and engine reachability checks.
- Arbitrary namespaced extension elements are flattened into a typed
  `PropertyBag` per process/node. Canonical printing preserves original
  namespace, element, key, and typed value.
- Gateway guards support comparisons, parentheses, negation, conjunction, and
  disjunction over multiple boolean/string/integer variables. Standard BPMN
  `conditionExpression` elements and the normalized `condition` attribute are
  both accepted.
- Complex gateway coverage uses a bounded symbolic decision procedure. It
  partitions scalar domains by comparison constants, checks every equivalence
  class, reports uncovered/overlapping witness assignments, and fails closed
  above the configurable `max_symbolic_assignments` budget.
- Canonical WIR sorting includes nodes, gateway transitions, boundary events,
  extension properties, case-model elements, and decision rules before hashing
  and Ed25519 signing.

## Durable Runtime Profile

- Collection values are materialized into `MultiInstanceStarted`; cardinality,
  effective parallelism, item variable, and item values are replay inputs rather
  than values recomputed after restart.
- Sequential and bounded-parallel iterations have explicit activated/completed
  events. Parallel completion replenishes one slot deterministically and the
  activity advances only after the persisted fan-in reaches total cardinality.
- Boundary subscriptions are explicitly armed and disarmed. Snapshots retain the
  typed timer/error/message trigger, attached node, target, interrupting flag,
  and arm timestamp.
- Interrupting boundary events persist cancelled task-token count and active
  multi-instance iteration indexes before routing to the boundary target.
  Non-interrupting branches retain the owner subscription and emit a branch-end
  audit event without completing the workflow while other work remains.

## Explicit Runtime Work

- Timer due-date scheduling and message/error correlation adapters must consume
  durable boundary subscription events and submit `TriggerBoundaryEvent`; they
  remain I/O concerns outside the deterministic domain.
- BPMN multi-instance completion-condition expressions are not yet represented
  in WIR. The implemented fan-in completes all materialized iterations.
- A multi-instance, compensation, or boundary scope attached directly to a
  sub-process requires retaining the scope node; the current inline normalizer
  rejects this combination with `InvalidSubProcess`.
- Call activity child-instance start/completion correlation is not yet wired to
  the application layer. The compiler, WIR, loader, and generated state machine
  retain the required call contract.

These remaining items are application orchestration or unsupported profile
combinations; they do not require DB/network/clock access in the pure evaluator.
