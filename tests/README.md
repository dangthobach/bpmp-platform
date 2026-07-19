# Test Organization

Executable Rust tests live beside the bounded context they verify:

- `apps/rust/*/tests/` contains deployable integration and CLI tests.
- `crates/*/src/` test modules contain pure domain and adapter tests.
- This directory is reserved for cross-service contract, property catalog,
  formal-model, chaos, and end-to-end suites that cannot belong to one crate.

Every universal requirement property must have one canonical property-based
test with at least 100 generated cases and this tag:

```rust
// Feature: rust-bpm-platform, Property N: property text
```

Current tagged coverage includes P1 (compiler round trip), P11 (deterministic
replay), and P53 (versioned configuration). The remaining P1-P53 catalog is an
explicit roadmap gap; ordinary example tests do not count as property coverage.

Requirement 1 has a dedicated AC1-AC12 acceptance suite and compliance matrix:

- `apps/rust/bpmn-compiler/tests/requirement_1_acceptance.rs`
- `docs/requirement-1-compliance.md`
- `apps/rust/bpmn-compiler/tests/enterprise_models.rs` covers sub-process,
  call activity, multi-instance, boundary events, extension properties, and
  symbolic complex-guard compilation/execution.
- `crates/bpmp-domain-core/src/workflow.rs` covers bounded sequential/parallel
  multi-instance execution, fan-in replay, interrupting/non-interrupting
  boundary semantics, durable subscription state, and branch completion.
- `apps/rust/bpmp-engine/src/event_codec.rs` and `snapshot_codec.rs` cover wire
  round trips and crash-recovery state for those runtime constructs.

Run all currently implemented workspace tests with:

```powershell
.\tools\check.ps1
```
