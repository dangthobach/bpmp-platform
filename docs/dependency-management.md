# Dependency Management

Runtime and cryptographic dependencies are pinned or resolved through the
committed root `Cargo.lock`. This workspace contains deployable binaries, so the
lock file is required for reproducible builds even though it also contains
library crates.

Dependabot checks Cargo dependencies weekly. Exact pins such as Wasmtime are
intentional compatibility boundaries; an update pull request must:

1. Review upstream security advisories and release notes for every skipped
   version.
2. Update the exact version and regenerate `Cargo.lock` in one change.
3. Run `tools/check-wasmtime.ps1` and `tools/check.ps1`.
4. Exercise fuel, memory, stack, table, instance, timeout, panic, and host/guest
   round-trip tests.
5. Record any WIT/ABI, determinism, performance, or rollback impact.

Security fixes may use an expedited review, but must not bypass sandbox and
deterministic replay gates.
