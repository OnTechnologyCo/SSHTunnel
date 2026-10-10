# 운영 및 API

## 데이터 파일

| 파일 | 용도 |
|---|---|
| `server.json` | 계정·공개키·bcrypt 암호 해시·접근 정책·수신 설정 |
| `host_ed25519.pem` | 서버 식별용 영구 개인키 |
| `admin.token` | 로컬 관리 API의 256비트 인증 토큰 |
| `server.lock` | 실행 중 데이터 폴더 중복 사용 방지 잠금 |
| `startup.log`, `startup.log.1` | 트레이 서버 시작 실패 로그 |
| `events.jsonl`, `events.jsonl.1` | 순환 이벤트 로그 |

계정 암호는 UTF-8 기준 12~72바이트입니다. 개인키·관리 토큰·암호는 API 응답이나 이벤트 로그에 출력하지 않습니다. 서버 키가 손상되면 자동 교체하지 않고 시작에 실패합니다. 전체 데이터 폴더를 안전하게 백업하세요.

`server.lock` 파일의 존재 자체는 실행 상태를 뜻하지 않습니다. 운영체제 파일 잠금을 사용하므로 비정상 종료 후에도 다음 실행이 가능합니다. 실행 중인 서버와 같은 데이터 폴더에 `init`/`check`를 실행하면 중복 사용 오류가 발생합니다.

```powershell
.\SSHTunnelServer.exe version
.\SSHTunnelServer.exe init --data-dir D:\TunnelData
.\SSHTunnelServer.exe check --data-dir D:\TunnelData
.\SSHTunnelServer.exe tray --data-dir D:\TunnelData
.\SSHTunnelServer.exe run --data-dir D:\TunnelData
.\SSHTunnelServer.exe open-admin --data-dir D:\TunnelData
```

`init`와 `check` 모두 데이터 폴더가 없으면 기본 설정을 생성하고 유효성을 확인합니다. 파일을 직접 수정하려면 서버를 종료한 뒤 편집하고 `check`를 실행합니다. 관리 토큰을 교체하려면 서버를 종료하고 `admin.token`을 별도 보관한 뒤 파일을 제거하고 재시작합니다. 기존 브라우저 탭에서 관리 연결을 다시 열어야 합니다.

## 관리 HTTP API

기본 URL: `http://127.0.0.1:17893`. 모든 `/api/` 요청에 `Authorization: Bearer <admin.token>`이 필요하며 변경 요청에는 `X-SSHTunnel: 1`도 필요합니다. JSON 변경 요청은 `Content-Type: application/json`을 사용합니다. Host 헤더는 실제 관리 포트와 루프백 호스트여야 하며 교차 출처 요청을 허용하지 않습니다.

| 메서드·경로 | 기능 |
|---|---|
| `GET /health` | 루프백 상태 확인, 인증 없이 버전·running 반환 |
| `GET /api/status` | 실행 주소·호스트 지문·현재 연결·재시작 필요 여부 |
| `GET /api/config` | 암호 해시를 제거한 설정, revision, 암호 사용 계정 목록 |
| `PUT /api/config` | 설정 및 선택적 계정 암호 갱신 |
| `POST /api/open` | 접속 허용 및 상태 저장 |
| `POST /api/close` | 전체 접속 종료·신규 차단·상태 저장 |
| `DELETE /api/connections/{id}` | 지정 SSH 세션 종료 |
| `GET /api/events` | 최근 이벤트 |

설정 저장 요청 형식은 `{"config": <GET에서 받은 config>, "revision": <GET에서 받은 revision>, "passwords": {"계정명": "새 암호"}}`입니다. `passwords` 항목을 생략하면 기존 암호를 유지하고 빈 문자열을 지정하면 암호 인증을 제거합니다. 변경된 설정을 기반으로 저장하면 HTTP 409를 반환합니다. 설정 저장 전 새 GET으로 최신 revision을 읽으세요.

## 문제 해결

- **SSH 인증 실패**: 계정 사용 여부, 공개키 원문, 사용자 이름, 관리 화면의 접속 허용 상태를 확인합니다. `ssh -v`로 클라이언트 로그를 확인합니다.
- **channel open failed**: 계정의 Local 권한과 목적지 허용 목록, 서버에서 대상 주소에 접근할 수 있는지 확인합니다.
- **remote port forwarding failed**: Reverse 권한, 포트 허용 목록, 해당 포트의 기존 사용 여부, 외부 인터페이스 공개 권한을 확인합니다. `127.0.0.1`을 명시하면 루프백으로 수신합니다.
- **Address already in use**: 같은 SSH/관리 포트를 쓰는 프로그램을 종료하거나 서버를 종료한 상태에서 `server.json`의 포트를 변경합니다.
- **데이터 폴더 사용 오류**: 동일 데이터 폴더를 사용하는 서버가 이미 실행 중인지, ACL과 파일 소유자가 올바른지 확인합니다. 심볼릭 링크·재분석 지점 데이터 경로는 허용하지 않습니다.
- **관리 화면 연결 실패**: 서버 실행 상태와 `server.json`의 `adminListen`을 확인합니다. 주소 변경 후에는 서버를 재시작하고 `Open-Admin.cmd`를 다시 실행합니다.
- **서비스 시작 실패**: 서비스를 정지한 상태에서 동일 데이터 폴더로 `run` 명령을 실행하고 표준 오류를 파일로 받으면 설정·포트·파일 권한 오류를 확인할 수 있습니다.


## 트레이·설치판

- EXE 기본 실행은 Windows GUI 트레이 모드입니다. `run`은 서비스/백그라운드 실행용으로 트레이를 만들지 않습니다. 두 모드 모두 새 콘솔을 만들지 않습니다.
- 관리 페이지는 트레이 메뉴를 선택할 때만 열립니다. 서버 시작이나 자동 재시작 시 브라우저를 열지 않습니다.
- 수신 포트 변경 후 트레이 **서버 다시 시작**을 선택합니다. 기존 터널을 종료하고 파일 잠금을 해제한 뒤 새 설정을 읽습니다.
- 업데이트/제거 시 실행 중인 트레이 프로세스를 감지합니다. 트레이 **서버 종료**로 종료한 후 진행하세요.
- 설치 제거는 사용자 데이터 폴더를 삭제하지 않습니다. 로그인 자동 실행 항목과 설치 바로가기는 제거합니다.
- GUI 프로그램의 진단 출력이 필요하면 PowerShell `Start-Process`의 `-Wait -RedirectStandardOutput -RedirectStandardError`를 사용하세요.


자동 주소 발급 API와 서버 프로그램 연결 모듈은 [TUNNEL-API.md](TUNNEL-API.md)를 참조하세요.
