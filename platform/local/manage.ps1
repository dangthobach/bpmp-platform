[CmdletBinding()]
param(
    [ValidateSet("init", "up", "migrate", "verify", "status", "logs", "down", "reset")]
    [string]$Action = "up",
    [string]$Service,
    [switch]$ConfirmReset
)

$ErrorActionPreference = "Stop"
$composeFile = Join-Path $PSScriptRoot "compose.infrastructure.yaml"
$environmentFile = Join-Path $PSScriptRoot ".env"
$environmentExample = Join-Path $PSScriptRoot ".env.example"

function Invoke-Compose {
    param([Parameter(Mandatory)][string[]]$Arguments)

    & docker compose --env-file $environmentFile --file $composeFile @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose failed with exit code $LASTEXITCODE"
    }
}

function Initialize-Environment {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        throw "Docker CLI is not available"
    }
    & docker info *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "Docker Engine is not running"
    }
    if (-not (Test-Path -LiteralPath $environmentFile)) {
        Copy-Item -LiteralPath $environmentExample -Destination $environmentFile
        Write-Host "Created $environmentFile. Review local credentials and ports before shared use."
    }
    Invoke-Compose @("config", "--quiet")
}

function Start-Datastores {
    Invoke-Compose @(
        "up", "--detach", "--wait",
        "human-postgres",
        "configuration-postgres",
        "projection-postgres",
        "governance-postgres",
        "redpanda",
        "redis",
        "otel-collector"
    )
}

function Invoke-Migrations {
    $jobs = @(
        "human-migrate",
        "configuration-migrate",
        "projection-migrate",
        "governance-migrate"
    )
    foreach ($job in $jobs) {
        Invoke-Compose @("--profile", "ddl", "run", "--rm", $job)
    }
}

function Initialize-KafkaTopics {
    Invoke-Compose @("--profile", "setup", "run", "--rm", "kafka-topic-init")
}

Initialize-Environment

switch ($Action) {
    "init" {
        Write-Host "Local environment configuration is valid."
    }
    "up" {
        Start-Datastores
        Invoke-Migrations
        Initialize-KafkaTopics
        Invoke-Compose @("ps")
    }
    "migrate" {
        Start-Datastores
        Invoke-Migrations
    }
    "verify" {
        Start-Datastores
        Invoke-Migrations
        Initialize-KafkaTopics
        Invoke-Compose @("ps")
    }
    "status" {
        Invoke-Compose @("ps", "--all")
    }
    "logs" {
        if ([string]::IsNullOrWhiteSpace($Service)) {
            Invoke-Compose @("logs", "--tail", "200")
        } else {
            Invoke-Compose @("logs", "--tail", "200", $Service)
        }
    }
    "down" {
        Invoke-Compose @("down", "--remove-orphans")
    }
    "reset" {
        if (-not $ConfirmReset) {
            throw "reset permanently deletes local PostgreSQL, Kafka and Redis volumes; pass -ConfirmReset"
        }
        Invoke-Compose @("down", "--volumes", "--remove-orphans")
    }
}
