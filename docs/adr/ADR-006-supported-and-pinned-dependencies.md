# ADR-006: Supported, pinned, and qualified production dependencies

- Status: Accepted
- Date: 2026-10-30

## Context

BPMP depends on language toolchains, consensus/storage libraries, databases,
Kafka, KMS, identity, Kubernetes, gateways, and observability components. Blind
automatic upgrades can introduce wire, disk, replay, performance, or security
regressions. Permanently frozen dependencies create a different security and
support risk.

## Decision

Production uses supported/LTS releases selected in
`microservices-architecture.md` and pinned by toolchain files, lockfiles, image
digests, Helm values, or environment ADRs as appropriate. “Newest” is not an
upgrade criterion. Automated tooling may open an upgrade change, but promotion
requires compatibility, security, replay/durability, and performance evidence
proportional to the dependency's blast radius.

Qualification includes, as applicable:

- compiler/linter/test qualification for Rust, Go, TypeScript, and generated code;
- Buf lint/breaking and clean regeneration for Protobuf tooling;
- old-version fixture load, deterministic replay, restart, snapshot, backup, and
  rollback/roll-forward evidence for durable formats;
- Raft partition/leader-change model and chaos tests;
- PostgreSQL migration, query-plan, failover, and backup/restore tests;
- Kafka duplicate/reorder/crash/checkpoint and broker compatibility tests;
- KMS/identity key rotation, revocation, outage, and fail-closed tests;
- representative p95/p99, allocation/memory, saturation, and soak comparisons;
- SBOM, provenance/signature, image/dependency advisory, and license gates.

Production upgrades are staged and reversible where the underlying format
permits it. An irreversible disk/schema/protocol migration requires an explicit
roll-forward and recovery plan before promotion. Security fixes may accelerate
the schedule, but they do not waive correctness or compatibility gates.

## Failure behavior

An upgrade that fails a required gate is not promoted. A vulnerable current
version triggers risk assessment, compensating controls, and a qualified patch
path rather than an untested production jump. Unsupported components are
replaced through an explicit migration ADR and compatibility window.

## Consequences

Dependency versions remain visible and reproducible. Upgrade changes include
the relevant test/benchmark evidence and operational rollback notes. The team
accepts deliberate qualification cost in exchange for preserving durable data,
determinism, tenant isolation, and availability.
