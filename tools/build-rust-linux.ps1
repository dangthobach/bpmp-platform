[CmdletBinding()]
param(
    [string[]]$CargoArguments = @("test", "-p", "bpmp-adapter-rocksdb", "--lib"),
    [string]$ContainerName = "bpmp-rust-build",
    [string]$TargetVolume = "bpmp-cargo-target",
    [switch]$ResetTargetCache
)

$ErrorActionPreference = "Stop"
$workspace = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path

docker info *> $null
if ($LASTEXITCODE -ne 0) {
    throw "Docker Linux daemon is unavailable"
}

$runningBuilds = docker ps --filter "name=^/$ContainerName$" --format "{{.Names}}"
if ($ResetTargetCache) {
    if ($runningBuilds -eq $ContainerName) {
        $cargoProcesses = docker exec $ContainerName sh -c "pgrep -x cargo || true"
        if ($cargoProcesses) {
            throw "Refusing to reset target cache while cargo is running in $ContainerName"
        }
        docker rm -f $ContainerName | Out-Null
    }
    docker volume rm $TargetVolume 2>$null | Out-Null
}

$existing = docker ps -a --filter "name=^/$ContainerName$" --format "{{.Names}}"
if (-not $existing) {
    docker volume create $TargetVolume | Out-Null
    docker run -d `
        --name $ContainerName `
        --mount "type=bind,source=$workspace,target=/work" `
        --mount "type=volume,source=$TargetVolume,target=/target" `
        --workdir /work `
        --env CARGO_TARGET_DIR=/target `
        --env PATH=/usr/local/cargo/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin `
        rust:1.91.1-bookworm sleep infinity | Out-Null
    docker exec $ContainerName bash -c "apt-get update -qq && apt-get install -y -qq clang cmake libclang-dev >/dev/null"
} elseif (-not $runningBuilds) {
    docker start $ContainerName | Out-Null
}

$activeCargo = docker exec $ContainerName sh -c "pgrep -x cargo || true"
if ($activeCargo) {
    throw "A Rust build is already running in $ContainerName; refusing to wait on Cargo's target lock"
}

docker exec $ContainerName cargo @CargoArguments
exit $LASTEXITCODE
