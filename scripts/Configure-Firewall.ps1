param(
    [string]$DataDir,
    [string]$ServerExe,
    [switch]$Apply,
    [switch]$Initialize,
    [switch]$IncludeTcpPorts,
    [switch]$Pause
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
try {
    $appRoot = Split-Path $PSScriptRoot -Parent
    if (-not $DataDir) {
        $portable = Join-Path $appRoot 'data'
        $installed = Join-Path $env:LOCALAPPDATA 'SSHTunnel\data'
        if (Test-Path (Join-Path $portable 'server.json')) { $DataDir = $portable }
        else { $DataDir = $installed }
    }
    $DataDir = [IO.Path]::GetFullPath($DataDir)
    $configPath = Join-Path $DataDir 'server.json'
    if (-not $ServerExe) { $ServerExe = Join-Path $appRoot 'SSHTunnelServer.exe' }
    $ServerExe = [IO.Path]::GetFullPath($ServerExe)
    if (-not (Test-Path -LiteralPath $ServerExe -PathType Leaf)) { throw 'SSHTunnelServer.exe 경로를 확인하세요.' }
    if (-not (Test-Path -LiteralPath $configPath) -and $Initialize) {
        # 설치한 사용자 컨텍스트에서 초기화한 뒤 권한을 올려 설정 소유권을 보존합니다.
        $init = Start-Process $ServerExe -ArgumentList ('init --data-dir "'+$DataDir+'"') -WindowStyle Hidden -Wait -PassThru
        if ($init.ExitCode -ne 0) { throw '서버 기본 설정 생성에 실패했습니다.' }
    }
    if (-not (Test-Path -LiteralPath $configPath)) { throw '서버 설정이 없습니다. 프로그램에서 설정을 저장한 후 실행하세요.' }
    $config = Get-Content -LiteralPath $configPath -Raw -Encoding UTF8 | ConvertFrom-Json
    $sshUri = [Uri]('tcp://' + $config.listen)
    if ($sshUri.Port -lt 1 -or $sshUri.Port -gt 65535) { throw 'SSH 수신 포트가 잘못되었습니다.' }
    $adminUri = [Uri]('http://' + $config.adminListen)
    $ports = @()
    if ($sshUri.IsLoopback) { Write-Warning 'SSH 수신 주소가 루프백입니다. 외부 접속에는 서버 설정에서 0.0.0.0 등의 수신 주소가 필요합니다.' }
    else { $ports += [pscustomobject]@{Name='SSH';Port=[string]$sshUri.Port;Program=$ServerExe} }
    if ($config.web -and $config.web.enabled) {
        $webProgram = 'Any'
        if ($config.web.scheme -eq 'https') {
            $webPort = 443
            if ($config.web.tlsMode -eq 'native') {
                $tlsListen = $config.web.httpsListen
                if (-not $tlsListen) { $tlsListen = '0.0.0.0:443' }
                $tlsUri = [Uri]('https://' + $tlsListen)
                $webPort = $tlsUri.Port
                if ($webPort -lt 1 -or $webPort -gt 65535) { throw 'HTTPS 수신 포트를 확인하세요.' }
                if ($tlsUri.IsLoopback) { Write-Warning '내장 HTTPS가 루프백 수신입니다. 외부 접속에는 외부 수신 주소가 필요합니다.' }
                $webProgram = $ServerExe
            }
        }
        elseif ($config.web.scheme -eq 'http') { $webPort = 80 }
        else { throw '웹 접속 방식은 http 또는 https여야 합니다.' }
        if ($webPort -eq $adminUri.Port -or $webPort -eq $sshUri.Port) { throw '공개 웹 포트가 관리/SSH 포트와 충돌합니다.' }
        $ports += [pscustomobject]@{Name='Web';Port=[string]$webPort;Program=$webProgram}
        $webListeners = @(Get-NetTCPConnection -State Listen -LocalPort $webPort -ErrorAction SilentlyContinue)
        if (-not ($webListeners | Where-Object {$_.LocalAddress -notin @('127.0.0.1','::1')})) {
            Write-Warning "외부 웹 TCP $webPort 수신기가 없습니다. 방화벽 허용만으로 웹 접속은 되지 않습니다."
            if ($config.web.tlsMode -eq 'native') { Write-Warning '서버를 재시작하고 관리 화면에서 내장 HTTPS 상태와 인증서 등록/발급을 확인하세요.' }
            else { Write-Warning "외부 HTTPS 프록시가 없으면 관리 화면에서 내장 HTTPS를 선택하세요. 외부 프록시를 사용하면 $($config.web.listen)로 전달하도록 설정하세요." }
        }
    }
    if ($IncludeTcpPorts -and $config.broker.enabled) {
        $min = [int]$config.broker.portMin; $max = [int]$config.broker.portMax
        if ($min -lt 1024 -or $max -gt 65535 -or $min -gt $max -or ($max-$min) -ge 4096) { throw 'TCP 터널 포트 범위가 잘못되었습니다.' }
        if ($adminUri.Port -ge $min -and $adminUri.Port -le $max) { throw 'TCP 범위에 관리 포트가 포함되어 있습니다.' }
        $ports += [pscustomobject]@{Name='TCP';Port="$min-$max";Program=$ServerExe}
    }
    if ($ports | Where-Object {$_.Port -eq [string]$adminUri.Port}) { throw '관리 포트는 공개하지 않습니다.' }
    Write-Host "설정 파일: $configPath"
    $ports | Format-Table Name,Port,Program -AutoSize
    $isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    if ($Apply -and -not $isAdmin) {
        $arguments = @('-NoProfile','-ExecutionPolicy','Bypass','-File',('"'+$PSCommandPath+'"'),'-DataDir',('"'+$DataDir+'"'),'-ServerExe',('"'+$ServerExe+'"'),'-Apply')
        if ($IncludeTcpPorts) { $arguments += '-IncludeTcpPorts' }
        if ($Pause) { $arguments += '-Pause' }
        $Pause = $false
        $child = Start-Process powershell.exe -Verb RunAs -ArgumentList $arguments -PassThru -Wait
        exit $child.ExitCode
    }
    if ($Apply) {
        # 설정 파일마다 규칙을 구분하고, 이 도구가 생성한 규칙만 갱신합니다.
        $hasher = [Security.Cryptography.SHA256]::Create()
        try { $scope = ([BitConverter]::ToString($hasher.ComputeHash([Text.Encoding]::UTF8.GetBytes($configPath.ToLowerInvariant())))).Replace('-','').Substring(0,12) }
        finally { $hasher.Dispose() }
        $group = 'OnTech.SSHTunnel.Managed'
        foreach ($item in $ports) {
            $ruleName = "OnTech.SSHTunnel.$scope.$($item.Name)"
            $existing = Get-NetFirewallRule -PolicyStore PersistentStore -Name $ruleName -ErrorAction SilentlyContinue
            if ($existing -and $existing.Group -ne $group) { throw "다른 프로그램의 규칙과 이름이 겹칩니다: $ruleName" }
            $display = "SSHTunnel $($item.Name) TCP $($item.Port) [$scope]"
            $rule = @{PolicyStore='PersistentStore';Name=$ruleName;Direction='Inbound';Action='Allow';Enabled='True';Profile='Any';Protocol='TCP';LocalPort=$item.Port;Program=$item.Program}
            if ($existing) { Set-NetFirewallRule @rule -NewDisplayName $display }
            else { New-NetFirewallRule @rule -Group $group -DisplayName $display | Out-Null }
            Write-Host "허용 완료: TCP $($item.Port) ($($item.Name))"
        }
        foreach ($kind in @('SSH','Web','TCP')) {
            if ($ports.Name -contains $kind) { continue }
            $old = Get-NetFirewallRule -PolicyStore PersistentStore -Name "OnTech.SSHTunnel.$scope.$kind" -ErrorAction SilentlyContinue
            if ($old -and $old.Group -eq $group) { $old | Remove-NetFirewallRule }
        }
        Write-Host 'Windows 방화벽 규칙 적용 완료. 기존 차단 정책, 공유기/NAT, HTTPS 인증서 및 프록시는 별도 확인이 필요합니다.'
    } else { Write-Host '진단만 수행했습니다. 실제 적용은 -Apply 옵션 또는 Configure-Firewall.cmd로 실행하세요.' }
    Write-Host '관리 화면과 루프백 웹 게이트웨이는 외부 방화벽 허용 대상에서 제외됩니다.'
} catch {
    Write-Host ('오류: ' + $_.Exception.Message) -ForegroundColor Red
    if ($Pause) { Read-Host 'Enter 키를 누르면 닫힙니다' | Out-Null }
    exit 1
}
if ($Pause) { Read-Host 'Enter 키를 누르면 닫힙니다' | Out-Null }
