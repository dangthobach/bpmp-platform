$ErrorActionPreference = 'Stop'

cargo clippy -p bpmp-adapter-wasmtime --all-targets -- -D warnings
if ($LASTEXITCODE -ne 0) {
    throw "Wasmtime Clippy gate failed with code $LASTEXITCODE"
}

cargo test -p bpmp-adapter-wasmtime
if ($LASTEXITCODE -ne 0) {
    throw "Wasmtime tests failed with code $LASTEXITCODE"
}
