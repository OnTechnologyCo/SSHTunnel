# 포트 없는 랜덤 웹 주소 · 1.3.1

브라우저용 터널은 `https://<6자리 랜덤키>.tunnel.example.com` 형식의 주소를 발급합니다. 브라우저에 별도 포트를 입력하지 않습니다. 모든 웹 터널은 공통 게이트웨이를 사용하며, 터널마다 TCP 포트를 만들지 않습니다. 서버 프로그램과 중계 서버 사이는 기존 SSH 암호화 연결을 사용합니다.

1.3.1부터 예를 들어 `https://a7k2m9.tunnel.example.com`처럼 발급합니다. 사용 중인 주소와 충돌하면 다시 생성하며, 인증키와 터널 소유권 키의 길이는 그대로 유지합니다. 1.3.0 SDK와 호환되며 서버 업데이트 후 다시 연결하면 새 6자리 주소를 받습니다.

## 동작

1. 원본 프로그램은 로컬 HTTP 서버를 실행합니다.
2. Go SDK에서 `Protocol: "http"`로 중계 서버에 접속합니다.
3. 중계 서버가 소문자 영문·숫자 6자리 식별자로 서브도메인을 발급합니다.
4. 브라우저는 반환된 `Endpoint().URL`에 접속합니다.
5. HTTPS 앞단 프록시는 Host를 보존해 웹 게이트웨이에 전달합니다. 게이트웨이는 해당 웹 터널의 SSH 채널로 요청을 전달합니다.

페이지 경로, 쿼리, HTTP 메서드, 요청 본문, 로그인용 Authorization·쿠키, 웹소켓 업그레이드와 SSE 스트리밍을 전달합니다. 루트 경로를 바꾸지 않으므로 `/assets/...` 같은 웹 리소스를 그대로 사용할 수 있습니다. 각 터널은 별도 브라우저 origin입니다.

## 필요한 공개 설정

중계 프로그램 설치만으로 DNS나 인증서가 생기지는 않습니다. 운영자가 소유한 도메인에서 한 번 설정해야 합니다.

| 항목 | 예시 |
|---|---|
| 기본 도메인 | `tunnel.example.com` |
| 와일드카드 DNS | `*.tunnel.example.com` → 중계 서버 또는 앞단 프록시의 공개 IP |
| HTTPS 인증서 | `*.tunnel.example.com`을 포함하는 인증서 |
| 외부 수신 | 앞단 HTTPS 프록시의 TCP 443 |
| 내부 웹 게이트웨이 | `127.0.0.1:17894` (기본) |
| 서버 프로그램의 SSH 접속 | 별도로 설정한 SSH 주소·포트 (기본 2222) |

와일드카드 인증서의 자동 발급은 DNS 인증 방식 등을 사용합니다. 기존 80·443 서비스가 있다면 해당 서비스를 중단하거나 포트를 뺏지 마세요. 기존 프록시에 **새 호스트 전용 사이트 블록**을 추가하거나 별도 중계 서버/IP를 사용합니다. 이미 운영 중인 프록시의 전역 설정, 기본 호스트, 인증 정보는 유지해야 합니다.

관리 화면 **서버 설정**에서:

- **자동 터널 기능 사용**과 **웹 터널 기능 사용**을 켭니다.
- 기본 도메인에는 `tunnel.example.com`만 입력합니다. `https://`, `*.`와 포트는 제외합니다.
- 게이트웨이는 기본 루프백 주소를 사용합니다. SSH·관리 화면과 다른 빈 포트여야 합니다.
- 브라우저 접속 방식은 `HTTPS`로 선택합니다. 이것은 공개 URL의 방식이며 내장 게이트웨이 자체가 TLS 인증서를 관리하는 기능은 아닙니다.
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

- SDK의 Target은 HTTP 서버입니다. 외부 HTTPS는 앞단에서 종료되고 서버 프로그램까지의 터널은 SSH로 암호화됩니다. HTTPS 전용 로컬 원본은 현재 직접 지원하지 않습니다.
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

수신기 없이 방화벽만 허용해도 웹 연결은 되지 않습니다. 도구가 수신기 없음 경고를 표시하면 HTTPS 인증서와 앞단 프록시를 먼저 구성하세요. 공유기 뒤에서는 같은 외부 포트의 포트포워딩도 필요합니다.

사용자 지정 데이터 폴더는 `powershell -File scripts/Configure-Firewall.ps1 -DataDir "D:\TunnelData" -Apply`로 지정합니다. `-Apply`를 생략하면 진단만 합니다. 기존 TCP 방식을 사용하는 경우에만 `-IncludeTcpPorts`를 추가하세요.
