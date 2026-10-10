param([string]$InstallDir = (Join-Path $env:ProgramFiles 'SSHTunnelServer'))
$ErrorActionPreference = 'Stop'
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Run from an elevated PowerShell window.' }
$name = 'SSHTunnelServer'
$service = Get-CimInstance Win32_Service -Filter "Name='$name'"
if (-not $service) { Write-Host 'Service is not installed.'; exit 0 }
$exe = Join-Path ([IO.Path]::GetFullPath($InstallDir)) 'SSHTunnelServer.exe'
if (-not $service.PathName.StartsWith(('"' + $exe + '" '), [StringComparison]::OrdinalIgnoreCase)) { throw 'Service points to a different executable. Supply its actual InstallDir.' }
if ($service.State -ne 'Stopped') { Stop-Service $name; (Get-Service $name).WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30)) }
& sc.exe delete $name
if ($LASTEXITCODE -ne 0) { throw 'Service deletion failed.' }
$rule = Get-NetFirewallRule -Name 'SSHTunnelServer-SSH' -ErrorAction SilentlyContinue
if ($rule) {
    $filter = $rule | Get-NetFirewallApplicationFilter
    if ($filter.Program -eq $exe) { $rule | Remove-NetFirewallRule }
}
Write-Host 'Service removed. Executable, configuration, keys and logs have been preserved.'
