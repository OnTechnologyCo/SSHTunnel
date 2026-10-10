#Requires -RunAsAdministrator
# 방화벽 cmdlet을 대체하여 실제 PC 규칙을 바꾸지 않고 생성/갱신/정리를 검증합니다.
$ErrorActionPreference = 'Stop'
$root = Join-Path $env:TEMP ('SSHTunnel-FirewallTest-' + [Guid]::NewGuid().ToString('N'))
$global:sshtunnelFirewallTestRules = @{}
$global:sshtunnelFirewallTestNewCount = 0
function Get-NetTCPConnection { [CmdletBinding()]param($State,$LocalPort) }
function Get-NetFirewallRule { [CmdletBinding()]param($PolicyStore,$Name) if($global:sshtunnelFirewallTestRules.ContainsKey($Name)){[pscustomobject]$global:sshtunnelFirewallTestRules[$Name]} }
function New-NetFirewallRule {
    [CmdletBinding()]param($PolicyStore,$Name,$DisplayName,$Direction,$Action,$Enabled,$Profile,$Protocol,$LocalPort,$Program,$Group)
    $global:sshtunnelFirewallTestRules[$Name]=@{Name=$Name;Group=$Group;Port=$LocalPort;Program=$Program;Action=$Action;Direction=$Direction}
    $global:sshtunnelFirewallTestNewCount++
}
function Set-NetFirewallRule {
    [CmdletBinding()]param($PolicyStore,$Name,$NewDisplayName,$Direction,$Action,$Enabled,$Profile,$Protocol,$LocalPort,$Program)
    if(-not $global:sshtunnelFirewallTestRules.ContainsKey($Name)){throw 'Missing rule to update'}
    $global:sshtunnelFirewallTestRules[$Name].Port=$LocalPort; $global:sshtunnelFirewallTestRules[$Name].Program=$Program
}
function Remove-NetFirewallRule { [CmdletBinding()]param([Parameter(ValueFromPipeline=$true)]$InputObject) process {$global:sshtunnelFirewallTestRules.Remove($InputObject.Name)} }
function Assert($condition,$message){if(-not $condition){throw $message}}
try {
    New-Item -ItemType Directory -Path $root | Out-Null
    $exe=Join-Path $root 'SSHTunnelServer.exe'; [IO.File]::WriteAllText($exe,'fixture')
    $config=@{listen='0.0.0.0:39333';adminListen='127.0.0.1:17893';web=@{enabled=$true;scheme='https';listen='127.0.0.1:17894'};broker=@{enabled=$true;portMin=20000;portMax=20199}}
    $configPath=Join-Path $root 'server.json'
    $helper=Join-Path (Split-Path $PSScriptRoot -Parent) 'scripts\Configure-Firewall.ps1'
    $config | ConvertTo-Json -Depth 4 | Set-Content $configPath -Encoding UTF8
    & $helper -DataDir $root -ServerExe $exe
    Assert ($global:sshtunnelFirewallTestRules.Count -eq 0) 'Diagnostic mode changed rules'
    & $helper -DataDir $root -ServerExe $exe -Apply
    Assert ($global:sshtunnelFirewallTestRules.Count -eq 2) 'Expected only SSH and HTTPS rules'
    Assert (@($global:sshtunnelFirewallTestRules.Values | Where-Object {$_.Port -eq '39333' -and $_.Program -eq $exe}).Count -eq 1) 'Missing program-scoped SSH rule'
    Assert (@($global:sshtunnelFirewallTestRules.Values | Where-Object {$_.Port -eq '443'}).Count -eq 1) 'Missing HTTPS rule'
    & $helper -DataDir $root -ServerExe $exe -Apply
    Assert ($global:sshtunnelFirewallTestRules.Count -eq 2 -and $global:sshtunnelFirewallTestNewCount -eq 2) 'Repeated apply duplicated rules'
    $config.listen='0.0.0.0:39444';$config.web.scheme='http'
    $config | ConvertTo-Json -Depth 4 | Set-Content $configPath -Encoding UTF8
    & $helper -DataDir $root -ServerExe $exe -Apply
    Assert (@($global:sshtunnelFirewallTestRules.Values | Where-Object {$_.Port -eq '39444'}).Count -eq 1) 'SSH port change was not applied'
    Assert (@($global:sshtunnelFirewallTestRules.Values | Where-Object {$_.Port -in @('39333','443','17893','17894')}).Count -eq 0) 'Unexpected or stale port remained open'
    & $helper -DataDir $root -ServerExe $exe -Apply -IncludeTcpPorts
    Assert ($global:sshtunnelFirewallTestRules.Count -eq 3) 'Explicit TCP range was not added'
    $config.web.enabled=$false
    $config | ConvertTo-Json -Depth 4 | Set-Content $configPath -Encoding UTF8
    & $helper -DataDir $root -ServerExe $exe -Apply
    Assert ($global:sshtunnelFirewallTestRules.Count -eq 1) 'Obsolete owned rules were not removed'
    Write-Output 'FIREWALL PASS: diagnostic mode, configured SSH port, web port, program scope, idempotent updates, port changes, opt-in TCP range, obsolete rule cleanup. No real firewall rules changed.'
} finally { if(Test-Path $root){Remove-Item -LiteralPath $root -Recurse -Force} }
