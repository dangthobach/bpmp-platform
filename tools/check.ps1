param(
    [switch]$IncludeWasmtime
)

$ErrorActionPreference = 'Stop'

function Invoke-Checked {
    param(
        [Parameter(Mandatory)]
        [string]$Executable,
        [Parameter(Mandatory)]
        [string[]]$Arguments
    )

    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Executable exited with code $LASTEXITCODE"
    }
}

$bufCommand = Get-Command buf -ErrorAction SilentlyContinue
if ($bufCommand) {
    $bufExecutable = $bufCommand.Source
} else {
    $bufExecutable = Get-ChildItem "$env:LOCALAPPDATA\Microsoft\WinGet\Packages" `
        -Recurse -Filter buf.exe -ErrorAction SilentlyContinue |
        Select-Object -First 1 -ExpandProperty FullName
}
if (-not $bufExecutable) {
    throw 'Buf is not installed. Run: winget install --id bufbuild.buf --exact'
}

Invoke-Checked -Executable 'cargo' -Arguments @('fmt', '--check')
$workspaceSelection = @('--workspace')
if (-not $IncludeWasmtime) {
    $workspaceSelection += @('--exclude', 'bpmp-adapter-wasmtime')
}
$clippyArguments = @('clippy') + $workspaceSelection + @(
    '--all-targets', '--all-features', '--', '-D', 'warnings'
)
$testArguments = @('test') + $workspaceSelection + @('--all-targets')
Invoke-Checked -Executable 'cargo' -Arguments $clippyArguments
Invoke-Checked -Executable 'cargo' -Arguments $testArguments
Invoke-Checked -Executable $bufExecutable -Arguments @('lint')
Invoke-Checked -Executable $bufExecutable -Arguments @(
    'breaking', '--against', 'contracts/baseline/v1.binpb'
)
