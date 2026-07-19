# Requirement 1 Compliance

This document traces Requirement 1 acceptance criteria to executable tests. The
gate for this requirement is `cargo test -p bpmn-compiler --all-targets`; the
repository gate is `./tools/check.ps1`.

## Acceptance Matrix

| AC | Implementation evidence | Executable evidence | Status |
| --- | --- | --- | --- |
| 1 | Namespace-aware streaming BPMN parser and WIR lowering | `ac1_namespace_aware_bpmn_compiles_to_wir` | Pass |
| 2 | Typed DMN decision tables and CMMN case models embedded in WIR | `ac2_dmn_and_cmmn_are_integrated_in_one_wir` | Pass |
| 3 | Standalone Rust state-machine generation from WIR | `ac3_generated_rust_has_no_runtime_xml_dependency_and_compiles` | Pass |
| 4 | Boolean, enum, and integer-interval gateway coverage analysis | `ac4_non_exhaustive_gateway_reports_source_location` plus compiler unit tests | Pass |
| 5 | Forward/reverse reachability, cycle, and structural gateway validation | `ac5_dead_and_unreachable_paths_report_locations` plus advanced gateway tests | Pass |
| 6 | BPMN compensation boundary and association resolution | `ac6_standard_compensation_boundary_resolves_handler_and_missing_handler_fails` | Pass |
| 7 | Longest cumulative SLA path validation | `ac7_cumulative_path_sla_conflict_reports_path` | Pass |
| 8 | Transitive typed data-flow and DMN I/O contract validation | `ac8_data_contract_types_propagate_through_gateway`, `ac8_dmn_output_type_is_inferred_and_checked_downstream` | Pass |
| 9 | CI-oriented diagnostics and non-zero CLI exit without partial artifact | `ac9_cli_returns_nonzero_and_does_not_publish_invalid_artifact` | Pass |
| 10 | Protobuf serialization, Ed25519 envelope, and direct engine loading | `ac10_serialized_artifact_loads_directly_in_engine` | Pass |
| 11 | Deterministic canonical print and compile round trip | `ac11_compile_print_compile_is_equivalent_for_valid_models` with 100 generated cases | Pass |
| 12 | Shared versioned Protobuf WIR contract and signature verification | `ac12_versioned_signed_contract_rejects_tampering` plus Buf gates | Pass |

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

Passing all twelve Requirement 1 acceptance checks means full acceptance
coverage for this executable profile. It does not claim implementation of every
element in the full OMG BPMN/DMN/CMMN catalogs. User tasks remain owned by Human
Runtime. Multi-instance fan-out/fan-in/replay and boundary subscription,
trigger, cancellation, and branch completion now execute through additive
durable events and snapshots in `bpmp-domain-core`/`bpmp-engine`. Retained
sub-process scopes, multi-instance completion-condition expressions, external
timer/message/error correlation adapters, and call child-instance orchestration
remain explicit follow-up work.
