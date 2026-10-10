# SSHTunnel Server

특정 장비·목적지에 고정되지 않는 **범용 SSH TCP 터널링 서버**입니다. 일반 `ssh` 클라이언트에서 Local(`-L`), Reverse(`-R`), 동적 SOCKS5(`-D`) 포워딩을 사용할 수 있습니다. 서버 실행 파일 하나에 SSH 엔진과 한국어 관리 화면이 포함되어 있습니다.

## 서버 프로그램 → 주소 발급 → 일반 클라이언트 접속

1.2.0부터 **키 기반 자동 터널 API와 Go 연결 모듈**을 제공합니다. 서버 프로그램이 중계 서버에 먼저 연결하면 공개 주소 `IP/도메인:자동배정포트`를 반환하고, 외부 클라이언트는 이 주소로 일반 TCP 접속합니다.

관리 화면 **서버 설정 → 자동 터널**에서 공개 IP/도메인과 포트 범위를 설정하고, **접속 계정**에서 인증키를 발급하세요. 서버 프로그램은 `pkg/tunnel`의 `tunnel.Open()`에 기존 TCP 서버의 로컬 주소를 전달하면 됩니다. 터널 식별키는 모듈이 자동 생성합니다.

- 상세 연동 문서: [docs/TUNNEL-API.md](docs/TUNNEL-API.md)
- 기존 TCP 서버 공개 예제: `examples/publish`
- 서버 프로그램이 직접 터널을 여는 HTTP 예제: `examples/http-server`
- 브라우저 연결·메시지 왕복 테스트: [examples/web-demo](examples/web-demo/README.md)
- 외부 클라이언트: 전용 프로그램 불필요

중계 서버의 SSH 포트와 자동 배정 포트 범위에 외부 접속이 가능해야 합니다. 자동 터널은 초기 비활성 상태이며 실제 공개 주소 설정 후 활성화합니다. 기존 SSH `-L` / `-R` / `-D` 기능도 유지합니다.

## 설치 및 트레이 실행 (1.2.0)

1. `output\SSHTunnelServer-Setup-1.2.0.exe`를 실행합니다. 한국어·영어 설치 화면을 제공합니다.
2. 설치 후 시작 메뉴의 **SSHTunnel Server**를 실행하면 **창·콘솔·브라우저를 자동으로 열지 않고 트레이 아이콘만 표시**합니다.
3. 트레이 아이콘을 클릭하고 **관리 화면 열기**를 선택합니다. 기본 관리 주소는 `http://127.0.0.1:17893`입니다.
4. **접속 계정 → 계정 추가**에서 SSH 공개키 또는 암호를 등록합니다. 기본 SSH 포트는 **2222**입니다.

트레이 메뉴는 **관리 화면 열기 / 서버 다시 시작 / 설정·로그 폴더 열기 / 서버 종료**를 제공합니다. 포트 설정을 저장한 뒤 **서버 다시 시작**을 선택하면 적용됩니다. 같은 로그인 세션에서 중복 실행하면 기존 트레이 서버를 유지합니다. Explorer가 재시작되면 아이콘을 다시 등록합니다. 시작 오류는 자동 팝업 대신 트레이 상태와 `startup.log`에 남기며, 메뉴의 **오류 상세 보기**에서 확인할 수 있습니다.

Windows가 아이콘을 숨긴 경우 알림 영역의 **∧ 숨겨진 아이콘**에서 찾으세요. 설치 화면에서 **로그인 시 자동 실행**과 **바탕화면 바로가기**를 선택할 수 있습니다. 자동 실행은 로그인 후 트레이로 실행하는 기능이며 Windows 서비스 설치와 별개입니다.

기본 설치 위치는 `%LOCALAPPDATA%\Programs\SSHTunnelServer`, 설정은 `%LOCALAPPDATA%\SSHTunnel\data`입니다. 설치 제거·업데이트 시 계정·설정·호스트 키를 보존합니다. 업데이트나 제거 전에는 트레이 메뉴에서 서버를 종료하세요. 초기 계정은 없으며 등록 전에는 SSH 인증에 성공할 수 없습니다.

## 포터블 실행 및 이전 데이터

`output\SSHTunnelServer.exe`를 직접 실행해도 트레이로 시작하며 설치판과 같은 사용자별 데이터 폴더를 사용합니다. 별도 설치 없이 폴더별 데이터를 쓰려면 **Start-Server.vbs**를 실행합니다. 소스 폴더의 VBS는 `output\SSHTunnelServer.exe`와 소스 폴더의 `data`를, 배포 ZIP의 VBS는 압축 해제 폴더의 실행 파일과 `data`를 사용합니다. 기존 `Start-Server.cmd`도 VBS를 호출하지만 CMD 실행 시 운영체제의 명령 창이 잠깐 보일 수 있으므로 EXE·바로가기·VBS 실행을 사용하세요.

기존 데이터를 계속 사용하는 예:

```powershell
.\output\SSHTunnelServer.exe tray --data-dir 'D:\src\SSHTunnel\data'
```

1.0 버전의 데이터는 자동 이동하거나 덮어쓰지 않습니다. 기존 데이터가 있다면 위처럼 명시하거나 서버 종료 후 데이터 폴더 전체를 새 위치에 복사하세요. 트레이와 Windows 서비스를 같은 포트·데이터 폴더로 동시에 실행할 수 없습니다.

## 범용 연결 예시

클라이언트에서 키를 생성합니다. `id_tunnel.pub`의 한 줄을 관리 화면에 등록하고 개인키 `id_tunnel`은 클라이언트에 보관합니다.

```powershell
ssh-keygen -t ed25519 -f id_tunnel
```

아래 `server.example.com`, `tunnel`, 대상 IP와 포트를 실제 환경으로 바꾸세요. 처음 접속할 때 표시되는 서버 키 지문을 관리 화면의 지문과 대조합니다.

```powershell
# Local: 내 PC 13389 → 서버에서 접근 가능한 192.168.0.10:3389
ssh -N -T -p 2222 -i id_tunnel -o ExitOnForwardFailure=yes -L 127.0.0.1:13389:192.168.0.10:3389 tunnel@server.example.com

# Reverse: 서버 18080 → 내 PC의 웹 서비스 8080
ssh -N -T -p 2222 -i id_tunnel -o ExitOnForwardFailure=yes -R 127.0.0.1:18080:127.0.0.1:8080 tunnel@server.example.com

# SOCKS5: 앱에서 목적지를 요청마다 선택
ssh -N -T -p 2222 -i id_tunnel -o ExitOnForwardFailure=yes -D 127.0.0.1:1080 tunnel@server.example.com

# 한 SSH 연결에 여러 TCP 서비스를 동시에 연결
ssh -N -T -p 2222 -i id_tunnel -o ExitOnForwardFailure=yes -L 127.0.0.1:15432:10.0.0.20:5432 -L 127.0.0.1:16379:10.0.0.21:6379 -R 127.0.0.1:18080:127.0.0.1:8080 tunnel@server.example.com
```

SOCKS5 클라이언트에서 원격 DNS가 필요하면 SOCKS5 호스트 이름 해석 옵션을 켭니다(예: `curl --proxy socks5h://127.0.0.1:1080 https://example.com`). 연결 유지가 필요하면 SSH 명령에 `-o ServerAliveInterval=30 -o ServerAliveCountMax=3`을 추가합니다. 끊어진 SSH 세션의 자동 재접속은 클라이언트 측에서 구성합니다.

## 계정 정책

| 항목 | 동작 |
|---|---|
| 인증 | 공개키 여러 개 또는 암호. Windows 계정과 독립적입니다. 자동 터널용 API 인증키도 사용할 수 있습니다. |
| Local / SOCKS 목적지 | `*:*`, `db.example.com:5432`, `10.0.0.0/8:443`, `192.168.1.20:1000-2000`, `[::1]:8080` |
| Reverse 수신 포트 | `*`, `18080`, `20000-30000`, `0`(서버가 빈 포트를 배정하도록 요청) |
| Reverse 외부 공개 | 기본 비허용. 허용한 계정만 `-R 0.0.0.0:...` 등으로 외부 인터페이스에 수신할 수 있습니다. |
| 동시 연결 | 서버 전체·계정별 SSH 세션 한도, 세션당 TCP 채널·Reverse 수신 포트 한도 |

빈 목적지/수신 포트 목록은 해당 요청을 모두 거부합니다. 목적지 규칙은 허용 목록이며 차단 규칙과 우선순위는 없습니다. 호스트 이름 규칙은 그 이름의 DNS 결과를 허용하고, CIDR 규칙은 실제 연결 IP를 검사합니다. Reverse는 서버에 열리는 포트를 제한하며, 클라이언트 뒤쪽 실제 목적지는 SSH 서버가 검사할 수 없습니다. `0` 허용은 배정될 포트 범위를 제한하는 기능이 아닙니다.

관리 화면에서 계정·설정을 저장하면 기존 SSH 연결을 모두 종료하고 재인증을 요구합니다. **개별 연결 종료**는 선택한 연결만 종료합니다. **전체 접속 중지**는 현재 연결과 Reverse 수신 포트를 닫고 새 접속을 차단하며 재시작 후에도 유지됩니다. SSH 및 관리 수신 주소 변경은 재시작 후 적용됩니다.

## Windows 서비스

관리자 PowerShell에서 실행합니다. 기본 서비스 계정은 `LocalService`입니다.

```powershell
# 방화벽 규칙은 변경하지 않음
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\Install-Service.ps1

# SSH 포트용 인바운드 방화벽 규칙도 함께 생성하려는 경우
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\Install-Service.ps1 -OpenFirewall
```

기본 설치 폴더는 `%ProgramFiles%\SSHTunnelServer`, 서비스 데이터는 `%ProgramData%\SSHTunnel`입니다. 포터블 데이터와 독립적입니다. 기존 포터블 설정을 서비스에서 계속 사용하려면 `-DataDir 'D:\src\SSHTunnel\data'`처럼 지정하고 포터블 서버를 먼저 종료합니다.

관리자 권한으로 `Open-Admin.cmd -DataDir "C:\ProgramData\SSHTunnel"`을 실행하면 서비스 관리 화면을 엽니다. 사용자 지정 데이터 폴더도 `-DataDir`로 지정하세요. 실행 파일에는 Go·.NET·Python 런타임이나 Windows OpenSSH Server 설치가 필요하지 않습니다.

```powershell
Get-Service SSHTunnelServer
Restart-Service SSHTunnelServer
Stop-Service SSHTunnelServer
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\Uninstall-Service.ps1
```

제거는 서비스 등록과 해당 실행 파일의 설치 스크립트가 만든 방화벽 규칙만 제거합니다. 실행 파일·설정·키·로그는 보존합니다. 업데이트는 먼저 제거하고 이전 실행 파일을 별도 보관한 다음 새 배포본의 설치 스크립트를 실행하세요. 기존 호스트 키를 유지해야 클라이언트의 서버 식별이 유지됩니다. SSH 포트 변경 시 기존 방화벽 규칙도 운영자가 갱신해야 합니다.

## 빌드 및 검증

Windows amd64, Go 1.26 이상과 MinGW `windres.exe`에서:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\Build.ps1
```

테스트 → `go vet` → 실행 파일 → 배포 ZIP 순서로 처리합니다. 결과는 `output\SSHTunnelServer.exe`, `dist\SSHTunnelServer-windows-amd64.zip`입니다. 테스트 중 시스템 `ssh`가 있으면 실제 OpenSSH SOCKS5 연결도 검사합니다. 런타임 데이터와 비밀키는 배포 ZIP에 포함하지 않습니다.

설치 파일까지 만들려면 Inno Setup 6/7이 필요합니다:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\Build-Installer.ps1
```

실행 파일은 Windows GUI 서브시스템으로 빌드하며 아이콘·버전·asInvoker 매니페스트를 포함합니다. 설치 파일은 `output\SSHTunnelServer-Setup-1.2.0.exe`입니다. 설치판은 현재 사용자용 트레이 프로그램이며 서비스·방화벽을 자동 변경하지 않습니다.

소스 구조: `cmd/sshtunnel` 실행·Windows 서비스, `internal/server` SSH·정책·관리 API, `internal/server/web` 내장 관리 화면, `scripts` 운영 도구입니다. 외부 모듈은 `go.mod`와 `go.sum`으로 고정됩니다. 배포본 `licenses`에 Go 및 의존 모듈의 라이선스를 포함합니다.

## 운영 범위

- TCP 터널 전용입니다. SSH 셸·명령 실행·SFTP·UDP·L3 VPN은 지원하지 않습니다.
- 관리 화면은 루프백 주소에만 수신하고 관리자 토큰으로 인증합니다. 원격 관리가 필요하면 먼저 서버 PC에 안전하게 접속해 로컬 관리 화면을 사용하세요.
- 데이터 폴더는 현재 실행 사용자·Administrators·SYSTEM·LocalService만 접근하도록 Windows ACL을 제한합니다. 같은 서버의 관리자와 LocalService 프로세스는 신뢰 경계 안에 있습니다.
- 외부 SSH 접속을 사용하려면 해당 포트의 방화벽·라우터/NAT 설정을 준비합니다. Reverse 외부 포트는 별도 방화벽 허용이 필요할 수 있습니다.
- 연결별 인증 시도 제한과 IP별 새 연결 제한(분당 60회)이 있습니다. 여러 사용자가 같은 NAT를 공유하면 이 IP 제한도 공유합니다.
- `events.jsonl`은 약 2 MiB에서 1개 백업 파일로 순환하며 연결·정책 이벤트만 기록합니다. UI는 이번 실행의 최근 200개 이벤트를 표시합니다.

자세한 파일·API 설명은 [docs/OPERATIONS.md](docs/OPERATIONS.md)에 있습니다.

참고: [Go SSH 패키지](https://pkg.go.dev/golang.org/x/crypto/ssh), [Windows OpenSSH 클라이언트](https://learn.microsoft.com/windows-server/administration/openssh/openssh-overview).
