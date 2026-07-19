# Contributing

## Development Setup

Install the Rust toolchain declared in `rust-toolchain.toml` and Buf. On
Windows, Buf can be installed with:

```powershell
winget install --id bufbuild.buf --exact
```

Keep configuration externalized and versioned. Deterministic domain code must
not read wall clock, randomness, network, database, filesystem, or environment
state. Tenant scope is mandatory across durable contracts and persistence keys.

## Changes

- Keep business rules in the owning bounded context and I/O in adapters.
- Add Protobuf fields compatibly; never reuse removed field numbers.
- Add unit tests for examples and property tests for universal invariants.
- Tag requirement properties as documented in `tests/README.md`.
- Do not commit secrets, generated credentials, or regulated data.

Run the full gate before submitting a change:

```powershell
.\tools\check.ps1
```

For changes to the Wasmtime adapter, also run:

```powershell
.\tools\check-wasmtime.ps1
```

Protocol changes must pass both `buf lint` and the checked-in breaking-change
baseline. Explain durable schema, migration, replay, and rollback implications
in the pull request.
