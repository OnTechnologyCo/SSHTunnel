# 포트 없는 고정 웹 주소 · 1.5.0

브라우저용 터널은 `https://<6자리 랜덤키>.tunnel.example.com` 형식의 주소를 발급합니다. 브라우저에 별도 포트를 입력하지 않습니다. 모든 웹 터널은 공통 게이트웨이를 사용하며, 터널마다 TCP 포트를 만들지 않습니다. 서버 프로그램과 중계 서버 사이는 기존 SSH 암호화 연결을 사용합니다.

1.3.1부터 예를 들어 `https://a7k2m9.tunnel.example.com`처럼 발급합니다. 사용 중인 주소와 충돌하면 다시 생성하며, 인증키와 터널 소유권 키의 길이는 그대로 유지합니다. 1.3.0 SDK와 호환되며 서버 업데이트 후 다시 연결하면 새 6자리 주소를 받습니다.


## 1.5.0: PC·서비스별 주소 유지

- 최초 발급된 6자리 주소는 **계정 + 터널 식별키**에 예약합니다. 연결 종료·PC 재부팅·중계 서버 재시작·접속 인증키 교체로 바뀌지 않습니다. 오프라인이면 웹 접속은 불가능하지만 주소 예약은 유지됩니다.
- Go SDK 1.5.0은 HTTP 터널의 식별키를 사용자 설정 폴더의 `OnTech/SSHTunnel/identities`에 자동 저장합니다. Windows에서는 보통 `%APPDATA%\OnTech\SSHTunnel\identities`입니다. 항상 같은 Windows 사용자/서비스 계정으로 실행하세요.
- 한 PC의 서비스는 서로 다른 `Identity`를 사용하세요. 생략하면 `Target`을 사용하므로 로컬 포트 변경 시 주소도 달라집니다. 임의 포트를 쓰는 프로그램은 `Identity: "my-service"`처럼 변하지 않는 이름을 반드시 지정하세요. 표시용 `Name` 변경은 주소에 영향을 주지 않습니다.
- 같은 계정으로 여러 PC를 사용할 수 있습니다. 각 PC가 생성한 키가 다르므로 각자 주소를 받습니다. 다른 PC에 식별키 폴더를 복사하면 동일한 서비스로 인식되며 동시 접속은 거절됩니다.
- 기존 연동 프로그램에서 이미 `TunnelKey`를 영구 저장한다면 중계 서버 업데이트만으로 적용됩니다. 매번 새 키를 만드는 연동은 SDK를 업데이트하거나 첫 키를 저장해 재사용해야 합니다. 직접 지정한 `TunnelKey`는 SDK가 대신 저장하지 않습니다.
- `KeyDirectory`로 전용 식별키 저장 폴더를 지정할 수 있습니다. 해당 폴더의 권한을 제한하므로 다른 용도의 공용 폴더를 지정하지 마세요. `Ephemeral: true`는 매번 새 키·주소를 발급하므로 예약 한도를 소비합니다.
- 관리 화면 **서버 상태 → 예약된 웹 주소**에서 연결 중/오프라인 상태를 확인합니다. 주소 변경이 필요하면 연결 프로그램의 자동 재연결을 중지하고 연결을 종료한 뒤 **주소 재발급**을 누르세요. 다음 연결부터 같은 식별키로 새 주소를 받습니다. 폐기된 주소는 다른 프로그램에 재사용하지 않습니다.
- 예약은 자동 만료되지 않습니다. 폐기 주소를 포함해 계정당 1,024개·서버 전체 65,536개를 보관하며, 활성 터널 한도와 별개입니다. 예약 한도에 도달해도 기존 주소 재접속은 가능합니다.
- 중계 서버의 데이터 폴더에 `web-addresses.json`을 저장합니다. 식별키 원문은 저장하지 않습니다. 저장 실패 시 주소를 발급하지 않으며 손상된 파일을 새 파일로 덮어쓰지 않습니다.

### 업데이트와 백업

1. 중계 서버를 종료한 뒤 1.5.0 설치 파일로 업데이트하고 다시 실행합니다. 기존 계정·인증서·설정은 유지합니다.
2. 연결 프로그램의 Go SDK를 1.5.0으로 교체하거나 `TunnelKey`를 영구 저장하도록 수정합니다. SDK 소스를 복사할 때 `internal/securefile`도 함께 포함하세요.
3. 프로그램별 고정 `Identity`로 연결해 주소를 확인합니다. **1.4.0 이하의 활성 주소는 디스크에 저장되지 않았으므로 이번 업데이트 후 최초 연결에서 한 번 바뀔 수 있습니다.**
4. 클라이언트의 식별키 폴더와 중계 서버의 전체 데이터 폴더를 함께 백업하세요. 파일 삭제·다른 OS 계정으로 실행·SSH 호스트 키 또는 접속 계정 변경 시에는 기존 주소가 복원되지 않을 수 있습니다. 웹 기본 도메인을 바꾸면 URL의 도메인 부분도 바뀝니다.

```go
session, err := tunnel.Open(ctx, tunnel.Options{
    Server: relay, User: user, AccessKey: accessKey, Fingerprint: fingerprint,
    Protocol: "http", Target: "127.0.0.1:9000", Identity: "my-service",
})
// 끊겼다면 같은 Options로 Open을 다시 호출합니다. URL 예약은 유지됩니다.
```

## 1.4.0: 별도 웹 서버 없이 내장 HTTPS 사용

1. 기존 프로그램을 종료하고 최신 설치 파일로 업데이트합니다. 기존 데이터와 계정은 유지됩니다.
2. 서버 설정에서 자동 터널·웹 터널을 켜고 웹 기본 도메인을 입력합니다. 예: `tunnel.ontechdev.com`.
3. HTTPS 처리 방식을 **내장 HTTPS**, 공개 수신 주소를 **0.0.0.0:443**, 내부 HTTP 주소를 **127.0.0.1:17894**로 저장합니다. 트레이에서 서버를 다시 시작합니다.
4. 같은 화면의 **내장 HTTPS · 인증서**에서 Cloudflare 영역 도메인(예: `ontechdev.com`)과 API 토큰을 입력합니다. 토큰 권한은 해당 영역의 **Zone Read / DNS Edit**로 제한합니다. 선택한 도메인의 DNS 인증용 TXT 레코드만 생성·삭제하며 기존 A/CNAME은 변경하지 않습니다.
5. Let's Encrypt 이용약관에 동의하고 **인증서 발급 · HTTPS 적용**을 누릅니다. 발급 상태와 오류는 화면에 표시됩니다. DNS 전파에 몇 분 걸릴 수 있습니다. 인증서가 발급되면 재시작 없이 적용됩니다.
6. 설치 폴더의 `Configure-Firewall.cmd` 또는 시작 메뉴의 **접속 포트 방화벽 설정**을 실행합니다. 내장 HTTPS 포트는 SSHTunnelServer.exe에 한정해 허용합니다.
7. 서버 프로그램을 다시 연결하고 새로 발급된 `https://6자리.tunnel.ontechdev.com` 주소로 접속합니다. 기본 도메인이나 IP만 입력하면 개별 터널이 선택되지 않습니다.

Cloudflare에는 `*.tunnel` DNS 레코드를 중계 서버 IP로 연결합니다. 직접 접속 구성은 DNS 전용을 사용하세요. 브라우저가 신뢰하는 공인 인증서를 DNS-01 방식으로 발급하므로 인증서 발급용 80 포트나 별도 Caddy/IIS 설치가 필요하지 않습니다. Cloudflare API, Let's Encrypt(HTTPS), 공개 DNS(1.1.1.1/8.8.8.8)로 나가는 연결은 필요합니다. `_acme-challenge`가 다른 DNS에 위임된 구성은 이 자동 발급 기능에서 지원하지 않습니다.

자동 갱신을 켜면 서버 실행 중 매시간 확인하여 인증서 유효기간의 마지막 1/3에 갱신을 시도합니다. 실패하면 기존 인증서를 유지하고 최소 6시간 후 재시도합니다. PC가 꺼져 있으면 갱신할 수 없습니다. 토큰 삭제 버튼으로 자동 갱신을 중지할 수 있습니다. 수동 인증서 등록도 자동 갱신을 중지합니다.

이미 인증서가 있으면 인증서 체인 PEM과 개인키 PEM을 직접 등록할 수 있습니다. 인증서는 `*.웹기본도메인`을 포함하고 키가 일치하며 유효기간 내여야 합니다. DNS 전용 직접 접속에는 Cloudflare Origin 인증서나 자체 서명 인증서가 브라우저에서 신뢰되지 않으므로 공인 인증서를 사용하세요. 토큰·개인키는 데이터 폴더의 `https` 하위에 접근 권한을 제한해 보관하며 관리 API 응답·이벤트 로그에는 포함하지 않습니다. 토큰은 채팅이나 GitHub에 올리지 마세요.

443 포트 충돌 시 다른 프로그램을 종료하거나 기존 서비스를 변경하지 않습니다. HTTPS 오류를 관리 화면에 표시하고 SSH/관리 화면은 계속 실행합니다. 기존 1.3.x 설정은 **외부 프록시 방식**으로 유지하므로 업데이트 후 내장 HTTPS를 직접 선택해야 합니다. 내장 HTTPS의 포트 수신이나 인증서가 준비되지 않으면 동작하지 않는 웹 URL을 새로 발급하지 않습니다. 관리 화면의 수신 준비 상태는 인터넷 경로·공유기·방화벽까지 보증하는 외부 접속 검사는 아닙니다.

## 동작

1. 원본 프로그램은 로컬 HTTP 서버를 실행합니다.
2. Go SDK에서 `Protocol: "http"`로 중계 서버에 접속합니다.
3. 중계 서버가 소문자 영문·숫자 6자리 식별자로 서브도메인을 발급합니다.
4. 브라우저는 반환된 `Endpoint().URL`에 접속합니다.
5. 내장 HTTPS가 직접 요청을 처리하거나 기존 HTTPS 앞단 프록시가 Host를 보존해 웹 게이트웨이에 전달합니다. 게이트웨이는 해당 웹 터널의 SSH 채널로 요청을 전달합니다.

페이지 경로, 쿼리, HTTP 메서드, 요청 본문, 로그인용 Authorization·쿠키, 웹소켓 업그레이드와 SSE 스트리밍을 전달합니다. 루트 경로를 바꾸지 않으므로 `/assets/...` 같은 웹 리소스를 그대로 사용할 수 있습니다. 각 터널은 별도 브라우저 origin입니다.

## 필요한 공개 설정

중계 프로그램 설치만으로 DNS나 인증서가 생기지는 않습니다. 운영자가 소유한 도메인에서 한 번 설정해야 합니다.

| 항목 | 예시 |
|---|---|
| 기본 도메인 | `tunnel.example.com` |
| 와일드카드 DNS | `*.tunnel.example.com` → 중계 서버 또는 앞단 프록시의 공개 IP |
| HTTPS 인증서 | `*.tunnel.example.com`을 포함하는 인증서 |
| 외부 수신 | 내장 HTTPS 또는 앞단 프록시의 TCP 443 |
| 내부 웹 게이트웨이 | `127.0.0.1:17894` (기본) |
| 서버 프로그램의 SSH 접속 | 별도로 설정한 SSH 주소·포트 (기본 2222) |

와일드카드 인증서의 자동 발급은 DNS 인증 방식 등을 사용합니다. 기존 80·443 서비스가 있다면 해당 서비스를 중단하거나 포트를 뺏지 마세요. 기존 프록시에 **새 호스트 전용 사이트 블록**을 추가하거나 별도 중계 서버/IP를 사용합니다. 이미 운영 중인 프록시의 전역 설정, 기본 호스트, 인증 정보는 유지해야 합니다.

관리 화면 **서버 설정**에서:

- **자동 터널 기능 사용**과 **웹 터널 기능 사용**을 켭니다.
- 기본 도메인에는 `tunnel.example.com`만 입력합니다. `https://`, `*.`와 포트는 제외합니다.
- 게이트웨이는 기본 루프백 주소를 사용합니다. SSH·관리 화면과 다른 빈 포트여야 합니다.
- 브라우저 접속 방식은 `HTTPS`로 선택합니다. 1.4.0의 **내장 HTTPS**에서는 이 프로그램이 인증서를 관리합니다. **외부 프록시** 방식은 별도 웹 서버가 인증서를 처리합니다.
- **DNS·프록시 설정 예시 보기**에서 저장된 설정에 맞는 예시를 확인합니다.
- 설정을 저장하고 트레이에서 서버를 다시 시작합니다.

기존 TCP 포트 방식의 공개 IP와 배정 범위는 웹 터널 개설에 사용하지 않습니다. 웹 터널만 쓴다면 TCP 공개 호스트를 비워도 됩니다. 이 경우 SDK의 `Server`에는 운영자가 SSH 접속 주소를 직접 지정합니다.

## 기존 Caddy 앞단에 연결하는 예시

아래는 **예시 파일**이며 현재 운영 프록시에 자동 적용하지 않습니다. 준비된 와일드카드 인증서와 실제 경로를 사용하세요.

```caddyfile
https://*.tunnel.example.com {
    tls C:/certs/tunnel-fullchain.pem C:/certs/tunnel-private.key
    reverse_proxy 127.0.0.1:17894
}
```

앞단은 원래 Host를 유지해야 합니다. `header_up Host localhost`처럼 호스트를 바꾸면 터널을 찾을 수 없습니다. 다른 서버가 80을 사용한다면 새 프록시의 자동 HTTP→HTTPS 리다이렉트 리스너가 충돌하지 않도록 그 프록시의 기존 정책을 확인하세요. 위 블록만 복사했다고 DNS·인증서·공유기 설정까지 완료되는 것은 아닙니다.

HTTP만 사용하는 별도 테스트 환경은 공개 방식 `http`, 게이트웨이 수신 `0.0.0.0:80`으로 구성할 수 있으나, 반드시 80 포트가 비어 있어야 합니다. 기본값은 기존 서버와 충돌을 피하는 루프백 수신입니다.

## Go 연결 코드

```go
session, err := tunnel.Open(ctx, tunnel.Options{
    Protocol:    "http",
    Server:      "relay.example.com:2222",
    User:        "my-web-app",
    AccessKey:   accessKey,
    Fingerprint: serverFingerprint,
    Target:      "127.0.0.1:9000", // 기존 HTTP 서버
    Name:        "내 웹 프로그램",
})
if err != nil { return err }
defer session.Close()
url := session.Endpoint().URL // https://랜덤키.tunnel.example.com
```

`examples/http-server`는 간단한 웹서버, `examples/web-demo`는 브라우저 왕복 테스트 페이지입니다. 기존 웹 프로그램을 수정하기 어렵다면 다음 연결 예제를 옆에서 실행할 수 있습니다.

```powershell
$env:TUNNEL_ACCESS_KEY = '관리 화면에서 발급한 값'
go run ./examples/publish -protocol http -server relay.example.com:2222 -user my-web-app -target 127.0.0.1:9000 -fingerprint 'SHA256:실제서버지문'
```

## 원본 웹 프로그램 설정

- SDK의 Target은 HTTP 서버입니다. 외부 HTTPS는 내장 수신기 또는 앞단 프록시에서 종료되고 서버 프로그램까지의 터널은 SSH로 암호화됩니다. HTTPS 전용 로컬 원본은 현재 직접 지원하지 않습니다.
- Host는 발급받은 도메인으로 전달됩니다. Host 허용 목록이 있는 프레임워크는 이 도메인을 허용하세요.
- `X-Forwarded-Proto`, `X-Forwarded-Host`, `X-Forwarded-Port`로 공개 URL을 알 수 있습니다. 원본 앱의 프록시 신뢰 설정을 자신의 터널 연결에 한정하세요.
- 외부에서 보낸 위조 Forwarded 계열 헤더는 제거합니다. `X-Forwarded-For`에는 게이트웨이에 직접 접속한 피어 IP가 들어갑니다. 앞단 프록시 뒤에서는 그 프록시 주소가 보입니다.
- 다른 터널의 쿠키를 덮어쓰지 않도록 응답 쿠키의 Domain 속성을 제거해 해당 호스트 전용으로 만듭니다. Secure·HttpOnly·SameSite·Path 속성은 유지합니다. 부모 도메인 공유 쿠키를 이용하는 SSO 앱은 별도 구성이 필요합니다.
- 앱이 localhost 주소나 고정 도메인을 HTML에 직접 쓰면 자동 변경하지 않습니다. 앱의 공개 URL 설정을 발급된 URL로 맞추세요. 같은 공개 호스트로 보내는 절대 리다이렉트의 http/https 방식은 공개 설정으로 맞춥니다.
- 업로드 한도는 기본 128MB이며 관리 화면에서 1~2048MB로 설정합니다. 앞단 프록시가 더 작은 제한을 가지면 앞단 제한이 먼저 적용됩니다. 응답 헤더 대기 시간은 30초이고 SSE·웹소켓은 연결 후 스트리밍됩니다.
- 랜덤 주소는 로그인 기능이 아닙니다. 중요한 웹 서비스에는 원본 앱의 인증을 사용하세요. 같은 기본 도메인의 서브도메인은 브라우저의 same-site 정책을 공유하므로 쿠키 격리와 별도로 원본 앱의 CSRF 방어가 필요합니다.

## 연결 수명과 호환성

터널·서버 종료, 계정 정책 변경, SSH 연결 해제 시 URL과 활성 웹소켓도 닫힙니다. 재접속하면 새 주소가 발급됩니다. 자동 재접속과 고정 서브도메인은 제공하지 않습니다.

SDK에서 Protocol을 생략하면 이전 버전과 동일한 TCP 포트 방식입니다. TCP 클라이언트와 기존 SSH `-L`/`-R`/`-D` 기능은 유지합니다. 웹 터널은 서버와 SDK 모두 1.3.0 이상이 필요합니다.

## 참고 문서

- Go ReverseProxy: https://pkg.go.dev/net/http/httputil#ReverseProxy
- Caddy 와일드카드 인증서: https://caddyserver.com/docs/caddyfile/patterns#wildcard-certificates

## Windows 방화벽 설정

설치 완료 화면의 **접속 포트 방화벽 허용** 옵션은 기본 체크되어 있습니다. 관리자 권한 승인 후 저장된 설정의 포트를 적용합니다. 새 설치는 기본 SSH 포트 2222를 사용하므로, 이후 수신 포트를 바꾸거나 웹 터널을 켜면 방화벽 설정 도구를 다시 실행하세요. 무인 설치에서는 이 대화형 단계를 생략합니다.

실제 중계 서버 PC에서 시작 메뉴의 **접속 포트 방화벽 설정** 또는 설치 폴더의 `Configure-Firewall.cmd`를 실행하세요. UAC 권한 승인을 거쳐 저장된 SSH 포트와 웹 방식에 따른 TCP 443(HTTPS) 또는 80(HTTP)을 허용합니다. 기본적으로 관리 포트·내부 게이트웨이·TCP 배정 범위는 공개하지 않습니다.

수신기 없이 방화벽만 허용해도 웹 연결은 되지 않습니다. 도구가 수신기 없음 경고를 표시하면 내장 HTTPS와 인증서를 설정하거나 기존 앞단 프록시를 구성하세요. 공유기 뒤에서는 같은 외부 포트의 포트포워딩도 필요합니다.

사용자 지정 데이터 폴더는 `powershell -File scripts/Configure-Firewall.ps1 -DataDir "D:\TunnelData" -Apply`로 지정합니다. `-Apply`를 생략하면 진단만 합니다. 기존 TCP 방식을 사용하는 경우에만 `-IncludeTcpPorts`를 추가하세요.

## Built-in HTTPS (English)

Version 1.4.0 can terminate HTTPS in the tunnel server itself. Choose **native** TLS, keep the HTTP gateway on loopback, set the HTTPS listener to `0.0.0.0:443`, save, and restart the tray server. Existing configurations retain proxy mode until explicitly changed.

Use the local admin page to import a valid wildcard certificate/key pair or issue one from Let's Encrypt using a Cloudflare API token scoped to **Zone Read and DNS Edit** for the relevant zone. Accept the CA terms in the form. Only temporary DNS-01 TXT records created by this operation are deleted; existing A/CNAME records are preserved. DNS delegation/CNAME challenge delegation is not supported. The relay needs outbound HTTPS and access to public DNS resolvers. Public CA issuance and real external reachability require a real authorized domain/token and are separate from offline integration tests.

Certificates activate without disconnecting existing tunnels. Automatic renewal checks hourly during the final third of the certificate lifetime and retries no more often than every six hours after failures. Tokens and keys are stored in ACL-restricted files and omitted from admin responses. Manually imported certificates must be publicly trusted for direct browser access; Cloudflare Origin CA certificates alone are not browser-trusted. Firewall configuration remains available from the installer and Start menu. Other processes occupying the HTTPS port are never terminated.

### Persistent identity (1.5.0)

HTTP addresses are reserved on disk by account and ownership key, including while offline. The 1.5.0 SDK persists a random key per relay fingerprint, account and service `Identity` (defaults to `Target`) in the OS user configuration directory. Set a stable `Identity` when local ports change. Keep both client identity files and the relay data directory backed up. Older integrations must reuse their `TunnelKey`; a server-only upgrade cannot identify a client that generates a fresh key every time. Upgrading from 1.4.0 may change the URL once because older addresses were memory-only. TCP port allocation is unchanged.
