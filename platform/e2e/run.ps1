[CmdletBinding()]
param(
    [switch]$KeepRunning,
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "../..")).Path
$compose = Join-Path $PSScriptRoot "compose.yaml"
$envFile = Join-Path $PSScriptRoot ".env"
$runtime = Join-Path $PSScriptRoot "runtime"

if (-not (Test-Path $envFile)) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot ".env.example") -Destination $envFile
}

function Invoke-Compose {
    docker compose --env-file $envFile -f $compose @args
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose failed: $($args -join ' ')"
    }
}

function Invoke-JsonRequest {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$Body,
        [Parameter(Mandatory)][hashtable]$Headers
    )
    Invoke-RestMethod `
        -SkipCertificateCheck `
        -Method Post `
        -Uri "https://localhost:$gatewayPort$Path" `
        -Headers $Headers `
        -ContentType "application/json" `
        -Body $Body
}

function Invoke-JsonRequestEventually {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$Body,
        [Parameter(Mandatory)][hashtable]$Headers,
        [int]$TimeoutSeconds = 60
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        try {
            return Invoke-JsonRequest -Path $Path -Body $Body -Headers $Headers
        } catch {
            $statusCode = [int]$_.Exception.Response.StatusCode
            if ($statusCode -notin 502, 503, 504 -or (Get-Date) -ge $deadline) {
                throw
            }
            Start-Sleep -Milliseconds 500
        }
    } while ((Get-Date) -lt $deadline)
    throw "request did not succeed before its retry deadline"
}

$resolvedRuntime = [System.IO.Path]::GetFullPath($runtime)
$resolvedE2E = [System.IO.Path]::GetFullPath($PSScriptRoot)
if (-not $resolvedRuntime.StartsWith($resolvedE2E, [StringComparison]::OrdinalIgnoreCase)) {
    throw "runtime directory escaped the E2E workspace"
}

try {
    Invoke-Compose down --volumes --remove-orphans
    if (Test-Path $runtime) {
        Remove-Item -LiteralPath $runtime -Recurse -Force
    }
    New-Item -ItemType Directory -Path $runtime | Out-Null

    if (-not $SkipBuild) {
        docker build -f (Join-Path $PSScriptRoot "Dockerfile.rust") -t bpmp/e2e-rust:local $root
        if ($LASTEXITCODE -ne 0) { throw "Rust E2E image build failed" }
        docker build -f (Join-Path $PSScriptRoot "Dockerfile.human-runtime") -t bpmp/e2e-human-runtime:local $root
        if ($LASTEXITCODE -ne 0) { throw "Human Runtime E2E image build failed" }
        docker build -f (Join-Path $PSScriptRoot "Dockerfile.api-gateway") -t bpmp/e2e-api-gateway:local $root
        if ($LASTEXITCODE -ne 0) { throw "API Gateway E2E image build failed" }
    }

    Invoke-Compose --profile setup run --rm fixture-generator
    Invoke-Compose up -d

    $settings = Get-Content $envFile | ConvertFrom-StringData
    $gatewayPort = $settings.GATEWAY_PORT
    $deadline = (Get-Date).AddMinutes(3)
    do {
        try {
            $ready = Invoke-WebRequest -SkipCertificateCheck -Uri "https://localhost:$gatewayPort/readyz" -TimeoutSec 2
            if ($ready.StatusCode -eq 200) { break }
        } catch {
            Start-Sleep -Seconds 2
        }
    } while ((Get-Date) -lt $deadline)
    if (-not $ready -or $ready.StatusCode -ne 200) {
        Invoke-Compose ps
        Invoke-Compose logs --no-color --tail 200
        throw "API Gateway did not become ready"
    }

    $token = (Get-Content (Join-Path $runtime "actor.jwt") -Raw).Trim()
    $suffix = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
    $instance = "e2e-$suffix"
    $startHeaders = @{
        Authorization = "Bearer $token"
        "X-BPMP-Tenant-ID" = "tenant-e2e"
        "X-Command-ID" = "start-$suffix"
        "Idempotency-Key" = "start-idem-$suffix"
        "X-Correlation-ID" = "correlation-$suffix"
    }
    $start = Invoke-JsonRequestEventually `
        -Path "/v1/workflows/approval/instances" `
        -Headers $startHeaders `
        -Body (@{ instance_id = $instance; workflow_version = "1"; start_node_id = "start" } | ConvertTo-Json -Compress)
    if ($start.command_id -ne "start-$suffix" -or $start.duplicate) {
        throw "start receipt is invalid"
    }
    $duplicate = Invoke-JsonRequestEventually `
        -Path "/v1/workflows/approval/instances" `
        -Headers $startHeaders `
        -Body (@{ instance_id = $instance; workflow_version = "1"; start_node_id = "start" } | ConvertTo-Json -Compress)
    if (-not $duplicate.duplicate -or $duplicate.committed_sequence -ne $start.committed_sequence) {
        throw "idempotent start did not return the original result"
    }

    $workItem = ""
    $deadline = (Get-Date).AddMinutes(2)
    do {
        $workItem = (Invoke-Compose exec -T postgres psql -U $settings.POSTGRES_USER -d $settings.POSTGRES_DB -Atc "SELECT work_item_id FROM work_items WHERE tenant_id='tenant-e2e' AND instance_id='$instance' AND status='ACTIVE'").Trim()
        if ($workItem) { break }
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $deadline)
    if (-not $workItem) {
        throw "Kafka event was not projected to an active PostgreSQL work item"
    }

    # Force a real Raft leader-loss path. Engine 2 remains the configured API
    # endpoint and must forward to, or become, the new majority leader.
    Invoke-Compose stop engine1

    $completeHeaders = @{
        Authorization = "Bearer $token"
        "X-BPMP-Tenant-ID" = "tenant-e2e"
        "X-Command-ID" = "complete-$suffix"
        "Idempotency-Key" = "complete-idem-$suffix"
        "X-Correlation-ID" = "correlation-$suffix"
    }
    $null = Invoke-JsonRequestEventually `
        -Path "/v1/work-items/$workItem/complete" `
        -Headers $completeHeaders `
        -Body (@{ decision = "approved"; expected_version = 1 } | ConvertTo-Json -Compress)

    $status = ""
    $deadline = (Get-Date).AddMinutes(2)
    do {
        $status = (Invoke-Compose exec -T postgres psql -U $settings.POSTGRES_USER -d $settings.POSTGRES_DB -Atc "SELECT status FROM work_items WHERE tenant_id='tenant-e2e' AND work_item_id='$workItem'").Trim()
        if ($status -eq "COMPLETED") { break }
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $deadline)
    if ($status -ne "COMPLETED") {
        throw "committed completion was not projected back to PostgreSQL"
    }
    $inboxCount = [int](Invoke-Compose exec -T postgres psql -U $settings.POSTGRES_USER -d $settings.POSTGRES_DB -Atc "SELECT count(*) FROM human_event_inbox WHERE tenant_id='tenant-e2e' AND stream_id='$instance'").Trim()
    if ($inboxCount -lt 2) {
        throw "Kafka consumer inbox does not contain both activation and completion"
    }

    Write-Host "Broker-backed E2E passed with leader failover: instance=$instance work_item=$workItem inbox=$inboxCount"
} finally {
    if (-not $KeepRunning) {
        Invoke-Compose down --volumes --remove-orphans
    }
}
