# Requirement 1 Compliance

This document traces Requirement 1 acceptance criteria to executable tests and
separates two different completion claims:

- **Executable profile:** every construct documented in
  `bpmn-enterprise-compiler-profile.md` must either compile with tested semantics
  or fail closed. All 12 dedicated acceptance tests pass for this profile.
- **Literal enterprise requirement:** the broad wording "valid BPMN/DMN/CMMN"
  is evaluated beyond the supported profile. This is not yet complete.

The focused gate is `cargo test -p bpmn-compiler --all-targets`; the repository
gate is `./tools/check.ps1 -IncludeWasmtime`.

## Current Assessment

As of 2026-07-19, the executable profile is **12/12 passing**. Against the
literal breadth of Requirement 1, **7 criteria are complete and 5 are partial**.
Requirement 1 must therefore not be reported as 100% complete.

## Acceptance Matrix

| AC | Implementation and executable evidence | Profile | Full requirement | Residual gap |
| --- | --- | --- | --- | --- |
| 1 | Namespace-aware bounded parser, semantic graph, WIR lowering; `ac1_namespace_aware_bpmn_compiles_to_wir` | Pass | Partial | The accepted BPMN catalog is a deliberate subset. Every unsupported executable BPMN flow element must be rejected explicitly; the current parser only diagnoses selected unsupported elements. |
| 2 | Typed DMN tables and CMMN case metadata; `ac2_dmn_and_cmmn_are_integrated_in_one_wir` | Pass | Partial | DMN supports boolean/integer/string decision-table IR. CMMN supports case/stage/milestone/sentry metadata, not the complete CMMN catalog or executable case semantics. |
| 3 | Standalone Rust generation without XML; `ac3_generated_rust_has_no_runtime_xml_dependency_and_compiles` | Pass | Partial | Compilation is proven, but behavioral equivalence is not proven for multi-instance, boundary, retained scope, compensation, and child call orchestration. |
| 4 | Static and bounded-symbolic coverage for boolean, enum/string and integer intervals; AC4 and gateway unit tests | Pass | Complete | Solver complexity is bounded by configuration and fails closed above the budget. |
| 5 | Forward/reverse reachability, dead paths, cycles and inclusive/parallel structural validation; AC5 and advanced gateway tests | Pass | Complete | Applies to the accepted and lowered graph profile. |
| 6 | Compensation boundary/association resolution and source spans; AC6 | Pass | Complete | Runtime compensation execution is tracked outside this compiler criterion. |
| 7 | Cumulative longest-path SLA validation and source spans; AC7 | Pass | Complete | Calendar-aware runtime scheduling is outside this static compiler criterion. |
| 8 | Transitive scalar contracts and DMN output propagation; both AC8 tests | Pass | Partial | No deep object/array/nullable/decimal/date-time schema compatibility or schema-reference resolution. |
| 9 | Typed CLI diagnostics, non-zero exit and no partial artifact; AC9 and CLI tests | Pass | Complete | The repository still needs a checked-in CI workflow to execute the gate automatically. |
| 10 | Deterministic Protobuf serialization, signed envelope and direct engine loading; AC10 | Pass | Complete | Artifact bytes do not depend on Rust memory layout. |
| 11 | Canonical printer and 100-case property tests at compiler and public API boundaries; AC11 | Pass | Partial | Current generators mostly vary identifiers on linear models. They do not generate the supported BPMN/DMN/CMMN grammar or every invalid class required by Properties 1 and 2. |
| 12 | Shared versioned Protobuf WIR, Ed25519 verification, Buf lint/breaking baseline; AC12 | Pass | Complete | Add old-version golden fixtures when schema version 2 or an upcaster is introduced. |

## Completion Blockers

The following work is required before claiming literal Requirement 1 completion,
in dependency order:

1. Reject every unsupported executable BPMN flow element explicitly with a
   source span; no standard semantic element may be silently ignored.
2. Make generated Rust behavior equivalent to the authoritative WIR evaluator
   for retained scopes, multi-instance, boundary, compensation, and call
   activity semantics.
3. Replace identifier-only round-trip generation with grammar-based
   BPMN/DMN/CMMN generators, and implement canonical Property 2 generation for
   each violation class with at least 100 cases.
4. Expand data contracts beyond scalar values to versioned deep structural
   typing and schema references.
5. Expand DMN FEEL/type/hit-policy coverage and CMMN lowering beyond the current
   stage/milestone/sentry metadata subset.

## Component Coverage

- Parser: namespaces, alternate prefixes, DI exclusion, malformed/random input,
  input-size/depth limits, DTD rejection, forward references, and source spans.
- Semantic validation: reachability, dead paths, cycles, exclusive/inclusive/
  parallel gateways, compensation, cumulative SLA, and transitive data contracts.
- Model integration: typed DMN IR/evaluation and the supported CMMN
  stage/milestone/sentry subset, including negative model validation.
- Outputs: canonical printer, generated Rust compilation, signed WIR artifact,
  CLI behavior, and compiler-to-engine loading.

## Standards Profile

The executable compiler profile also supports inline embedded sub-processes,
versioned call activities, typed multi-instance metadata, timer/error/message
boundaries, namespaced typed extension properties, and bounded symbolic
coverage for composed guards. The exact profile and runtime boundary are in
`docs/bpmn-enterprise-compiler-profile.md`.

Passing all twelve Requirement 1 acceptance checks means acceptance coverage
for the explicitly documented executable profile only. It does not mean literal
or full-catalog Requirement 1 completion. User tasks remain owned by Human
Runtime. Multi-instance fan-out/fan-in/replay and boundary subscription,
trigger, cancellation, and branch completion now execute through additive
durable events and snapshots in `bpmp-domain-core`/`bpmp-engine`. The bounded
timer scheduler and message/error correlation adapters persist projection,
lease, retry, checkpoint, signal, and dead-letter state in RocksDB and dispatch
through the authorized engine command path. Typed multi-instance completion
conditions now execute durably and record early-cancelled iterations. Retained
sub-processes with boundary or compensation ownership now preserve scope
identity in WIR, durable enter/complete events, snapshots, canonical round trips,
and deterministic replay for a single active invocation. Concurrent retained
scope tokens, interrupting cancellation of a retained scope, concrete deployment
transport bindings, and call child-instance orchestration remain explicit
follow-up work.
