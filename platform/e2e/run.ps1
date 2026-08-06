[CmdletBinding()]
param(
    [switch]$KeepRunning,
    [switch]$SkipBuild,
    [ValidateRange(10, 600)]
    [int]$StartupTimeoutSeconds = 90
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "../..")).Path
$compose = Join-Path $PSScriptRoot "compose.yaml"
$envFile = Join-Path $PSScriptRoot ".env"
$runtime = Join-Path $PSScriptRoot "runtime"
$realtimeProcess = $null
$curlExecutable = if ($IsWindows) { "curl.exe" } else { "curl" }

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

$bootstrapManifest = Get-Content (Join-Path $PSScriptRoot "manifest.json") -Raw | ConvertFrom-Json
$expectedPostgresDatabase = ([System.Uri]$bootstrapManifest.postgres_dsn).AbsolutePath.Trim('/')
if (-not $expectedPostgresDatabase) {
    throw "manifest postgres_dsn must include a database name"
}
$envLines = Get-Content $envFile
$databaseSettingFound = $false
$envLines = @($envLines | ForEach-Object {
    if ($_ -match '^POSTGRES_DB=') {
        $databaseSettingFound = $true
        "POSTGRES_DB=$expectedPostgresDatabase"
    } else {
        $_
    }
})
if (-not $databaseSettingFound) {
    $envLines += "POSTGRES_DB=$expectedPostgresDatabase"
}
Set-Content -LiteralPath $envFile -Value $envLines

function Invoke-Compose {
    docker compose --env-file $envFile -f $compose @args
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose failed: $($args -join ' ')"
    }
}

function Invoke-TimedPhase {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][scriptblock]$Action
    )
    $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    Write-Host "==> $Name"
    try {
        & $Action
    } finally {
        $stopwatch.Stop()
        Write-Host ("<== {0}: {1:n1}s" -f $Name, $stopwatch.Elapsed.TotalSeconds)
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
    Invoke-TimedPhase -Name "Clean previous E2E state" -Action {
        Invoke-Compose down --volumes --remove-orphans
        if (Test-Path $runtime) {
            try {
                Remove-Item -LiteralPath $runtime -Recurse -Force
            } catch {
                # Files written through the bind mount can be owned by the
                # container's remapped root user on Linux. Let that same image
                # clear its output before removing the host directory.
                Invoke-Compose --profile setup run --rm --no-deps `
                    --entrypoint /bin/sh fixture-generator `
                    -c "rm -rf /runtime/*"
                Remove-Item -LiteralPath $runtime -Recurse -Force
            }
        }
        New-Item -ItemType Directory -Path $runtime | Out-Null
    }

    if (-not $SkipBuild) {
        Invoke-TimedPhase -Name "Build E2E images" -Action {
            # The Rust Dockerfiles share Cargo cache mounts. Serializing the
            # image build avoids concurrent unpack races in the shared cache.
            Invoke-Compose --parallel 1 --profile setup build
        }
    }

    Invoke-TimedPhase -Name "Generate E2E fixtures" -Action {
        Invoke-Compose --profile setup run --rm fixture-generator
    }
    try {
        Invoke-TimedPhase -Name "Start and health-check E2E infrastructure" -Action {
            Invoke-Compose up -d --wait --wait-timeout $StartupTimeoutSeconds `
                postgres authz-postgres redis redpanda otel-collector key-lifecycle
        }
        Invoke-TimedPhase -Name "Start and health-check E2E applications" -Action {
            Invoke-Compose up -d --wait --wait-timeout $StartupTimeoutSeconds
        }
    } catch {
        Invoke-Compose ps
        Invoke-Compose logs --no-color --tail 200
        throw
    }

    $settings = Get-Content $envFile -Raw | ConvertFrom-StringData
    $manifestSettings = Get-Content (Join-Path $PSScriptRoot "manifest.json") -Raw | ConvertFrom-Json
    $tenantId = $manifestSettings.tenant_id
    $gatewayPort = $settings.GATEWAY_PORT
    $governancePort = $settings.GOVERNANCE_PORT
    $cockpitGatewayPort = $settings.COCKPIT_GATEWAY_PORT
    $cockpitWebPort = $settings.COCKPIT_WEB_PORT
    $token = (Get-Content (Join-Path $runtime "actor.jwt") -Raw).Trim()
    $suffix = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
    $cockpitPage = Invoke-WebRequest `
        -Method Get `
        -Uri "http://localhost:$cockpitWebPort/"
    $cockpitRuntime = Invoke-RestMethod `
        -Method Get `
        -Uri "http://localhost:$cockpitWebPort/config.json"
    $cockpitScriptPath = [regex]::Match($cockpitPage.Content, 'src="([^"]+\.js)"').Groups[1].Value
    $cockpitStylesheetPath = [regex]::Match($cockpitPage.Content, 'href="([^"]+\.css)"').Groups[1].Value
    if (
        $cockpitPage.StatusCode -ne 200 -or
        $cockpitPage.Content -notmatch '<div id="root"></div>' -or
        $cockpitRuntime.realtimePath -ne "/realtime/v1/events" -or
        -not $cockpitScriptPath -or
        -not $cockpitStylesheetPath
    ) {
        throw "Cockpit Web artifact or runtime configuration is unavailable"
    }
    $cockpitScript = Invoke-WebRequest `
        -Method Head `
        -Uri "http://localhost:$cockpitWebPort$cockpitScriptPath"
    $cockpitStylesheet = Invoke-WebRequest `
        -Method Head `
        -Uri "http://localhost:$cockpitWebPort$cockpitStylesheetPath"
    if (
        $cockpitScript.Headers.'Content-Type' -notmatch 'javascript' -or
        $cockpitStylesheet.Headers.'Content-Type' -notmatch '^text/css'
    ) {
        throw "Cockpit Web JavaScript or stylesheet MIME type is invalid"
    }
    $configurationPage = Invoke-RestMethod `
        -Method Get `
        -Uri "http://localhost:$cockpitWebPort/v1/configuration/profiles?page_size=50" `
        -Headers @{
            Authorization = "Bearer $token"
            "X-BPMP-Tenant-ID" = $tenantId
            "X-Correlation-ID" = "cockpit-configuration-$suffix"
        }
    if ($null -eq $configurationPage.profiles) {
        throw "Cockpit Web configuration facade is unavailable"
    }
    $uuidV4Pattern = '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$'
    foreach ($profile in @($configurationPage.profiles)) {
        if ($profile.id -notmatch $uuidV4Pattern) {
            throw "Cockpit Web configuration profile '$($profile.id)' is not a valid UUID v4"
        }
        foreach ($version in @($profile.current_version, $profile.latest_version)) {
            if ($null -ne $version -and $version.id -notmatch $uuidV4Pattern) {
                throw "Cockpit Web configuration version '$($version.id)' is not a valid UUID v4"
            }
        }
    }
    $organizationPage = Invoke-RestMethod `
        -Method Get `
        -Uri "http://localhost:$cockpitWebPort/api/v1/organizations?offset=0&limit=50" `
        -Headers @{
            Authorization = "Bearer $token"
            "X-Tenant-ID" = $tenantId
            "X-Request-ID" = "cockpit-organization-$suffix"
        }
    if ($organizationPage.error_code -ne "OK" -or $null -eq $organizationPage.data) {
        throw "Cockpit Web organization facade is unavailable"
    }
    $openAPI = Invoke-RestMethod `
        -SkipCertificateCheck `
        -Method Get `
        -Uri "https://localhost:$gatewayPort/openapi/v1.json"
    if ($openAPI.openapi -ne "3.1.0" -or
        @($openAPI.paths.PSObject.Properties).Count -ne 16) {
        throw "API Gateway OpenAPI contract is unavailable or incomplete"
    }
    $apiReference = Invoke-WebRequest `
        -SkipCertificateCheck `
        -Method Get `
        -Uri "https://localhost:$gatewayPort/docs"
    if ($apiReference.StatusCode -ne 200 -or
        $apiReference.Content -notmatch 'data-url="/openapi/v1.json"') {
        throw "API Gateway interactive API reference is unavailable"
    }
    $reflectedServices = & buf curl `
        --protocol grpc `
        --list-services `
        --cacert (Join-Path $runtime "secrets/ca.pem") `
        --cert (Join-Path $runtime "secrets/tls.pem") `
        --key (Join-Path $runtime "secrets/tls-key.pem") `
        --servername governance-service `
        "https://localhost:$governancePort"
    if ($LASTEXITCODE -ne 0 -or
        $reflectedServices -notcontains "bpmp.governance.v1.GovernanceApprovalService" -or
        $reflectedServices -contains "bpmp.raft.v1.RaftPeerService") {
        throw "Governance gRPC reflection is unavailable"
    }
    $configurationHeaders = @{
        Authorization = "Bearer $token"
        "X-BPMP-Tenant-ID" = $tenantId
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
        event_payload_key_scope = "$tenantId/operational"
        authorization_audit_key_scope = "$tenantId/audit"
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
        workers = @{
            poll_interval_ms = "100"
            outbox_batch_size = 64
            outbox_retry = @{
                max_attempts = 10
                initial_backoff_ms = "50"
                max_backoff_ms = "1000"
                multiplier_millis = 2000
            }
            local_task_batch_size = 32
            local_task_retry = @{
                max_attempts = 3
                initial_backoff_ms = "25"
                max_backoff_ms = "250"
                multiplier_millis = 2000
            }
            remote = @{
                dispatch_batch_size = 32
                max_workers = 100000
                max_credit_per_worker = 64
                max_capabilities_per_worker = 32
                lease_duration_ms = "30000"
                heartbeat_timeout_ms = "10000"
                max_identifier_bytes = 256
                max_protocol_version_bytes = 32
                stream_channel_capacity = 128
                max_input_bytes = "65536"
                max_output_bytes = "65536"
                max_attempts = 5
                retry_delay_ms = "1000"
            }
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
        "X-BPMP-Tenant-ID" = $tenantId
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
        upstream_retry = @{
            max_attempts = 3
            initial_backoff_ms = "25"
            max_backoff_ms = "250"
            multiplier_millis = 2000
        }
        encryption_key_scope = $configurationPolicy.event_payload_key_scope
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
        query_default_page_size = 50
        query_max_page_size = 200
        engine_retry = @{
            max_attempts = 5
            initial_backoff_ms = "50"
            max_backoff_ms = "1000"
            multiplier_millis = 2000
        }
        engine_circuit_breaker_failure_threshold = 5
        engine_circuit_breaker_open_ms = "1000"
        engine_retryable_codes = @("UNAVAILABLE", "DEADLINE_EXCEEDED")
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
    $projectionConfiguration = Get-Content (Join-Path $runtime "projection-service.json") -Raw | ConvertFrom-Json
    $cockpitConfiguration = Get-Content (Join-Path $runtime "cockpit-gateway.json") -Raw | ConvertFrom-Json
    $governanceConfiguration = Get-Content (Join-Path $runtime "governance-service.json") -Raw | ConvertFrom-Json
    $consumerGroups = @($engineConfigurations | ForEach-Object {
        $_.kafka.consumer_groups.configuration_reloader
    })
    $consumerGroups += $gatewayConfiguration.runtime_configuration.kafka.consumer_group
    $consumerGroups += $humanConfiguration.runtime_configuration.kafka.consumer_group
    $consumerGroups += $projectionConfiguration.runtime_configuration.kafka.consumer_group
    $consumerGroups += $cockpitConfiguration.kafka.consumer_group
    $consumerGroups += $governanceConfiguration.kafka.consumer_group
    $consumerGroups | ForEach-Object {
        Wait-KafkaConsumerGroup -ConsumerGroup $_
    }

    $instance = "e2e-$suffix"
    $realtimeOutput = Join-Path $runtime "cockpit-realtime.txt"
    $realtimeCurlConfig = Join-Path $runtime "cockpit-realtime.curl"
    @"
insecure
silent
show-error
no-buffer
max-time = 90
header = "Authorization: Bearer $token"
header = "X-BPMP-Tenant-ID: $tenantId"
header = "X-Correlation-ID: realtime-$suffix"
url = "https://localhost:$cockpitGatewayPort/realtime/v1/events?names=workflow.changed,work-item.changed"
"@ | Set-Content -LiteralPath $realtimeCurlConfig -Encoding utf8NoBOM
    $realtimeProcessArguments = @{
        FilePath = $curlExecutable
        ArgumentList = @("--config", $realtimeCurlConfig)
        RedirectStandardOutput = $realtimeOutput
        RedirectStandardError = Join-Path $runtime "cockpit-realtime-error.txt"
        PassThru = $true
    }
    if ($IsWindows) {
        $realtimeProcessArguments.WindowStyle = "Hidden"
    }
    $realtimeProcess = Start-Process @realtimeProcessArguments
    $startHeaders = @{
        Authorization = "Bearer $token"
        "X-BPMP-Tenant-ID" = $tenantId
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

    $governanceRequestId = "governance-$suffix"
    $governanceCreatedAt = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
    $governancePayload = @{
        request_id = $governanceRequestId
        idempotency_key = "governance-create-idem-$suffix"
        created_at_epoch_ms = "$governanceCreatedAt"
        spec = @{
            tenant_id = $tenantId
            instance_id = $instance
            workflow_type = "approval"
            workflow_version = "1"
            policy_id = "abort-and-reconcile-e2e"
            legal_deadline_epoch_ms = "$($governanceCreatedAt + 3600000)"
            key_scope = "$tenantId/subject-$suffix"
            key_epoch = "1"
            reason_code = "E2E_GOVERNANCE_SMOKE"
        }
    } | ConvertTo-Json -Depth 4 -Compress
    $governanceResponseJson = & buf curl `
        --schema (Join-Path $root "contracts/proto") `
        --protocol grpc `
        --cacert (Join-Path $runtime "secrets/ca.pem") `
        --cert (Join-Path $runtime "secrets/tls.pem") `
        --key (Join-Path $runtime "secrets/tls-key.pem") `
        --servername governance-service `
        -d $governancePayload `
        "https://localhost:$governancePort/bpmp.governance.v1.GovernanceApprovalService/CreateApproval"
    if ($LASTEXITCODE -ne 0) {
        throw "Governance CreateApproval gRPC request failed"
    }
    $governanceResponse = $governanceResponseJson | ConvertFrom-Json
    if (
        $governanceResponse.approval.requestId -ne $governanceRequestId -or
        $governanceResponse.approval.status -ne "APPROVAL_REQUEST_STATUS_PENDING" -or
        -not $governanceResponse.approval.requestDigest
    ) {
        throw "Governance CreateApproval returned an invalid durable approval"
    }
    $governanceRequestCount = [int](Invoke-Compose exec -T postgres psql -U $settings.POSTGRES_USER -d $settings.POSTGRES_DB -Atc "SELECT count(*) FROM governance.governance_approval_requests WHERE tenant_id='$tenantId' AND request_id='$governanceRequestId'").Trim()
    $governanceAuditCount = [int](Invoke-Compose exec -T postgres psql -U $settings.POSTGRES_USER -d $settings.POSTGRES_DB -Atc "SELECT count(*) FROM governance.governance_service_audit WHERE tenant_id='$tenantId' AND request_id='$governanceRequestId' AND action='CREATED'").Trim()
    if ($governanceRequestCount -ne 1 -or $governanceAuditCount -ne 1) {
        throw "Governance approval or immutable creation audit was not committed"
    }

    $workItem = ""
    $deadline = (Get-Date).AddMinutes(2)
    do {
        $workItem = (Invoke-Compose exec -T postgres psql -U $settings.POSTGRES_USER -d $settings.POSTGRES_DB -Atc "SELECT work_item_id FROM human_runtime.work_items WHERE tenant_id='$tenantId' AND instance_id='$instance' AND status='ACTIVE'").Trim()
        if ($workItem) { break }
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $deadline)
    if (-not $workItem) {
        throw "Kafka event was not projected to an active PostgreSQL work item"
    }
    $realtimeDeadline = (Get-Date).AddSeconds(30)
    do {
        $realtimeContent = if (Test-Path $realtimeOutput) {
            Get-Content $realtimeOutput -Raw
        } else {
            ""
        }
        if (
            $realtimeContent -match "event: workflow.changed" -and
            $realtimeContent -match "event: work-item.changed" -and
            $realtimeContent -match [regex]::Escape($instance)
        ) {
            break
        }
        Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $realtimeDeadline)
    if (
        $realtimeContent -notmatch "event: workflow.changed" -or
        $realtimeContent -notmatch "event: work-item.changed" -or
        $realtimeContent -notmatch [regex]::Escape($instance)
    ) {
        throw "Cockpit Gateway did not fan out the committed workflow event"
    }
    if (-not $realtimeProcess.HasExited) {
        Stop-Process -Id $realtimeProcess.Id
    }

    # Force a real Raft leader-loss path. Engine 2 remains the configured API
    # endpoint and must forward to, or become, the new majority leader.
    Invoke-Compose stop engine1

    $completeHeaders = @{
        Authorization = "Bearer $token"
        "X-BPMP-Tenant-ID" = $tenantId
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
        $status = (Invoke-Compose exec -T postgres psql -U $settings.POSTGRES_USER -d $settings.POSTGRES_DB -Atc "SELECT status FROM human_runtime.work_items WHERE tenant_id='$tenantId' AND work_item_id='$workItem'").Trim()
        if ($status -eq "COMPLETED") { break }
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $deadline)
    if ($status -ne "COMPLETED") {
        throw "committed completion was not projected back to PostgreSQL"
    }
    $inboxCount = [int](Invoke-Compose exec -T postgres psql -U $settings.POSTGRES_USER -d $settings.POSTGRES_DB -Atc "SELECT count(*) FROM human_runtime.human_event_inbox WHERE tenant_id='$tenantId' AND stream_id='$instance'").Trim()
    if ($inboxCount -lt 2) {
        throw "Kafka consumer inbox does not contain both activation and completion"
    }
    $projectionStatus = ""
    $deadline = (Get-Date).AddMinutes(2)
    do {
        $projectionStatus = (Invoke-Compose exec -T postgres psql -U $settings.POSTGRES_USER -d $settings.POSTGRES_DB -Atc "SELECT status FROM projection.workflow_instance_read_models WHERE tenant_id='$tenantId' AND instance_id='$instance'").Trim()
        if ($projectionStatus -eq "COMPLETED") { break }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    if ($projectionStatus -ne "COMPLETED") {
        throw "workflow instance was not projected to the durable query read model"
    }
    $projectionInboxCount = [int](Invoke-Compose exec -T postgres psql -U $settings.POSTGRES_USER -d $settings.POSTGRES_DB -Atc "SELECT count(*) FROM projection.projection_event_inbox WHERE tenant_id='$tenantId' AND instance_id='$instance'").Trim()
    if ($projectionInboxCount -lt 3) {
        throw "projection inbox does not contain the committed workflow lifecycle"
    }

    Write-Host "Broker-backed E2E passed with Projection, Governance and leader failover: config=$($configuration.id) governance=$governanceRequestId instance=$instance work_item=$workItem human_inbox=$inboxCount projection_inbox=$projectionInboxCount"
} finally {
    if ($null -ne $realtimeProcess -and -not $realtimeProcess.HasExited) {
        Stop-Process -Id $realtimeProcess.Id
    }
    if (-not $KeepRunning) {
        Invoke-Compose down --volumes --remove-orphans
    }
}
