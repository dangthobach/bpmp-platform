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
} else {
    $currentSettings = Get-Content $envFile -Raw | ConvertFrom-StringData
    $exampleSettings = Get-Content (Join-Path $PSScriptRoot ".env.example") -Raw | ConvertFrom-StringData
    foreach ($entry in $exampleSettings.GetEnumerator()) {
        if (-not $currentSettings.ContainsKey($entry.Key)) {
            Add-Content -LiteralPath $envFile -Value "$($entry.Key)=$($entry.Value)"
        }
    }
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

function Invoke-GetRequestEventually {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][hashtable]$Headers,
        [int]$TimeoutSeconds = 60
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        try {
            return Invoke-RestMethod `
                -SkipCertificateCheck `
                -Method Get `
                -Uri "https://localhost:$gatewayPort$Path" `
                -Headers $Headers
        } catch {
            $statusCode = [int]$_.Exception.Response.StatusCode
            if ($statusCode -notin 502, 503, 504 -or (Get-Date) -ge $deadline) {
                throw
            }
            Start-Sleep -Milliseconds 500
        }
    } while ((Get-Date) -lt $deadline)
    throw "GET request did not succeed before its retry deadline"
}

function Wait-KafkaConsumerGroup {
    param(
        [Parameter(Mandatory)][string]$ConsumerGroup,
        [int]$TimeoutSeconds = 60
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        $description = Invoke-Compose exec -T redpanda rpk group describe $ConsumerGroup -c
        $rows = @($description | Select-Object -Skip 1 | Where-Object { $_.Trim() })
        $caughtUp = $rows.Count -gt 0
        foreach ($row in $rows) {
            $columns = @($row -split '\s+' | Where-Object { $_ })
            if ($columns.Count -lt 6 -or $columns[5] -notin "0", "-") {
                $caughtUp = $false
                break
            }
        }
        if ($caughtUp) {
            return
        }
        Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $deadline)
    throw "Kafka consumer group '$ConsumerGroup' did not commit through the topic end"
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
        docker build -f (Join-Path $PSScriptRoot "Dockerfile.configuration-service") -t bpmp/e2e-configuration-service:local $root
        if ($LASTEXITCODE -ne 0) { throw "Configuration Service E2E image build failed" }
    }

    Invoke-Compose --profile setup run --rm fixture-generator
    Invoke-Compose up -d

    $settings = Get-Content $envFile -Raw | ConvertFrom-StringData
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
    $configurationHeaders = @{
        Authorization = "Bearer $token"
        "X-BPMP-Tenant-ID" = "tenant-e2e"
        "X-Command-ID" = "configuration-create-$suffix"
        "Idempotency-Key" = "configuration-create-idem-$suffix"
        "X-Correlation-ID" = "configuration-correlation-$suffix"
    }
    $configurationPolicy = @{
        snapshot_interval_events = 100
        max_events_per_decision = 256
        command_timeout_ms = "5000"
        optimistic_conflict_retry = @{
            max_attempts = 5
            initial_backoff_ms = "25"
            max_backoff_ms = "1000"
            multiplier_millis = 2000
        }
        local_wasm = @{
            max_module_bytes = "1048576"
            max_input_bytes = "65536"
            max_output_bytes = "65536"
            max_memory_bytes = "16777216"
            max_wasm_stack_bytes = "1048576"
            max_table_elements = 1024
            max_instances = 4
            max_tables = 4
            max_memories = 2
            fuel = "10000000"
        }
        event_payload_key_scope = "tenant-e2e/operational"
        authorization_audit_key_scope = "tenant-e2e/audit"
        max_multi_instance_cardinality = 1000
        default_multi_instance_parallelism = 32
        boundary_runtime = @{
            projection_batch_size = 64
            dispatch_batch_size = 64
            max_dispatch_attempts = 10
            retry_delay_ms = "250"
            lease_duration_ms = "5000"
            max_timer_horizon_ms = "31536000000"
            max_expression_bytes = 65536
            worker_id = "configuration-e2e"
            max_signal_id_bytes = 256
            max_reference_bytes = 1024
            max_subscriptions_per_instance = 256
        }
    }
    $configuration = Invoke-JsonRequestEventually `
        -Path "/v1/configuration/profiles" `
        -Headers $configurationHeaders `
        -Body (@{
            name = "engine-default-$suffix"
            owner = "ENGINE"
            scope = @{ type = "WORKFLOW_TYPE"; reference = "approval" }
            schema_version = 1
            policy_version = "policy-e2e-v1"
            reason = "broker E2E bootstrap"
            values = $configurationPolicy
        } | ConvertTo-Json -Depth 8 -Compress)
    $queryHeaders = @{
        Authorization = "Bearer $token"
        "X-BPMP-Tenant-ID" = "tenant-e2e"
        "X-Correlation-ID" = "configuration-correlation-$suffix"
    }
    $configurationDetail = Invoke-GetRequestEventually `
        -Path "/v1/configuration/profiles/$($configuration.id)" `
        -Headers $queryHeaders
    $configurationHeaders["X-Command-ID"] = "configuration-publish-$suffix"
    $configurationHeaders["Idempotency-Key"] = "configuration-publish-idem-$suffix"
    $publishedConfiguration = Invoke-JsonRequestEventually `
        -Path "/v1/configuration/profiles/$($configuration.id)/versions/$($configurationDetail.versions[0].id)/publish" `
        -Headers $configurationHeaders `
        -Body (@{ expected_version = 1; reason = "activate broker E2E policy" } | ConvertTo-Json -Compress)
    if (-not $publishedConfiguration.current_published_version_id -or $publishedConfiguration.aggregate_version -ne 2) {
        throw "configuration lifecycle did not publish a version"
    }
    $gatewayConfigurationHeaders = $configurationHeaders.Clone()
    $gatewayConfigurationHeaders["X-Command-ID"] = "gateway-configuration-draft-$suffix"
    $gatewayConfigurationHeaders["Idempotency-Key"] = "gateway-configuration-draft-idem-$suffix"
    $gatewayPolicy = @{
        rate_limit_requests = 1000
        rate_limit_window_ms = "60000"
        upstream_timeout_ms = "3000"
        circuit_breaker_failure_threshold = 5
        circuit_breaker_open_ms = "1000"
        bulkhead_max_concurrency = 128
        max_request_body_bytes = "65536"
        max_upstream_response_bytes = "1048576"
        batch_chunk_size = 100
        batch_concurrency = 4
    }
    $configurationProfiles = Invoke-GetRequestEventually `
        -Path "/v1/configuration/profiles?page_size=100" `
        -Headers $queryHeaders
    $gatewaySeedProfile = @($configurationProfiles.profiles | Where-Object {
        $_.owner -eq "API_GATEWAY"
    })
    $humanSeedProfile = @($configurationProfiles.profiles | Where-Object {
        $_.owner -eq "HUMAN_RUNTIME"
    })
    if ($gatewaySeedProfile.Count -ne 1 -or $humanSeedProfile.Count -ne 1) {
        throw "seeded runtime configuration profiles are not unique"
    }
    $gatewayDraft = Invoke-JsonRequestEventually `
        -Path "/v1/configuration/profiles/$($gatewaySeedProfile[0].id)/versions" `
        -Headers $gatewayConfigurationHeaders `
        -Body (@{
            expected_version = $gatewaySeedProfile[0].aggregate_version
            schema_version = 1
            policy_version = "policy-api-gateway-e2e-v1"
            reason = "broker E2E runtime cache"
            values = $gatewayPolicy
        } | ConvertTo-Json -Depth 5 -Compress)
    $gatewayProfileDetail = Invoke-GetRequestEventually `
        -Path "/v1/configuration/profiles/$($gatewaySeedProfile[0].id)" `
        -Headers $queryHeaders
    $gatewayConfigurationHeaders["X-Command-ID"] = "gateway-configuration-publish-$suffix"
    $gatewayConfigurationHeaders["Idempotency-Key"] = "gateway-configuration-publish-idem-$suffix"
    $gatewayPublished = Invoke-JsonRequestEventually `
        -Path "/v1/configuration/profiles/$($gatewaySeedProfile[0].id)/versions/$($gatewayProfileDetail.versions[0].id)/publish" `
        -Headers $gatewayConfigurationHeaders `
        -Body (@{ expected_version = $gatewayDraft.aggregate_version; reason = "activate Gateway runtime policy" } | ConvertTo-Json -Compress)

    $humanConfigurationHeaders = $configurationHeaders.Clone()
    $humanConfigurationHeaders["X-Command-ID"] = "human-configuration-draft-$suffix"
    $humanConfigurationHeaders["Idempotency-Key"] = "human-configuration-draft-idem-$suffix"
    $humanPolicy = @{
        projection_batch_size = 64
        escalation_batch_size = 32
        escalation_lease_ms = "5000"
        escalation_retry_ms = "1000"
        escalation_poll_ms = "250"
        engine_command_timeout_ms = "3000"
        max_assignment_candidates = 100
        max_delegation_depth = 8
    }
    $humanDraft = Invoke-JsonRequestEventually `
        -Path "/v1/configuration/profiles/$($humanSeedProfile[0].id)/versions" `
        -Headers $humanConfigurationHeaders `
        -Body (@{
            expected_version = $humanSeedProfile[0].aggregate_version
            schema_version = 1
            policy_version = "policy-human-runtime-e2e-v1"
            reason = "broker E2E runtime cache"
            values = $humanPolicy
        } | ConvertTo-Json -Depth 5 -Compress)
    $humanProfileDetail = Invoke-GetRequestEventually `
        -Path "/v1/configuration/profiles/$($humanSeedProfile[0].id)" `
        -Headers $queryHeaders
    $humanConfigurationHeaders["X-Command-ID"] = "human-configuration-publish-$suffix"
    $humanConfigurationHeaders["Idempotency-Key"] = "human-configuration-publish-idem-$suffix"
    $humanPublished = Invoke-JsonRequestEventually `
        -Path "/v1/configuration/profiles/$($humanSeedProfile[0].id)/versions/$($humanProfileDetail.versions[0].id)/publish" `
        -Headers $humanConfigurationHeaders `
        -Body (@{ expected_version = $humanDraft.aggregate_version; reason = "activate Human Runtime policy" } | ConvertTo-Json -Compress)
    if (
        -not $gatewayPublished.current_published_version_id -or
        -not $humanPublished.current_published_version_id
    ) {
        throw "runtime cache owner profiles were not published"
    }

    $engineConfigurations = 1..3 | ForEach-Object {
        Get-Content (Join-Path $runtime "engine-$_.json") -Raw | ConvertFrom-Json
    }
    $gatewayConfiguration = Get-Content (Join-Path $runtime "api-gateway.json") -Raw | ConvertFrom-Json
    $humanConfiguration = Get-Content (Join-Path $runtime "human-runtime.json") -Raw | ConvertFrom-Json
    $consumerGroups = @($engineConfigurations | ForEach-Object {
        $_.kafka.consumer_groups.configuration_reloader
    })
    $consumerGroups += $gatewayConfiguration.runtime_configuration.kafka.consumer_group
    $consumerGroups += $humanConfiguration.runtime_configuration.kafka.consumer_group
    $consumerGroups | ForEach-Object {
        Wait-KafkaConsumerGroup -ConsumerGroup $_
    }

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

    Write-Host "Broker-backed E2E passed with dynamic configuration and leader failover: config=$($configuration.id) instance=$instance work_item=$workItem inbox=$inboxCount"
} finally {
    if (-not $KeepRunning) {
        Invoke-Compose down --volumes --remove-orphans
    }
}
