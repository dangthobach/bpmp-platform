# ADR-003: Protobuf for durable contracts

- Status: Accepted
- Date: 2026-07-18

## Context

WIR and event bytes outlive a process and often outlive a deployed binary.
Rust memory layout and implementation-specific serializers are not stable
cross-language contracts.

## Decision

Use versioned Protobuf packages for WIR, command, event, and configuration
contracts. Field numbers, enum values, package versions, and reserved fields
are immutable protocol constants. Removed fields are reserved and semantic or
wire-breaking changes require a new package version.

Generated transport types are mapped into validated domain types at ingress;
they are not used as domain models.

## Consequences

CI must add Buf lint, breaking-change detection, and generated-code drift checks
when Buf is available. Runtime loading must verify artifact hash/signature before
constructing domain objects.

