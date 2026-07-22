# BPMN Element Coverage

This matrix distinguishes compiler recognition from complete executable
behavior. A green compiler test alone is not sufficient evidence of an
enterprise runtime implementation.

## End-to-End Executable Profile

| Family | Elements | Runtime evidence | Important edge coverage |
| --- | --- | --- | --- |
| Workflow | `process`, `startEvent`, `endEvent`, `sequenceFlow` | Typed WIR, signed loading, deterministic routing and replay | missing references, unreachable paths, cycles, terminal tokens |
| Tasks | `serviceTask`, `userTask`, version-pinned `scriptTask`, `businessRuleTask` | Dedicated task/decision events and commands; User Task committed projection | wrong lifecycle, duplicate completion, empty decision, tenant mismatch, boundary cancellation |
| Gateways | `exclusiveGateway`, `inclusiveGateway`, `parallelGateway` | Typed guards, persisted token obligations and joins | ambiguity, non-exhaustive coverage, unbalanced pairing, outstanding tokens |
| Scope | accepted `subProcess` subset | Inline lowering or retained scope identity and replay | invalid ownership, missing scope, single active retained invocation |
| Reuse | `callActivity` contract | Called element/version retained; external completion protocol | child-instance orchestration is not complete |
| Repetition | `multiInstanceLoopCharacteristics`, `loopCardinality`, `completionCondition` | Sequential/bounded-parallel fan-out/fan-in and early completion | zero/cardinality mismatch, configured bounds, cancellation, replay |
| Boundary | `boundaryEvent` with timer/error/message definitions | Durable subscriptions, correlation, retry and authorized dispatch | duplicate signal, lease recovery, ambiguous error, interrupting/non-interrupting behavior |
| Models | DMN decision tables and CMMN stage/milestone/sentry profile | Typed evaluation in authoritative Rust lifecycle | type mismatch, no rule, duplicate sentry, deterministic replay |

## Compiler-Supported but Runtime-Partial

| Construct | Remaining work before full claim |
| --- | --- |
| Compensation associations/events | Execute handler with a durable scoped compensation ledger and cancellation ordering |
| Retained sub-process boundaries | Scope-instance keyed concurrent tokens and interrupting cancellation |
| Call activity | Durable parent-child start/completion correlation and crash recovery |
| Multi-instance User Task | Iteration identity in work-item activation/completion, assignment and cancellation projection |
| Generated Rust state machine | Behavioral equivalence for retained scope, boundary, compensation, call activity and multi-instance combinations |
| Data contracts | Deep object/list/nullable/decimal/date-time typing and schema references |

## Explicitly Unsupported

These elements fail compilation with `UnsupportedElement` and an exact source
span. The catalog test executes every item, so none can silently disappear:

- Tasks: `task`, `sendTask`, `receiveTask`, `manualTask`.
- Gateways/events: `complexGateway`, `eventBasedGateway`,
  `intermediateCatchEvent`, `intermediateThrowEvent`.
- Scope/loop: `transaction`, `adHocSubProcess`, event sub-process and
  `standardLoopCharacteristics`.
- Event definitions: signal, escalation, cancel, conditional, link and
  terminate.
- Collaboration: choreography task/call/sub-choreography, conversation,
  collaboration, choreography, participant, message flow and conversation link.

## Completion Rule

Requirement 1 can be called 100% only after every partial and unsupported row
has typed WIR, pure decide/evolve semantics where applicable, durable events and
snapshots, transport/adapters, deterministic replay, negative tests, and at
least one production-path integration test. Requirement 2 additionally needs
broker-backed Kafka, PostgreSQL, concrete Rust Engine process integration and
P95 evidence under the declared production topology.
