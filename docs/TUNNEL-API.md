# 자동 주소 발급 터널 API · 1.3.0

## 브라우저용 웹 터널

1.3.0부터 `tunnel.Options{Protocol: "http"}`로 포트 없는 랜덤 서브도메인 URL을 발급합니다. DNS·HTTPS 앞단 구성과 예제는 [WEB-TUNNELS.md](WEB-TUNNELS.md)를 확인하세요. 아래 설명은 Protocol을 생략하거나 `tcp`로 지정한 기존 TCP 방식입니다.

## 요청한 연결 구조

서버 프로그램은 자신의 기존 TCP 서비스를 실행한 뒤 Go 연결 모듈로 중계 서버에 먼저 접속합니다. 모듈은 임의 터널 식별키를 만들고 개설 API를 호출합니다. 중계 서버는 포트를 배정하고 `공개호스트:포트`를 반환합니다. 외부 클라이언트는 이 주소로 일반 TCP 접속하고, 연결 모듈이 데이터를 서버 프로그램의 로컬 서비스로 전달합니다.

서버 프로그램 쪽에는 인바운드 포트 개방이 필요하지 않습니다. 중계 서버 쪽 SSH 포트와 공개 터널 포트 범위는 외부에서 도달 가능해야 합니다. HTTP, 자체 TCP 프로토콜, DB, RDP 등 원본 서비스의 TCP 바이트를 그대로 전달합니다. UDP와 VPN은 지원하지 않습니다. 웹 URL 발급은 별도 HTTP 모드에서 지원하며 DNS·인증서는 운영자가 설정합니다.

## 중계 서버 최초 설정

1. 트레이 → **관리 화면 열기 → 서버 설정**으로 이동합니다.
2. **자동 터널 기능 사용**을 켭니다.
3. **공개 IP / 도메인**에 외부 클라이언트가 접속할 중계 서버의 IP 또는 도메인을 입력합니다. `http://`와 포트는 제외합니다.
4. 수신 IP와 배정 포트 범위를 정합니다. 기본값은 `0.0.0.0`, `20000~20199`입니다. 방화벽과 공유기/NAT에서 동일한 포트 번호로 허용·전달합니다. 프로그램이 외부 IP를 추측하거나 공유기 설정을 자동 변경하지 않습니다.
5. **접속 계정 → 자동 터널용 인증키 발급**에서 계정과 인증키를 만듭니다. 새 계정은 자동 터널만 허용하며 일반 Local/SOCKS/Reverse 권한은 주지 않습니다.
6. 발급된 `user`, `accessKey`, `fingerprint`, `serverAddress`를 서버 프로그램에 넣습니다. 공개 호스트를 아직 설정하지 않았다면 `serverAddress`가 비어 있으므로 실제 중계 접속 주소를 입력하세요.

인증키는 발급할 때 한 번 표시하며 서버에는 SHA-256 해시만 보관합니다. 키 재발급은 이전 키를 폐기하고 현재 SSH 연결을 종료합니다. 외부 클라이언트에는 발급된 **접속 주소만** 전달하고 인증키를 전달하지 않습니다. 공개 주소는 접근 비밀이 아니므로 필요한 클라이언트 인증은 원본 서버 프로그램에서 수행하세요.

## 두 종류의 키

| 항목 | 생성·보관 | 역할 |
|---|---|---|
| `AccessKey` | 관리 화면에서 발급, 서버 프로그램이 안전하게 보관 | 중계 서버 계정 인증 |
| `TunnelKey` | 연결 모듈이 Open마다 자동 생성, 필요하면 호출자가 지정 | 동시에 열린 터널의 식별·중복 방지 |

TunnelKey만으로 중계 서버에 인증하거나 다른 연결의 터널을 종료할 수 없습니다. 같은 키가 사용 중이면 새 개설을 거부합니다. 연결이 종료되면 키와 포트를 반환하므로 같은 키로 다시 열 수 있지만 주소/포트가 유지된다고 보장하지 않습니다.

## Go 서버 프로그램에 넣기

모듈 소스는 `pkg/tunnel`입니다. 이 프로젝트 안에서는 다음 import를 사용합니다.

```go
import "ontech/sshtunnel/pkg/tunnel"
```

별도 프로젝트에서 로컬 소스를 참조하려면 그 프로젝트의 `go.mod`에 다음을 추가하고 `go mod tidy`를 실행합니다. 대상 프로젝트도 Go 1.26 이상이 필요합니다.

```go
require ontech/sshtunnel v0.0.0
replace ontech/sshtunnel => D:/src/SSHTunnel
```

기존 서버가 `127.0.0.1:9000`에 수신 중이라고 가정합니다.

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

session, err := tunnel.Open(ctx, tunnel.Options{
    Server:      "relay.example.com:2222",
    User:        "my-app",
    AccessKey:   os.Getenv("TUNNEL_ACCESS_KEY"),
    Fingerprint: "SHA256:관리화면의실제호스트지문",
    Target:      "127.0.0.1:9000",
    Name:        "my-tcp-server",
    // TunnelKey: 비우면 암호학적으로 안전한 256비트 키를 생성합니다.
})
if err != nil {
    return err
}
defer session.Close()

address := session.Endpoint().Address // 예: relay.example.com:20037
// address를 외부 클라이언트에 전달합니다.
// 서버 프로그램은 기존 TCP 리스너로 평소처럼 처리합니다.

if err := session.Wait(); err != nil {
    // 중계 연결이 끊겼습니다. 필요하면 새 Open 후 새 주소를 공지합니다.
    return err
}
```

`session.Key()`는 생성된 TunnelKey, `session.Endpoint()`는 ID·이름·주소·호스트·포트를 반환합니다. `Done()`으로 종료를 감시하고 `Err()`로 중계 종료 오류를 확인합니다. `LastStreamError()`는 로컬 대상 접속 실패 같은 최근 개별 연결 오류를 반환합니다. `MaxConnections`는 모듈 측 동시 전달 한도로 기본 64입니다. 중계 서버의 계정·채널 한도도 적용됩니다.

## 실행 가능한 예제

프로젝트 폴더에서 PowerShell로 실행합니다. 아래 인증키·호스트 지문은 실제 발급값으로 바꾸세요. 인증키를 소스 저장소에 넣지 마세요.

```powershell
$env:TUNNEL_ACCESS_KEY = '발급받은-64자리-인증키'

go run ./examples/publish -server relay.example.com:2222 -user my-app -target 127.0.0.1:9000 -fingerprint 'SHA256:실제지문' -name my-service
```

이 예제는 이미 실행 중인 TCP 서버를 공개합니다. 별도 서버 설치 프로그램이 아니며 Go 모듈의 사용 예제입니다.

자체 HTTP 서버가 실행 후 웹 터널까지 여는 전체 예제 (1.3.0부터 웹 기본 도메인 설정 필요):

```powershell
$env:TUNNEL_SERVER = 'relay.example.com:2222'
$env:TUNNEL_USER = 'my-app'
$env:TUNNEL_ACCESS_KEY = '발급받은-64자리-인증키'
$env:TUNNEL_FINGERPRINT = 'SHA256:실제지문'
go run ./examples/http-server
```

출력된 `http://공개호스트:배정포트`로 일반 브라우저 또는 HTTP 클라이언트가 접속하면 서버 프로그램의 응답을 받습니다. 서버 프로그램에 기존 클라이언트 통신 코드가 있다면 출력된 주소와 포트만 전달하면 됩니다.

## 다른 언어에서 구현할 때: 전송 프로토콜

이 API는 **SSH 연결 안의 JSON 전역 요청**입니다. 별도의 외부 HTTP 관리 API를 노출하지 않습니다. Go 모듈이 SSH 인증·API 요청·데이터 채널·종료 처리를 감춥니다. 다른 언어는 아래 SSH 확장 메시지를 구현해야 합니다.

1. 중계 서버 SSH 포트에 연결하고 저장한 호스트 지문을 검증합니다.
2. SSH 사용자 이름과 password 인증 방식으로 AccessKey를 전달합니다. 이는 SSH로 암호화되며 일반 계정 암호와 별도로 검증합니다.
3. `tunnel-open@ontech.dev` 전역 요청을 `want-reply=true`로 전송합니다.

요청 JSON:

```json
{"key":"64자리 소문자 hex 터널 식별키","name":"my-service"}
```

성공 응답(`success=true`) JSON 예:

```json
{"id":"서버가생성한24자리ID","name":"my-service","address":"relay.example.com:20037","host":"relay.example.com","port":20037}
```

실패는 `success=false`와 `{"error":"사유"}`입니다. 요청 크기는 최대 4096바이트, 이름은 제어문자 없이 최대 160바이트입니다.

4. 외부 TCP 접속마다 중계 서버가 `tunnel-connection@ontech.dev` SSH 채널을 엽니다. ExtraData는 `{"tunnelId":"...","origin":"접속IP:포트"}` JSON입니다. ID를 검사하고 로컬 TCP 서비스에 연결한 다음 채널을 수락해 양방향 복사합니다. 한 방향 EOF는 반대 방향 응답을 기다리는 half-close로 전달해야 합니다.
5. 명시 종료는 `tunnel-close@ontech.dev` 전역 요청에 `{"id":"..."}`를 보냅니다. 해당 SSH 연결이 소유한 터널만 종료할 수 있습니다. SSH 연결 자체를 닫아도 모든 소유 터널이 정리됩니다.
6. 중계 서버가 터널을 닫으면 `tunnel-closed@ontech.dev` 전역 요청(`want-reply=false`)과 `{"id":"..."}` 알림을 보냅니다. `keepalive@openssh.com` 요청에도 응답해야 합니다.

SDK의 각 Open은 독립된 SSH 연결 한 개와 자동 터널 한 개를 사용합니다. 여러 Open으로 여러 서비스를 동시에 공개할 수 있습니다. 원시 API는 한 SSH 연결에 여러 터널을 만들 수도 있으며 연결당 한도를 공유합니다.

## 수명과 운영 한계

- 활성 SSH 연결이 유지되는 동안만 공개 주소가 유효합니다. 재접속 시 포트가 달라질 수 있으며 자동 재접속·주소 공지는 애플리케이션에서 처리합니다.
- 관리자 터널 종료는 해당 터널만 닫습니다. 계정·설정 저장 및 키 교체는 기존 SSH 연결을 모두 재인증시킵니다.
- 전체 접속 중지는 자동 터널도 함께 닫으며 서버 재시작 후에도 유지됩니다.
- SDK의 Target은 연결 모듈이 있는 PC에서 접근 가능한 TCP 주소입니다. 중계 서버가 Target에 직접 접속하지 않습니다.
- 서버 프로그램 ↔ 중계 서버는 SSH로 암호화됩니다. 외부 클라이언트 ↔ 공개 포트 구간은 원본 프로토콜을 그대로 사용하므로 TLS가 필요하면 원본 서비스에서 TLS를 제공해야 합니다.
- 클라이언트의 원본 IP는 SSH 채널 메타데이터에 포함되지만 현재 SDK는 PROXY protocol 헤더를 삽입하지 않습니다. 원본 TCP 서버에는 연결 모듈의 로컬 접속 주소가 보입니다.

## 관리자 API

기존 로컬 관리 인증 규칙을 사용합니다.

| 요청 | 기능 |
|---|---|
| `POST /api/agents` | `{name,maxConnections,revision}`으로 자동 터널 계정·인증키 생성 |
| `POST /api/users/{name}/access-key` | `{revision}`으로 키 교체 및 자동 터널 권한 활성화 |
| `GET /api/tunnels` | 현재 터널 ID·주소·계정·연결 수 |
| `DELETE /api/tunnels/{id}` | 해당 터널과 활성 TCP 연결 종료 |

GET 설정 응답에는 인증키·인증키 해시를 반환하지 않습니다. 이 키는 관리자 토큰과 별개입니다.

## 1.3.0 확장 필드

개설 JSON에 `"protocol":"http"`를 추가하면 웹 터널을 엽니다. 생략하면 기존 TCP입니다. 응답에는 `protocol`, `url` 필드가 추가되며, 웹 모드의 `address`와 `host`는 포트 없는 서브도메인, `port`는 0, `url`은 브라우저에서 사용할 전체 URL입니다. 별도 TCP 포트 대신 HTTP Host로 라우팅하며 같은 SSH 채널 프로토콜을 사용합니다. TLS 원본이 아닌 로컬 HTTP 서버를 Target으로 지정하세요.
