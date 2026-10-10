param([string]$Setup = (Join-Path (Split-Path $PSScriptRoot -Parent) 'output\SSHTunnelServer-Setup-1.4.0.exe'))
$ErrorActionPreference = 'Stop'
$uninstallKey='HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\OnTech.SSHTunnelServer_is1'
$runKey='HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
if(Test-Path $uninstallKey){throw 'Existing installation found in this user context; refusing to alter it.'}
if(Get-ItemProperty $runKey -Name OnTechSSHTunnelServer -ErrorAction SilentlyContinue){throw 'Existing startup entry found; refusing to alter it.'}
$root=Join-Path $env:TEMP ('SSHTunnel-InstallerTest-'+[Guid]::NewGuid().ToString('N'))
$install=Join-Path $root 'app'
$data=Join-Path $root 'data'
New-Item -ItemType Directory -Path $root | Out-Null
try {
 $arguments='/VERYSILENT /SUPPRESSMSGBOXES /NORESTART /SP- /DIR="'+$install+'" /TASKS="startup" /LOG="'+(Join-Path $root 'setup.log')+'"'
 $p=Start-Process $Setup -ArgumentList $arguments -Wait -PassThru
 if($p.ExitCode -ne 0){throw ('Installer failed: '+$p.ExitCode)}
 $exe=Join-Path $install 'SSHTunnelServer.exe'
 if(-not (Test-Path $exe)){throw 'Installed executable missing.'}
 foreach($file in @('Configure-Firewall.cmd','scripts\Configure-Firewall.ps1')){if(-not(Test-Path (Join-Path $install $file))){throw ('Firewall helper missing: '+$file)}}
 $source=Join-Path (Split-Path $Setup -Parent) 'SSHTunnelServer.exe'
 if((Get-FileHash $source).Hash -ne (Get-FileHash $exe).Hash){throw 'Installed executable hash mismatch.'}
 if(-not (Test-Path $uninstallKey)){throw 'Uninstall entry missing.'}
 $startup=(Get-ItemProperty $runKey -Name OnTechSSHTunnelServer).OnTechSSHTunnelServer
 if(-not $startup.StartsWith(('"'+$exe+'" tray '))){throw 'Invalid tray startup command.'}
 $p=Start-Process $exe -ArgumentList ('init --data-dir "'+$data+'"') -Wait -PassThru
 if($p.ExitCode -ne 0){throw 'Installed executable init failed.'}
 $before=(Get-FileHash (Join-Path $data 'host_ed25519.pem')).Hash
 $uninstaller=Join-Path $install 'unins000.exe'
 $p=Start-Process $uninstaller -ArgumentList '/VERYSILENT /SUPPRESSMSGBOXES /NORESTART' -Wait -PassThru
 if($p.ExitCode -ne 0){throw 'Uninstall failed.'}
 if(Test-Path $exe){throw 'Executable remains after uninstall.'}
 if(Test-Path $uninstallKey){throw 'Uninstall entry remains.'}
 if(Get-ItemProperty $runKey -Name OnTechSSHTunnelServer -ErrorAction SilentlyContinue){throw 'Startup entry remains.'}
 if((Get-FileHash (Join-Path $data 'host_ed25519.pem')).Hash -ne $before){throw 'User data was removed or changed.'}
 Write-Output 'INSTALLER PASS: silent install; installed executable hash; uninstall registration; optional login startup; installed binary init; uninstall; startup cleanup; data preserved.'
} finally {
 $uninstaller=Join-Path $install 'unins000.exe'
 if(Test-Path $uninstaller){$cleanup=Start-Process $uninstaller -ArgumentList '/VERYSILENT /SUPPRESSMSGBOXES /NORESTART' -Wait -PassThru;if($cleanup.ExitCode -ne 0){Write-Warning "Cleanup failed. Retained: $root"}}
 if(-not (Test-Path $uninstallKey) -and (Test-Path $root)){Remove-Item -LiteralPath $root -Recurse -Force}
}
