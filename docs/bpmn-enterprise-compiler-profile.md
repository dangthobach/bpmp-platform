# BPMN Enterprise Compiler Profile

This profile defines the enterprise BPMN constructs accepted by the AOT
compiler and the exact failure behavior for combinations that are not yet
executable. Unsupported combinations fail during compilation and never enter a
signed WIR artifact.

## Fail-Closed Catalog

The parser classifies BPMN model elements through one centralized executable
profile. Known presentation metadata is ignored explicitly. Every other element
in the BPMN model namespace is either lowered by a supported branch or rejected
with `UnsupportedElement` and an exact source span. Vendor extensions remain
supported only through their own namespace under `extensionElements`.

The following executable families are recognized but rejected until they have
an end-to-end WIR and durable runtime implementation:

- generic, send, receive, and manual tasks;
- complex and event-based gateways;
- intermediate catch and throw events;
- transaction, ad-hoc, event sub-process, and standard-loop scopes;
- signal, escalation, cancel, conditional, link, and terminate event definitions;
- choreography, conversation, participant, and message-flow constructs.

This classification is intentionally not an alias layer. For example, a receive
task is not lowered as a service task because doing so would omit its durable
message subscription and acknowledge semantics.

## Implemented

- User tasks lower to a dedicated WIR node with a dynamic
  `assignmentPolicyRef` lookup key and optional `formKey`. Activation and
  completion use dedicated durable domain events; committed activations are
  published through the engine outbox for Human Runtime projection. Omitting
  `assignmentPolicyRef` uses the stable node ID as the configuration lookup key,
  not a fixed assignee or group.
- Script tasks lower to a dedicated WIR node only when both
  `implementationRef` and `implementationVersion` pin an external executable
  artifact. Activation/completion have typed durable events. Inline BPMN script
  bodies fail closed so unversioned source cannot enter a signed deployment.
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
- Interrupting boundaries on a user task also commit a typed
  `UserTaskCancelled` event in the same authoritative engine transaction. Human
  Runtime closes the PostgreSQL work item only from that committed event; retry
  uses the boundary command idempotency key and cannot cancel twice.
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
- Human Runtime must still project `UserTaskActivated`, resolve the assignment
  policy by tenant/workflow/version/node/config version, and submit authorized
  `CompleteUserTask` commands. Assignment, claim/delegate, forms, and SLA state
  are not owned by the compiler or deterministic engine core.
- Multi-instance user tasks still need iteration identity in the Human Runtime
  work-item and completion contracts. Core fan-out/fan-in is durable, but this
  combination remains outside the end-to-end human-task profile and must not be
  presented as fully integrated.
- A deployment adapter must resolve the signed/pinned script artifact, execute
  it through the Wasmtime port, durably persist its result, and only then submit
  `CompleteScriptTask`. The compiler and core do not perform registry, network,
  or clock I/O.
- The fail-closed catalog above still requires separate vertical slices before
  those elements become executable. The first slice should introduce one common
  durable interaction contract for receive tasks, intermediate catch events,
  and event-based gateways; the next should add send/throw outbox semantics.
- Transaction/event sub-process cancellation, escalation, and compensation need
  scope-instance keyed tokens before those scope variants can be enabled.
- Choreography and conversation models should compile into participant process
  contracts or a separate collaboration IR; they must not masquerade as local
  workflow nodes in the authoritative engine.

These remaining items are application orchestration or unsupported profile
combinations; they do not require DB/network/clock access in the pure evaluator.
