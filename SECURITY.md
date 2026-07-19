# Security Policy

## Reporting a Vulnerability

Do not open a public issue for a suspected vulnerability. Use GitHub private
vulnerability reporting for this repository:

https://github.com/dangthobach/bpmp-platform/security/advisories/new

Include the affected component and revision, impact, reproduction steps, and
any suggested mitigation. Remove credentials, plaintext regulated data,
private keys, and access tokens from the report.

The maintainers aim to acknowledge a report within three business days,
provide an initial assessment within seven business days, and coordinate a
fix and disclosure timeline according to severity. These are response targets,
not a guarantee.

## Scope

Security-sensitive areas include tenant isolation, authorization proofs and
policy bundles, event/snapshot encryption, KMS and key revocation, RocksDB/Raft
durability, Kafka outbox publication, WASM sandboxing, JWT/JWKS verification,
audit integrity, and compiler parsing of untrusted BPMN/DMN/CMMN input.

Only the latest revision of the default branch is supported until formal
release branches are published. Dependency security updates must preserve
replay compatibility and pass the repository quality gates before release.
