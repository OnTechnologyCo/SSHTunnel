param([string]$Exe = (Join-Path (Split-Path $PSScriptRoot -Parent) 'output\SSHTunnelServer.exe'))
$ErrorActionPreference = 'Stop'
Add-Type @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
public class TrayWindow {
 public IntPtr Handle; public bool Visible; public string Class;
}
public static class TrayProbe {
 delegate bool EnumProc(IntPtr h, IntPtr p);
 [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc callback, IntPtr p);
 [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h, out uint p);
 [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr h, StringBuilder name, int count);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern uint RegisterWindowMessage(string name);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
 public static TrayWindow[] Find(int pid) {
  var result=new List<TrayWindow>();
  EnumWindows(delegate(IntPtr h, IntPtr p) {
   uint owner; GetWindowThreadProcessId(h,out owner);
   if(owner==pid) {var name=new StringBuilder(256); GetClassName(h,name,256); result.Add(new TrayWindow{Handle=h,Visible=IsWindowVisible(h),Class=name.ToString()});}
   return true;
  },IntPtr.Zero);
  return result.ToArray();
 }
}
'@
function Free-Port {
 $listener=New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback,0)
 $listener.Start();$port=$listener.LocalEndpoint.Port;$listener.Stop();return $port
}
function Wait-State($Base, $Headers, $Previous) {
 for($i=0;$i -lt 60;$i++) {
  try {$state=Invoke-RestMethod ($Base+'/api/status') -Headers $Headers -TimeoutSec 1;if(-not $Previous -or $state.started -ne $Previous){return $state}}catch{}
  Start-Sleep -Milliseconds 100
 }
 throw 'Server was not ready within the timeout.'
}
$root=Join-Path $env:TEMP ('SSHTunnel-TrayTest-'+[Guid]::NewGuid().ToString('N'))
$data=Join-Path $root 'data'
$process=$null
New-Item -ItemType Directory -Path $root | Out-Null
try {
 $bytes=[IO.File]::ReadAllBytes($Exe)
 $pe=[BitConverter]::ToInt32($bytes,0x3c)
 $subsystem=[BitConverter]::ToUInt16($bytes,$pe+24+68)
 if($subsystem -ne 2){throw 'Executable is not Windows GUI subsystem.'}
 $init=Start-Process $Exe -ArgumentList ('init --data-dir "'+$data+'"') -Wait -PassThru
 if($init.ExitCode -ne 0){throw 'Init failed.'}
 $configPath=Join-Path $data 'server.json'
 $cfg=[IO.File]::ReadAllText($configPath)|ConvertFrom-Json
 $cfg.listen='127.0.0.1:'+(Free-Port)
 $cfg.adminListen='127.0.0.1:'+(Free-Port)
 [IO.File]::WriteAllText($configPath,($cfg|ConvertTo-Json -Depth 10),(New-Object Text.UTF8Encoding($false)))
 $headers=@{Authorization='Bearer '+[IO.File]::ReadAllText((Join-Path $data 'admin.token')).Trim()}
 $base='http://'+$cfg.adminListen
 # Default mode, no hidden-window override: the executable itself must stay invisible.
 $process=Start-Process $Exe -ArgumentList ('--data-dir "'+$data+'"') -PassThru
 $state=Wait-State $base $headers $null
 $windows=@([TrayProbe]::Find($process.Id))
 $hostWindow=$windows|Where-Object {$_.Class -eq 'OnTech.SSHTunnel.TrayHost.v1'}|Select-Object -First 1
 if(-not $hostWindow -or @($windows|Where-Object {$_.Visible}).Count -ne 0){throw 'Missing hidden tray host or unexpected visible window.'}
 $duplicate=Start-Process $Exe -ArgumentList ('--data-dir "'+$data+'"') -PassThru
 if(-not $duplicate.WaitForExit(5000)){Stop-Process -Id $duplicate.Id -Force;throw 'Duplicate instance did not exit.'}
 if($duplicate.ExitCode -ne 0){throw 'Duplicate instance returned an error.'}
 [void](Wait-State $base $headers $null)
 # Explorer restart notification targets only this app, never Explorer itself.
 [void][TrayProbe]::PostMessage($hostWindow.Handle,[TrayProbe]::RegisterWindowMessage('TaskbarCreated'),[IntPtr]::Zero,[IntPtr]::Zero)
 [void][TrayProbe]::PostMessage($hostWindow.Handle,0x111,[IntPtr]1002,[IntPtr]::Zero)
 $restarted=Wait-State $base $headers $state.started
 if($restarted.version -ne '1.3.0'){throw 'Unexpected server version.'}
 [void][TrayProbe]::PostMessage($hostWindow.Handle,0x10,[IntPtr]::Zero,[IntPtr]::Zero)
 if(-not $process.WaitForExit(10000)){throw 'Tray did not shut down gracefully.'}
 if($process.ExitCode -ne 0){throw 'Tray exited with an error.'}
 $check=Start-Process $Exe -ArgumentList ('check --data-dir "'+$data+'"') -Wait -PassThru
 if($check.ExitCode -ne 0){throw 'Data lock was not released.'}
 # A bad configuration stays in the tray with an error, without opening a dialog.
 [IO.File]::WriteAllText($configPath,'{broken-json')
 $process=Start-Process $Exe -ArgumentList ('--data-dir "'+$data+'"') -PassThru
 for($i=0;$i -lt 50 -and -not (Test-Path (Join-Path $data 'startup.log'));$i++){Start-Sleep -Milliseconds 100}
 if(-not (Test-Path (Join-Path $data 'startup.log'))){throw 'Startup error was not recorded.'}
 $windows=@([TrayProbe]::Find($process.Id));$hostWindow=$windows|Where-Object {$_.Class -eq 'OnTech.SSHTunnel.TrayHost.v1'}|Select-Object -First 1
 if(-not $hostWindow -or @($windows|Where-Object {$_.Visible}).Count -ne 0){throw 'Startup error displayed a window or lost the tray host.'}
 [void][TrayProbe]::PostMessage($hostWindow.Handle,0x10,[IntPtr]::Zero,[IntPtr]::Zero)
 if(-not $process.WaitForExit(5000)){throw 'Failed-start tray did not exit.'}
 Write-Output 'TRAY PASS: GUI subsystem; no visible windows; single instance; restart; Explorer notification; graceful exit; data lock release; silent startup-error handling.'
} finally {
 if($process -and -not $process.HasExited){Stop-Process -Id $process.Id -Force;$process.WaitForExit()}
 if(Test-Path $root){Remove-Item -LiteralPath $root -Recurse -Force}
}
