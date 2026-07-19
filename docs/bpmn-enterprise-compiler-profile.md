# BPMN Enterprise Compiler Profile

This profile defines the enterprise BPMN constructs accepted by the AOT
compiler and the exact failure behavior for combinations that are not yet
executable. Unsupported combinations fail during compilation and never enter a
signed WIR artifact.

## Implemented

- Plain embedded sub-processes with one outer entry/exit and one inner start/end
  are normalized into a flat canonical graph. Sub-processes that own boundary
  or compensation semantics are retained as explicit WIR scope nodes, and child
  nodes carry their owning scope identity through canonical printing and engine
  loading.
- Call activities retain `calledElement` and optional `calledVersion` in a
  dedicated WIR node. The deterministic core exposes them through the external
  task completion protocol while child-instance orchestration remains an
  application-layer responsibility.
- Sequential/parallel multi-instance definitions retain collection, item,
  cardinality, and bounded parallelism metadata. Runtime cardinality and default
  parallelism ceilings come from the pinned resolved configuration, not constants.
- Multi-instance `completionCondition` is compiled into typed boolean IR. The
  deterministic evaluator injects BPMN instance counters, persists whether the
  condition completed the activity and records active iterations cancelled by
  early completion.
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
- A bounded boundary runtime projects committed subscription events into
  RocksDB, schedules due timers with reclaimable leases, and persists
  message/error signals before acknowledging ingress. Projection checkpoints,
  retry state, timer generations, and dead-letter state survive restart.
- Correlation is tenant/instance scoped and exact for messages. Errors support
  exact references and an explicitly modeled wildcard subscription; ambiguous
  matches fail closed. Dispatch re-enters `Engine::handle`, preserving authz,
  audit, optimistic version checks, and write-side idempotency.
- Timer expressions support RFC 3339 dates, bounded ISO 8601 duration values,
  and finite/infinite repeat cycles. Expression bytes and scheduling horizon are
  resolved from the pinned engine configuration.
- Retained sub-process entry and completion use deterministic scope instance
  identifiers. Scope invocation counters and active parent/child relationships
  are durable event and snapshot state, so replay does not derive identity from
  wall-clock or storage order.

## Explicit Runtime Work

- Deployment hosts must bind their concrete transport consumer and secure
  authorization-context vault to the boundary signal ingress and credentials
  ports. The engine does not trust actor claims supplied by a workload.
- Retained sub-process execution currently permits one active invocation per
  scope node. Concurrent or multi-instance retained scopes fail closed until
  tokens carry a scope-instance key through split/join and completion.
- Non-interrupting boundaries owned by a retained sub-process can be armed and
  routed. Interrupting scope cancellation and compensation-handler execution
  fail closed until scoped token cancellation is implemented.
- Call activity child-instance start/completion correlation is not yet wired to
  the application layer. The compiler, WIR, loader, and generated state machine
  retain the required call contract.

These remaining items are application orchestration or unsupported profile
combinations; they do not require DB/network/clock access in the pure evaluator.
