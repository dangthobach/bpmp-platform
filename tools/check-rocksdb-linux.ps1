$ErrorActionPreference = 'Stop'

$image = 'bpmp-rust-rocksdb-ci:1.91.1'
docker build `
    --file platform/local/Dockerfile.rust-rocksdb-ci `
    --tag $image `
    platform/local
if ($LASTEXITCODE -ne 0) {
    throw "Docker image build failed with code $LASTEXITCODE"
}

docker run --rm `
    --volume "${PWD}:/workspace" `
    --volume 'bpmp-cargo-registry:/usr/local/cargo/registry' `
    --volume 'bpmp-cargo-target-linux:/workspace/target' `
    --workdir /workspace `
    $image `
    cargo test -p bpmp-adapter-rocksdb --lib
if ($LASTEXITCODE -ne 0) {
    throw "Linux RocksDB tests failed with code $LASTEXITCODE"
}
