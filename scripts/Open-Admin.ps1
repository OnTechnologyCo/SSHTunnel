param([string]$DataDir)
$ErrorActionPreference = 'Stop'
if (-not $DataDir) {
    $portable = Join-Path (Split-Path $PSScriptRoot -Parent) 'data'
    if (Test-Path (Join-Path $portable 'server.json')) { $DataDir = $portable }
    elseif (Test-Path (Join-Path $env:LOCALAPPDATA 'SSHTunnel\data\server.json')) { $DataDir = Join-Path $env:LOCALAPPDATA 'SSHTunnel\data' }
    else { $DataDir = Join-Path $env:ProgramData 'SSHTunnel' }
}
$config = Get-Content -LiteralPath (Join-Path $DataDir 'server.json') -Raw | ConvertFrom-Json
$token = [IO.File]::ReadAllText((Join-Path $DataDir 'admin.token')).Trim()
if ($token -notmatch '^[0-9a-f]{64}$') { throw 'Invalid admin token.' }
$uri = [Uri]('http://' + $config.adminListen + '/')
if (-not $uri.IsLoopback) { throw 'Management address must be loopback.' }
# The fragment is consumed locally by the page; the token is not sent in a URL request.
Start-Process ($uri.AbsoluteUri + '#token=' + $token)
