[CmdletBinding(SupportsShouldProcess)]
param(
    [switch]$HostRustTarget,
    [switch]$LegacyLinuxTarget,
    [switch]$DockerLinuxTarget,
    [string]$ContainerName = "bpmp-rust-build",
    [string]$TargetVolume = "bpmp-cargo-target"
)

$ErrorActionPreference = "Stop"
$workspace = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path

function Remove-WorkspaceDirectory {
    param([string]$RelativePath)
    $candidate = [System.IO.Path]::GetFullPath((Join-Path $workspace $RelativePath))
    $prefix = $workspace.TrimEnd([System.IO.Path]::DirectorySeparatorChar) + [System.IO.Path]::DirectorySeparatorChar
    if (-not $candidate.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to remove path outside workspace: $candidate"
    }
    if ((Test-Path -LiteralPath $candidate) -and $PSCmdlet.ShouldProcess($candidate, "Remove build cache")) {
        Remove-Item -LiteralPath $candidate -Recurse -Force
    }
}

function Assert-NoDockerBindMount {
    param([string]$TargetPath)
    docker info *> $null
    if ($LASTEXITCODE -ne 0) {
        return
    }
    $resolvedTarget = [System.IO.Path]::GetFullPath($TargetPath)
    foreach ($containerId in (docker ps -q)) {
        $sources = docker inspect --format "{{range .Mounts}}{{println .Source}}{{end}}" $containerId
        foreach ($source in $sources) {
            if ($source -and [System.IO.Path]::GetFullPath($source) -eq $resolvedTarget) {
                $name = docker inspect --format "{{.Name}}" $containerId
                throw "Refusing to remove $resolvedTarget while mounted by running container $name"
            }
        }
    }
}

if ($HostRustTarget) {
    if (Get-Process cargo, rustc -ErrorAction SilentlyContinue) {
        throw "Refusing to remove host target while Rust build processes are running"
    }
    Remove-WorkspaceDirectory "target"
}

if ($LegacyLinuxTarget) {
    if (Get-Process cargo, rustc -ErrorAction SilentlyContinue) {
        throw "Refusing to remove legacy Linux target while Rust build processes are running"
    }
    Assert-NoDockerBindMount (Join-Path $workspace "target-linux")
    Remove-WorkspaceDirectory "target-linux"
}

if ($DockerLinuxTarget) {
    docker info *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "Docker Linux daemon is unavailable"
    }
    $container = docker ps -a --filter "name=^/$ContainerName$" --format "{{.Names}}"
    if ($container) {
        $running = docker ps --filter "name=^/$ContainerName$" --format "{{.Names}}"
        if ($running) {
            $cargoProcesses = docker exec $ContainerName sh -c "pgrep -x cargo || true"
            if ($cargoProcesses) {
                throw "Refusing to remove Docker target volume while cargo is running"
            }
        }
        if ($PSCmdlet.ShouldProcess($ContainerName, "Remove Linux builder container")) {
            docker rm -f $ContainerName | Out-Null
        }
    }
    if ($PSCmdlet.ShouldProcess($TargetVolume, "Remove Docker target volume")) {
        docker volume rm $TargetVolume | Out-Null
    }
}
