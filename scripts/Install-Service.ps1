param(
    [string]$InstallDir = (Join-Path $env:ProgramFiles 'SSHTunnelServer'),
    [string]$DataDir = (Join-Path $env:ProgramData 'SSHTunnel'),
    [switch]$OpenFirewall
)
$ErrorActionPreference = 'Stop'
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run this script from an elevated PowerShell window.'
}
$name = 'SSHTunnelServer'
if (Get-Service -Name $name -ErrorAction SilentlyContinue) { throw 'Service already exists. Stop/uninstall it before replacing the executable. Data is preserved.' }
$root = Split-Path $PSScriptRoot -Parent
$source = Join-Path $root 'SSHTunnelServer.exe'
if (-not (Test-Path $source)) { $source = Join-Path $root 'output\SSHTunnelServer.exe' }
if (-not (Test-Path $source)) { throw 'Build the server first.' }
$InstallDir = [IO.Path]::GetFullPath($InstallDir)
$DataDir = [IO.Path]::GetFullPath($DataDir)
if ($InstallDir.Contains('"') -or $DataDir.Contains('"')) { throw 'Invalid path.' }
$exe = Join-Path $InstallDir 'SSHTunnelServer.exe'
if (Test-Path $exe) { throw 'Destination executable already exists. Select an empty installation directory or explicitly remove the previous executable after uninstall.' }
if ($OpenFirewall -and (Get-NetFirewallRule -Name 'SSHTunnelServer-SSH' -ErrorAction SilentlyContinue)) { throw 'Firewall rule SSHTunnelServer-SSH already exists.' }
New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
Copy-Item -LiteralPath $source -Destination $exe
New-Item -ItemType Directory -Path (Join-Path $InstallDir 'scripts') -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'Open-Admin.ps1') -Destination (Join-Path $InstallDir 'scripts') -Force
Copy-Item -LiteralPath (Join-Path $root 'Open-Admin.cmd') -Destination $InstallDir -Force
& $exe init --data-dir $DataDir
if ($LASTEXITCODE -ne 0) { throw 'Data initialization failed. No service was installed.' }
$config = Get-Content -LiteralPath (Join-Path $DataDir 'server.json') -Raw | ConvertFrom-Json
$commandLine = '"' + $exe + '" run --data-dir "' + $DataDir + '"'
New-Service -Name $name -BinaryPathName $commandLine -DisplayName 'SSH Tunnel Server' -StartupType Automatic -Description 'General-purpose SSH TCP forwarding server' -Credential (New-Object PSCredential('NT AUTHORITY\LocalService', (New-Object Security.SecureString))) | Out-Null
& sc.exe failure $name reset= 86400 actions= restart/5000/restart/15000/restart/60000 | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Could not configure service recovery.' }
if ($OpenFirewall) {
    $port = [int](($config.listen -split ':')[-1])
    New-NetFirewallRule -Name 'SSHTunnelServer-SSH' -DisplayName 'SSHTunnelServer SSH' -Direction Inbound -Action Allow -Protocol TCP -LocalPort $port -Program $exe -Profile Any | Out-Null
}
Start-Service $name
Write-Host "Installed and started $name as LocalService."
Write-Host "Management: http://$($config.adminListen)"
Write-Host "Run Open-Admin.cmd -DataDir `"$DataDir`" as administrator."
