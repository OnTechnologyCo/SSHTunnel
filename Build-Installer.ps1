param([string]$ISCC, [switch]$SkipBuild)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'scripts\Go-Toolchain.ps1')
$previousToolchain = $env:GOTOOLCHAIN
try {
$env:GOTOOLCHAIN = Get-ProjectGoToolchain
if (-not $SkipBuild) { & (Join-Path $PSScriptRoot 'Build.ps1') }
# Check the actual embedded executable even when the caller skips rebuilding.
Assert-ProjectGoBinary (Join-Path $PSScriptRoot 'output\SSHTunnelServer.exe') | Write-Output
$sdkModule = Get-Content (Join-Path $PSScriptRoot 'output\SDK\go.mod') -Raw
if ($sdkModule -notmatch ('(?m)^go ' + [regex]::Escape($env:GOTOOLCHAIN.Substring(2)) + '\s*$')) { throw 'Packaged SDK uses a stale Go minimum; rebuild.' }
if (-not $ISCC) {
    $cmd = Get-Command ISCC.exe -ErrorAction SilentlyContinue
    if ($cmd) { $ISCC = $cmd.Source }
    else {
        $candidates = @(
            (Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6\ISCC.exe'),
            (Join-Path $env:ProgramFiles 'Inno Setup 6\ISCC.exe'),
            (Join-Path $env:LOCALAPPDATA 'Programs\Inno Setup 6\ISCC.exe'),
            (Join-Path $env:ProgramFiles 'Inno Setup 7\ISCC.exe'),
            (Join-Path $env:LOCALAPPDATA 'Programs\Inno Setup 7\ISCC.exe')
        )
        $ISCC = $candidates | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
    }
}
if (-not $ISCC -or -not (Test-Path -LiteralPath $ISCC)) { throw 'Install Inno Setup 6/7 or supply -ISCC with its compiler path.' }
& $ISCC (Join-Path $PSScriptRoot 'installer\SSHTunnelServer.iss')
if ($LASTEXITCODE -ne 0) { throw 'Inno Setup compilation failed.' }
$setup = Join-Path $PSScriptRoot 'output\SSHTunnelServer-Setup-1.5.1.exe'
Get-Item -LiteralPath $setup | Select-Object FullName,Length
Get-FileHash -LiteralPath $setup -Algorithm SHA256 | Format-List
} finally {
    $env:GOTOOLCHAIN = $previousToolchain
}
