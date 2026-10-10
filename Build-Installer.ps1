param([string]$ISCC, [switch]$SkipBuild)
$ErrorActionPreference = 'Stop'
if (-not $SkipBuild) { & (Join-Path $PSScriptRoot 'Build.ps1') }
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
$setup = Join-Path $PSScriptRoot 'output\SSHTunnelServer-Setup-1.2.0.exe'
Get-Item -LiteralPath $setup | Select-Object FullName,Length
Get-FileHash -LiteralPath $setup -Algorithm SHA256 | Format-List
