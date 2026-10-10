#define AppVersion "1.3.1"

[Setup]
AppId=OnTech.SSHTunnelServer
AppName=SSHTunnel Server
AppVersion={#AppVersion}
AppPublisher=OnTech
DefaultDirName={localappdata}\Programs\SSHTunnelServer
DefaultGroupName=SSHTunnel Server
DisableProgramGroupPage=yes
DisableDirPage=no
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0.17763
OutputDir=..\output
OutputBaseFilename=SSHTunnelServer-Setup-{#AppVersion}
SetupIconFile=..\assets\server.ico
UninstallDisplayIcon={app}\SSHTunnelServer.exe
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
AppMutex=Local\OnTech.SSHTunnelServer.Tray
CloseApplications=no
RestartApplications=no
VersionInfoVersion=1.3.1.0

[Languages]
Name: "korean"; MessagesFile: "compiler:Languages\Korean.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[CustomMessages]
korean.DesktopTask=바탕화면 바로가기 만들기
korean.StartupTask=Windows 로그인 시 트레이에서 자동 실행
korean.LaunchProgram=SSHTunnel Server 실행 (트레이 아이콘)
korean.OpenAdmin=관리 화면 열기
korean.Firewall=접속 포트 방화벽 설정
korean.AllowFirewall=접속 포트 방화벽 허용 (관리자 권한 필요)
english.DesktopTask=Create a desktop shortcut
english.StartupTask=Start in the system tray when signing in to Windows
english.LaunchProgram=Launch SSHTunnel Server in the system tray
english.OpenAdmin=Open management page
english.Firewall=Configure connection port firewall
english.AllowFirewall=Allow connection ports through Windows Firewall (administrator approval)

[Tasks]
Name: "desktopicon"; Description: "{cm:DesktopTask}"; Flags: unchecked
Name: "startup"; Description: "{cm:StartupTask}"; Flags: unchecked

[Files]
Source: "..\output\SSHTunnelServer.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\output\docs\*"; DestDir: "{app}\docs"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "..\output\licenses\*"; DestDir: "{app}\licenses"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "..\output\scripts\*"; DestDir: "{app}\scripts"; Flags: ignoreversion
Source: "..\Open-Admin.cmd"; DestDir: "{app}"; Flags: ignoreversion

Source: "..\output\SDK\*"; DestDir: "{app}\SDK"; Flags: ignoreversion recursesubdirs createallsubdirs

Source: "..\Configure-Firewall.cmd"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\SSHTunnel Server"; Filename: "{app}\SSHTunnelServer.exe"; Parameters: "tray --data-dir ""{localappdata}\SSHTunnel\data"""; WorkingDir: "{app}"
Name: "{group}\{cm:OpenAdmin}"; Filename: "{app}\SSHTunnelServer.exe"; Parameters: "open-admin --data-dir ""{localappdata}\SSHTunnel\data"""; WorkingDir: "{app}"
Name: "{group}\Uninstall SSHTunnel Server"; Filename: "{uninstallexe}"
Name: "{autodesktop}\SSHTunnel Server"; Filename: "{app}\SSHTunnelServer.exe"; Parameters: "tray --data-dir ""{localappdata}\SSHTunnel\data"""; WorkingDir: "{app}"; Tasks: desktopicon

Name: "{group}\{cm:Firewall}"; Filename: "{app}\Configure-Firewall.cmd"; WorkingDir: "{app}"

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "OnTechSSHTunnelServer"; ValueData: """{app}\SSHTunnelServer.exe"" tray --data-dir ""{localappdata}\SSHTunnel\data"""; Flags: uninsdeletevalue; Tasks: startup
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueName: "OnTechSSHTunnelServer"; Flags: deletevalue; Check: not WizardIsTaskSelected('startup')

[Run]
Filename: "{sys}\WindowsPowerShell\v1.0\powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\scripts\Configure-Firewall.ps1"" -DataDir ""{localappdata}\SSHTunnel\data"" -ServerExe ""{app}\SSHTunnelServer.exe"" -Initialize -Apply -Pause"; Description: "{cm:AllowFirewall}"; Flags: postinstall skipifsilent
Filename: "{app}\SSHTunnelServer.exe"; Parameters: "tray --data-dir ""{localappdata}\SSHTunnel\data"""; Description: "{cm:LaunchProgram}"; Flags: nowait postinstall skipifsilent

; Data is outside {app} and is never deleted by the installer or uninstaller.
