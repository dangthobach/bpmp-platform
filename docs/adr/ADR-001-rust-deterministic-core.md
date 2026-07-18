# ADR-001: Rust-only deterministic workflow core

- Status: Accepted
- Date: 2026-07-18

## Context

Durable replay requires one authoritative interpretation of WIR and one set of
workflow transition rules. Reimplementing those rules in edge or projection
services would create divergent state.

## Decision

`bpmp-engine` is the only deployable that owns WIR interpretation, `decide()`,
`evolve()`, authoritative workflow state, transition authorization, and
write-side idempotency. Its domain core is Rust and has no clock, randomness,
network, database, filesystem, environment, async runtime, or ambient mutable
state. Every nondeterministic value enters through a command or a versioned
decision context.

Other services use versioned contracts and committed integration events. They
must not interpret WIR or apply authoritative workflow transitions.

## Consequences

The core can be replayed and property-tested as pure code. Adapters and service
APIs may evolve independently, but all command paths converge on the engine.

