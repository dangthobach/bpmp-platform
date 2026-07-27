[CmdletBinding()]
param(
    [switch]$RequireComplete
)

$ErrorActionPreference = "Stop"
$workspace = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$pattern = "Feature:\s*rust-bpm-platform,\s*Property\s+([1-9]|[1-4][0-9]|5[0-3]):"
$matches = Get-ChildItem `
    -Path (Join-Path $workspace "apps"), (Join-Path $workspace "crates"), (Join-Path $workspace "go"), (Join-Path $workspace "tests") `
    -Recurse -File -Include *.rs,*.go |
    Select-String -Pattern $pattern

$indexed = @{}
foreach ($match in $matches) {
    $number = [int]$match.Matches[0].Groups[1].Value
    if (-not $indexed.ContainsKey($number)) {
        $indexed[$number] = @()
    }
    $indexed[$number] += "$($match.Path):$($match.LineNumber)"
}

$duplicates = @()
$missing = @()
foreach ($number in 1..53) {
    if (-not $indexed.ContainsKey($number)) {
        $missing += $number
    } elseif ($indexed[$number].Count -ne 1) {
        $duplicates += "P${number}: $($indexed[$number] -join ', ')"
    }
}

if ($duplicates.Count -gt 0) {
    throw "Property catalog contains duplicate canonical tests:`n$($duplicates -join "`n")"
}

Write-Host "Canonical property coverage: $($indexed.Count)/53"
if ($missing.Count -gt 0) {
    Write-Host "Missing: $($missing | ForEach-Object { "P$_" } | Join-String -Separator ', ')"
    if ($RequireComplete) {
        throw "Property catalog is incomplete"
    }
}
