# SSHTunnel 접속·주소 발급·프로그램 연동 가이드 — LLM용 단일 문서

- 작성일: 2026-10-23
- 기준: SSHTunnel 서버·Go SDK v1.5.0, 소스 커밋 38b7873
- 대상: 기존 웹/API/MCP/데스크톱 프로그램에 외부 접속 기능을 추가하는 개발자·LLM
- 목표: 로컬 서비스를 실행하고, 중계 서버에 접속하여 공개 URL을 받은 뒤, 연결이 끊겨도 같은 URL로 복구한다.
- 이 문서는 다른 PC에서도 사용할 수 있다. 특정 개발 PC의 D 드라이브나 설치 경로에 의존하지 않는다.
- 문서에 실제 AccessKey, TunnelKey, 관리자 토큰은 포함하지 않는다. 접속 권한은 운영자가 별도로 제공해야 한다.

## 1. LLM이 먼저 이해할 내용

**기존 중계 서버를 이용한다면, 공개하려는 프로그램 PC에 SSHTunnelServer를 설치하지 않는다.** 그 PC에는 원본 서비스와 연결 모듈만 필요하다.

구성은 세 부분이다.

| 구성 요소 | 실행 위치 | 역할 |
|---|---|---|
| 중계 서버 | 외부에서 접속 가능한 서버 PC | SSH 연결 수신, 웹 주소 예약, HTTPS 처리, 트래픽 중계 |
| 원본 서비스 + 연결 모듈 | 공개하려는 프로그램이 있는 PC | 로컬 HTTP 서비스를 열고 SSH로 중계 서버에 먼저 연결 |
| 외부 브라우저/API/MCP 클라이언트 | 다른 PC 또는 휴대폰 | 발급된 HTTPS URL로 접속 |

연결 모듈이 `127.0.0.1:9000`을 Target으로 지정하면, 그 주소는 **연결 모듈이 실행되는 PC**의 9000 포트다. 중계 서버의 localhost가 아니다.

주소 발급은 REST API 호출이 아니라 **SSH 연결 내부의 확장 요청**이다. `POST /register`, `POST /tunnel` 등의 가상 API를 만들거나 호출하지 말고, 아래 Go SDK 또는 연결 도우미를 사용한다. MCP의 `/register`는 해당 MCP 앱의 인증 기능일 수 있으며 터널 주소 발급과 별개다.

웹/API/MCP에는 `Protocol: "http"`를 사용한다. 반환된 `session.Endpoint().URL`이 외부 접속 주소다. 예: `https://a7k2m9.tunnel.example.com`. 예시 주소를 고정값으로 사용하거나 6자리 ID를 클라이언트에서 만들어서는 안 된다.

## 2. 구현 전에 확보할 설정

기존 앱의 설정·환경변수·비밀 저장소를 먼저 확인한다. 값이 없으면 운영자에게 필요한 항목만 요청한다. 인증키를 추측하거나 기존 계정 키를 임의 재발급하지 않는다.

| 설정 | 필수 여부 | 값·규칙 |
|---|---|---|
| Server | 필수 | 중계 SSH 주소. `호스트:SSH포트` 형식. `https://`를 붙이지 않는다. |
| User | 필수 | 중계 서버 관리 화면에서 만든 접속 계정. Windows 로그인 계정과 별개다. |
| AccessKey | 필수 | 관리 화면에서 발급한 64자리 소문자 16진수 인증키. 일반 SSH 개인키와 다르다. |
| Fingerprint | 필수 | 관리 화면/신뢰할 수 있는 운영자가 제공한 SSH 호스트 지문 `SHA256:...` |
| Target | 필수 | 기존 로컬 **HTTP** 서비스 주소. 예: `127.0.0.1:9000`. `http://`를 붙이지 않는다. |
| Identity | 필수로 지정할 것 | 변하지 않는 앱·서비스 식별명. 예: `myapp-web`. 버전·임시 포트·실행 시각을 넣지 않는다. |
| KeyDirectory | 이 가이드에서는 필수 | PC에 남는 앱 전용 식별키 폴더의 절대경로. 실행 계정에 접근 권한이 있어야 한다. |
| Name | 선택 | 관리 화면 표시 이름. 예: `회사노트북 / MyApp`. 주소 식별 기준이 아니다. |
| Protocol | 웹에서는 필수 | `http`. 생략하면 TCP 모드가 된다. |
| MaxConnections | 선택 | SDK 동시 전달 수. 기본 64, 범위 1~1024. 서버 한도도 적용된다. |

이 프로젝트에서 사용해 온 중계 접속 설정은 `tunnel.ontechdev.com:39333`, 웹 기본 도메인은 `tunnel.ontechdev.com`이다. **운영자가 현재 제공한 값이 우선**이며, 계정·AccessKey·Fingerprint는 별도로 받아야 한다. 제품의 기본 SSH 포트 2222와 운영 포트 39333을 혼동하지 않는다.

SSH 호스트 지문과 브라우저 HTTPS 인증서는 다른 항목이다. HTTPS 인증서 지문을 Fingerprint에 넣거나 SSH 호스트 검증을 생략하지 않는다.

## 3. 주소를 유지하는 규칙 — 가장 중요

서버는 웹 주소를 **접속 계정 + TunnelKey**에 예약한다. PC 이름이나 IP로 동일 PC를 판단하지 않는다.

SDK v1.5.0의 기본 저장 동작은 다음과 같다.

1. `Fingerprint + User + Identity`를 JSON 배열로 만들고 SHA-256으로 키 파일 이름을 정한다.
2. 전용 폴더에서 기존 `<해시>.key`를 읽는다.
3. 파일이 없을 때만 암호학적 난수 32바이트를 64자리 소문자 hex로 만들어 저장한다.
4. 이후 연결에서는 같은 값을 사용한다. 서버는 저장된 예약 주소를 반환한다.

따라서 다음 조건을 지킨다.

- `Identity`는 제품 내부의 고정 문자열로 정한다. 같은 PC에 서로 다른 서비스가 있으면 서로 다른 Identity를 쓴다.
- 각 PC는 **자신의 키를 최초 실행 시 생성**한다. 배포 ZIP·설치 파일·PC 복제 이미지에 이미 생성된 키를 포함하지 않는다.
- `KeyDirectory`는 앱 전용 영구 데이터 폴더를 사용한다. 임시 폴더, 빌드 출력 폴더, 버전별 설치 폴더, 자동 정리 폴더를 쓰지 않는다.
- SDK가 폴더·파일 접근 권한을 제한한다. `C:\ProgramData` 전체나 다른 앱이 공유하는 폴더를 지정하지 말고 앱 전용 하위 폴더를 지정한다.
- 서비스로 운영하면 항상 같은 서비스 계정으로 실행한다. GUI 사용자와 SYSTEM이 번갈아 기본 사용자 폴더를 쓰면 서로 다른 키가 생길 수 있다.
- `Ephemeral`은 false로 둔다. `tunnel.NewKey()`를 재접속마다 호출하지 않는다.
- `Name` 변경이나 IP 변경은 키 식별 기준이 아니다. Target 포트가 바뀌어도 고정 Identity와 같은 키 파일을 쓰면 주소가 유지된다.
- 같은 계정에서 AccessKey만 교체해도 기존 TunnelKey를 유지하면 주소 예약이 유지된다.
- User, Identity, Fingerprint, 키 보관 위치를 바꾸면 SDK가 다른 키 파일을 찾을 수 있다. 특히 서버 SSH 호스트 키 교체 시 자동으로 새 키를 만들기 전에 이전 식별키 이관을 검토한다.
- 직접 `TunnelKey`를 지정하면 SDK가 대신 파일에 저장하지 않는다. 직접 영구 저장·재사용하거나 `tunnel.LoadOrCreateKey(전용파일경로)`로 읽은 값을 사용한다.
- 키 손상·권한 오류를 새 키 생성으로 우회하지 않는다. 기존 파일을 보존하고 권한 또는 백업을 복구한다.
- 앱 업데이트·제거 후 재설치 과정에서 키를 무조건 삭제하지 않는다. 주소 유지가 필요한 업데이트는 설정과 키를 보존한다.

기본 KeyDirectory를 생략했을 때의 위치는 `os.UserConfigDir()/OnTech/SSHTunnel/identities`다. Windows 일반 사용자에서는 보통 `%APPDATA%\OnTech\SSHTunnel\identities`지만 서비스 계정의 경로는 다르다.

**이미 운영 중인 앱의 변경 시:** 기존 Identity와 키 폴더부터 조사한다. 아래 예제의 `myapp-web`과 새 KeyDirectory를 그대로 덮어씌우면 기존 주소가 바뀔 수 있다. 기존 폴더를 계속 쓰거나, 정지 상태에서 실제 키를 권한과 함께 이전한다. 키 내용은 로그에 출력하지 않는다.

중계 서버도 같은 데이터 폴더를 계속 사용해야 한다. `web-addresses.json`과 서버 호스트 키를 포함한 전체 서버 데이터를 보존한다. 서버 재시작·클라이언트 재접속은 주소를 유지하지만, 키/예약 데이터 삭제나 관리자 명시 재발급까지 복구해 주지는 않는다. 웹 기본 도메인을 바꾸면 URL의 도메인 부분도 바뀐다.

## 4. 가장 빠른 연결 확인 — SDK의 기존 예제

빌드 PC에 Go 1.26 이상과 Git이 필요하다. 컴파일된 연결 도우미를 배포받는 실행 PC에는 Go·Git이 필요하지 않다.

아래는 비어 있는 작업 폴더에서 시작하는 예다. 원본 앱은 먼저 `127.0.0.1:9000`에서 HTTP로 실행되어 있어야 한다.

```powershell
git clone --branch v1.5.0 --depth 1 https://github.com/OnTechnologyCo/SSHTunnel.git sshtunnel-sdk
Set-Location .\sshtunnel-sdk

# 실제 값은 운영 설정에서 주입한다. 인증키를 저장소나 명령행 인자로 넣지 않는다.
$env:TUNNEL_ACCESS_KEY = '<발급받은 64자리 인증키>'

go run ./examples/publish -protocol http -server 'tunnel.ontechdev.com:39333' -user '<접속계정>' -fingerprint 'SHA256:<실제호스트지문>' -target '127.0.0.1:9000' -identity 'myapp-web' -name 'MyApp'
```

출력된 `브라우저 접속 URL: https://...`로 접속한다. 이 예제는 SDK 기본 사용자 키 폴더를 쓰며 **자동 재접속은 구현되어 있지 않다.** 터널이 끊기면 종료하므로 제품에 넣을 때는 다음 절의 연결 도우미 또는 동등한 재접속 처리를 사용한다.

이 빠른 확인 예제와 다음 도우미가 **서로 다른 키 폴더**를 사용하면 주소도 달라질 수 있다. 동일 주소로 계속 쓸 목적이면 처음부터 다음 도우미를 사용하거나 기존 키 폴더를 지정한다.

Git 없이 SDK를 받는 경우: [SSHTunnel-GoSDK-1.5.0.zip](https://github.com/OnTechnologyCo/SSHTunnel/releases/download/v1.5.0/SSHTunnel-GoSDK-1.5.0.zip). 압축 해제 후 `go.mod`가 있는 SDK 루트를 모듈 경로로 사용한다. `pkg/tunnel`만 복사하면 안 되며 `internal/securefile` 등 SDK 전체 구성이 필요하다.

## 5. 바로 빌드 가능한 연결 도우미

다음 코드를 `main.go`로 저장한다. 이 문서의 Go 코드 블록은 이 블록 하나뿐이다.

이 코드는 로컬 HTTP 서버를 대신 만들지 않는다. 이미 실행 중인 앱을 공개하며, 연결 성공 시 stdout에 JSON 한 줄을 출력한다. 네트워크 단절 시 같은 키로 재접속하고, Ctrl+C 또는 stdin의 `stop` 한 줄로 종료한다. 다른 언어의 앱에서 자식 프로세스로 실행하기에도 사용할 수 있다.

```go
package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"ontech/sshtunnel/pkg/tunnel"
)

func required(name string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return v, nil
}

func options() (tunnel.Options, error) {
	o := tunnel.Options{Protocol: "http", MaxConnections: 64}
	fields := []struct {
		name string
		dst  *string
	}{
		{"TUNNEL_SERVER", &o.Server},
		{"TUNNEL_USER", &o.User},
		{"TUNNEL_ACCESS_KEY", &o.AccessKey},
		{"TUNNEL_FINGERPRINT", &o.Fingerprint},
		{"TUNNEL_TARGET", &o.Target},
		{"TUNNEL_IDENTITY", &o.Identity},
		{"TUNNEL_KEY_DIR", &o.KeyDirectory},
	}
	for _, f := range fields {
		v, err := required(f.name)
		if err != nil {
			return o, err
		}
		*f.dst = v
	}
	for _, address := range []string{o.Server, o.Target} {
		host, port, err := net.SplitHostPort(address)
		if err != nil || host == "" || port == "" || port == "0" {
			return o, errors.New("Server/Target must be host:port without a URL scheme")
		}
	}
	b, err := hex.DecodeString(o.AccessKey)
	if err != nil || len(b) != 32 || len(o.AccessKey) != 64 ||
		strings.ToLower(o.AccessKey) != o.AccessKey {
		return o, errors.New("AccessKey must be 64 lowercase hex characters")
	}
	if !strings.HasPrefix(o.Fingerprint, "SHA256:") {
		return o, errors.New("Fingerprint must be the verified SSH SHA256 fingerprint")
	}
	if !filepath.IsAbs(o.KeyDirectory) {
		return o, errors.New("KeyDirectory must be an absolute persistent app-only path")
	}
	o.Name = strings.TrimSpace(os.Getenv("TUNNEL_NAME"))
	if o.Name == "" {
		o.Name = o.Identity
	}
	if len(o.Identity) > 256 || strings.IndexFunc(o.Identity, unicode.IsControl) >= 0 ||
		len(o.Name) > 160 || strings.IndexFunc(o.Name, unicode.IsControl) >= 0 {
		return o, errors.New("Identity/Name length or control-character validation failed")
	}
	return o, nil
}

func run() error {
	opts, err := options()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// 부모 앱이 stdin에 stop 한 줄을 보내면 정상 종료한다.
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == "stop" {
				cancel()
				return
			}
		}
		// 콘솔 입력이 없는 서비스에서도 동작하도록 EOF 자체로 종료하지 않는다.
	}()

	out := json.NewEncoder(os.Stdout)
	emit := func(v map[string]any) error {
		v["time"] = time.Now().UTC().Format(time.RFC3339)
		return out.Encode(v)
	}
	delay := 2 * time.Second
	const maxDelay = 30 * time.Second

	for ctx.Err() == nil {
		started := time.Now()
		session, openErr := tunnel.Open(ctx, opts)
		if openErr == nil {
			ep := session.Endpoint()
			// 외부 HTTPS 운영을 전제로 한다. 서버가 HTTP 주소를 주면 설정을 고친다.
			if !strings.HasPrefix(ep.URL, "https://") {
				_ = session.Close()
				return errors.New("relay returned a non-HTTPS URL; check relay web configuration")
			}
			if err := emit(map[string]any{
				"event": "connected", "url": ep.URL, "id": ep.ID,
				"name": ep.Name, "protocol": ep.Protocol,
			}); err != nil {
				_ = session.Close()
				return err
			}
			waitErr := session.Wait()
			streamErr := session.LastStreamError()
			_ = session.Close()
			if ctx.Err() != nil {
				break
			}
			if err := emit(map[string]any{"event": "disconnected"}); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "tunnel disconnected: %v\n", waitErr)
			if streamErr != nil {
				fmt.Fprintf(os.Stderr, "last local stream error: %v\n", streamErr)
			}
			if time.Since(started) >= 30*time.Second {
				delay = 2 * time.Second
			}
		} else {
			if ctx.Err() != nil {
				break
			}
			// 인증·저장 오류에서도 키를 바꾸지 않고 같은 설정으로 기다려 재시도한다.
			fmt.Fprintf(os.Stderr, "tunnel open failed: %v\n", openErr)
		}

		pause := delay + time.Duration(rand.Int64N(int64(time.Second)))
		if err := emit(map[string]any{
			"event": "retrying", "retryAfterMs": pause.Milliseconds(),
		}); err != nil {
			return err
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
	return emit(map[string]any{"event": "stopped"})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

주석의 재시도 정책: 인증·권한·키 저장 오류도 새 키를 만들지 않고 재시도한다. 같은 오류가 계속되면 운영자가 설정을 수정하고 도우미를 다시 실행한다. 재시도 대기는 2→4→8→16→30초에 0~1초 미만의 지연을 더한다. 호스트 검증 실패를 무시해서 접속하는 동작은 없다.

### 5.1 새 프로젝트에서 빌드

다음 명령은 **비어 있는 새 연결 도우미 폴더**에서 실행한다. 기존 제품의 go.mod를 새로 만들지 않는다. 먼저 위 코드를 main.go로 저장한다.

```powershell
go mod init example.com/tunnelagent
New-Item -ItemType Directory -Path .\third_party -Force | Out-Null
git clone --branch v1.5.0 --depth 1 https://github.com/OnTechnologyCo/SSHTunnel.git .\third_party\SSHTunnel
go mod edit '-require=ontech/sshtunnel@v0.0.0'
go mod edit '-replace=ontech/sshtunnel=./third_party/SSHTunnel'
go mod tidy
go build -trimpath -o tunnel-agent.exe .
```

모듈의 실제 선언은 `ontech/sshtunnel`이다. 저장소 URL을 보고 import를 `github.com/OnTechnologyCo/SSHTunnel/pkg/tunnel`로 임의 변경하지 않는다. SDK 루트 전체를 상대 경로로 참조하므로 다른 PC로 프로젝트를 옮겨도 동일하게 빌드할 수 있다.

Go가 아닌 C#/Python/Node.js 앱에는 컴파일된 `tunnel-agent.exe`만 함께 배포할 수 있다. 실행 PC의 OS·CPU에 맞게 빌드한다. Linux/macOS에서는 실행 파일 이름과 영구 데이터 경로를 해당 OS에 맞춘다.

### 5.2 실행 설정

다음 예제는 **새 앱의 사용자 계정 실행**을 위한 것이다. 기존 앱은 기존 Identity·키 경로를 먼저 확인한다.

```powershell
$env:TUNNEL_SERVER = 'tunnel.ontechdev.com:39333'
$env:TUNNEL_USER = '<운영자가 발급한 접속계정>'
$env:TUNNEL_ACCESS_KEY = '<발급받은 64자리 인증키>'
$env:TUNNEL_FINGERPRINT = 'SHA256:<관리 화면에서 확인한 실제 지문>'
$env:TUNNEL_TARGET = '127.0.0.1:9000'
$env:TUNNEL_IDENTITY = 'myapp-web'
$env:TUNNEL_NAME = 'MyApp'
$env:TUNNEL_KEY_DIR = Join-Path $env:LOCALAPPDATA 'MyApp\TunnelIdentity'

# 원본 앱의 HTTP 서비스가 이미 실행 중이어야 한다.
.\tunnel-agent.exe
```

`<...>`는 실행 가능한 인증값이 아니다. 배포 앱에서는 안전하게 보관한 설정에서 **자식 프로세스 환경변수로만** 주입하는 방식을 우선한다. 인증키를 프로세스 명령행·일반 로그·Git·설치 패키지에 넣지 않는다. 이 도우미는 별도 JSON 설정 파일을 자동으로 읽지 않는다.

Windows 서비스에서는 사용자별 LOCALAPPDATA 예제를 그대로 쓰지 말고 서비스 전용의 영구 데이터 위치, 예를 들어 `C:\ProgramData\MyApp\TunnelIdentity`를 사용한다. 설치 단계에서 실제 서비스 계정이 사용할 수 있는 전용 폴더를 준비한다. 기존 키가 있으면 경로만 바꾸지 말고 이전 키를 보존한다.

성공 시 stdout 예시:

```json
{"event":"connected","url":"https://a7k2m9.tunnel.example.com","id":"a7k2m9","name":"MyApp","protocol":"http","time":"2026-10-23T08:00:00Z"}
```

이 시점은 **터널이 개설되었다는 의미**다. DNS·공인 인증서·로컬 앱 응답까지 확인되었다는 뜻은 아니므로 8절의 공개 HTTP 확인을 완료해야 한다.

### 5.3 다른 언어의 부모 프로그램에서 사용

1. 로컬 HTTP 서버를 시작하고 준비 완료를 확인한다.
2. 앱 하나당 연결 도우미 하나만 시작한다. 실행 파일은 앱이 설치한 절대경로로 지정하고 셸 문자열 조합을 피한다.
3. 필요한 환경변수를 자식에게 넘긴다. Windows GUI 앱에서는 창을 숨기고 stdin/stdout/stderr를 각각 리다이렉트한다.
4. stdout을 **한 줄씩 JSON으로 파싱**한다. 일반 로그와 섞지 않는다. stderr도 비동기로 계속 소비하여 파이프가 가득 차는 것을 막는다.
5. `connected` 이벤트의 `url`을 앱의 외부 접속 주소로 저장·표시한다. 먼저 외부 건강 상태 확인을 한 뒤 사용자에게 연결 완료를 표시한다.
6. `disconnected`/`retrying`이면 오프라인 또는 재접속 중으로 표시한다. 마지막 URL은 유지하되 활성 연결로 표시하지 않는다.
7. 연결 도우미 내부에서 재접속하므로 이벤트마다 도우미를 추가 실행하지 않는다. 도우미 자체가 종료되었을 때만 부모가 지연 후 재실행한다.
8. 정상 종료 시 stdin에 `stop\n`을 쓰고 flush한 뒤 종료를 기다린다. 응답하지 않으면 앱이 시작한 해당 자식 PID만 종료한다.
9. 부모 앱 비정상 종료 시 도우미가 남지 않도록 프로세스 소유권/Windows Job Object 등 제품의 기존 수명 관리에 연결한다. stdin EOF만으로는 이 예제 도우미가 종료되지 않는다.

`connected` 때마다 “주소가 변경됨” 알림을 보내지 않는다. 이전에 저장한 URL과 실제로 다를 때만 변경 알림을 보낸다. 최초 발급과 동일 주소 재연결은 구분한다.

Go 앱에 직접 넣을 때는 `tunnel.Open(ctx, opts)`와 재접속 루프를 앱 수명에 연결한다. `session.Wait()`/`Done()`은 연결 종료를 감시하며 SDK 자체가 새 SSH 연결을 자동으로 만들지는 않는다. `Close()` 또는 ctx 취소로 종료한다.

## 6. 웹/API/MCP가 실제로 동작하도록 연결

발급 URL을 단순히 화면에 표시하는 것으로 구현을 끝내지 않는다.

| 원본 기능 | 실제 외부 주소 예시 |
|---|---|
| 웹 페이지 `/` | `발급URL + "/"` |
| HTTP API `/api/status` | `발급URL + "/api/status"` |
| MCP `/mcp` | `발급URL + "/mcp"` |
| WebSocket `/ws` | `wss://발급호스트/ws` |
| SSE `/events` | `발급URL + "/events"` |

위 경로는 **원본 앱이 실제 제공할 때만** 사용할 수 있다. 터널은 /mcp, /health, /register 등의 기능을 새로 만들어 주지 않는다.

- HTTP 메서드·경로·쿼리·본문·Authorization·쿠키·WebSocket·SSE를 전달한다.
- HTTP 모드 Target은 평문 HTTP 리스너다. 로컬 HTTPS 전용 포트를 직접 넣으면 안 된다. 로컬 HTTP 리스너 또는 제품이 관리하는 내부 HTTP 프록시를 준비한다.
- 외부 HTTPS는 중계 서버에서 처리하고, 중계 서버↔연결 모듈은 SSH로 암호화한다.
- 원본 앱은 가능하면 루프백에 수신한다. 프로그램 PC에서 외부 인바운드 포트를 새로 열 필요는 없다.
- Host 허용 목록이 있으면 반환된 실제 호스트를 허용한다. 터널은 공개 Host를 원본에 전달한다.
- `X-Forwarded-Proto/Host/Port`를 처리하도록 구성하고, 프록시 신뢰 범위를 실제 터널 경로로 제한한다.
- 앱의 외부 URL, OAuth issuer·redirect URL, 콜백 주소, 생성 링크가 localhost를 가리키면 발급 URL로 맞춘다. 해당 앱의 설정 방식에 따라 반영하되 Identity와 키는 바꾸지 않는다.
- 페이지 자원은 가능하면 `/assets/...` 같은 상대경로를 사용한다. HTML/JS에 들어 있는 localhost 주소를 터널이 자동 치환하지 않는다.
- MCP 인증과 로그인 세션은 원본 MCP 앱이 관리한다. 터널 AccessKey를 MCP 사용자 인증 토큰으로 전달하지 않는다.
- 응답 쿠키 Domain 속성은 터널에서 제거되어 해당 호스트 전용으로 바뀐다. Secure·HttpOnly·SameSite·Path는 유지된다. 부모 도메인 쿠키를 공유하는 SSO는 별도 검토가 필요하다.
- 공개 URL은 비밀번호가 아니다. 외부에 노출할 원본 앱의 인증·인가를 유지한다.

## 7. 중계 서버 쪽 준비 사항 — 이미 운영 중이면 확인만

프로그램을 붙이는 LLM이 중계 서버를 초기화하거나 계정 키를 교체할 필요는 없다. 아래 설정이 준비되지 않았을 때만 **실제 중계 서버 PC**에서 운영자가 설정한다.

1. 서버 v1.5.0 이상에서 자동 터널과 웹 터널을 활성화한다.
2. 접속 계정에 자동 터널 사용 권한을 부여하고 AccessKey를 발급한다. 발급 시 한 번 표시되므로 안전하게 보관한다.
3. 웹 기본 도메인을 `tunnel.example.com`처럼 설정한다. `https://`, `*.`, 포트를 붙이지 않는다.
4. 와일드카드 DNS `*.tunnel.example.com`이 실제 중계 서버/프록시를 가리키도록 구성한다.
5. 내장 HTTPS를 쓰면 수신 포트 443과 해당 와일드카드의 유효한 공인 인증서를 준비한다. 기존 앞단 프록시가 있으면 공개 Host를 보존하여 내부 게이트웨이로 전달한다.
6. 중계 서버의 SSH 수신 포트와 외부 HTTPS 443이 방화벽·공유기에서 도달 가능해야 한다.
7. 관리 화면·내부 HTTP 게이트웨이는 외부에 공개하지 않는다. 기본 관리 포트는 17893, 내부 게이트웨이는 17894이며 브라우저 사용자에게 주는 주소가 아니다.
8. 내장 HTTPS의 DNS 인증 발급 기능은 서버 관리 화면에서 Cloudflare Zone Read/DNS Edit 권한과 도메인을 지정한다. 기존 DNS나 다른 서비스의 443 포트를 임의 변경하지 않는다.
9. 서버 재시작/업데이트 후에도 같은 데이터 폴더와 호스트 키를 사용한다. `web-addresses.json`만이 아니라 서버 데이터 전체를 백업한다.

서버 설치 파일: [SSHTunnelServer-Setup-1.5.0.exe](https://github.com/OnTechnologyCo/SSHTunnel/releases/download/v1.5.0/SSHTunnelServer-Setup-1.5.0.exe). **이 파일은 중계 서버용이며 원본 앱에 붙이는 연결 도우미가 아니다.**

기본 데이터 위치는 실행 방식에 따라 다르다. 설치판 트레이는 `%LOCALAPPDATA%\SSHTunnel\data`, 서비스 설치 기본값은 `%ProgramData%\SSHTunnel`, 소스/포터블 시작 스크립트는 배포 폴더의 `data`를 사용할 수 있다. 재시작 방식을 바꾸면서 데이터 폴더까지 바뀌지 않도록 실제 `--data-dir`를 확인한다.

## 8. 최소 검증과 완료 조건

핵심 검증만 수행한다. 운영 서버 재시작으로 다른 사용자의 연결을 끊지 말고, 서버 재시작 검증은 별도 시험 서버나 승인된 운영 점검 시간에 수행한다.

1. **로컬 응답:** 원본 앱에서 실제 제공하는 읽기 전용 경로로 로컬 HTTP 응답을 확인한다.
2. **터널 개설:** `connected` 이벤트에서 실제 발급 URL을 얻는다.
3. **외부 응답:** 가능하면 다른 네트워크의 PC/휴대폰에서 같은 경로를 호출하여 기대한 응답 내용 또는 원본 앱의 정상 로그인 화면을 확인한다. 포트가 열려 있다는 검사만으로 완료 처리하지 않는다.
4. **주소 유지:** 연결 도우미 정상 종료→재실행 후 URL이 이전 값과 같아야 한다. 기존 연결이 끝난 다음 재실행한다.
5. **사용 기능:** 앱이 실제 사용하는 API·WebSocket·SSE·MCP 기능만 왕복 확인한다. 모든 기능을 무조건 시험하지 않는다.
6. 임시 로컬 포트를 쓰는 앱이면 Target 포트를 바꿔도 고정 Identity와 같은 키 폴더로 URL이 유지되는지 시험 환경에서 확인한다.

예시 PowerShell:

```powershell
# /health는 원본 앱이 제공하는 실제 읽기 전용 경로로 바꾼다.
Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:9000/health'

# 연결 도우미에서 반환한 실제 URL로 바꾼다.
$publicUrl = 'https://<발급된 실제 호스트>'
Invoke-WebRequest -UseBasicParsing -Uri ($publicUrl + '/health')
```

일부 인증 앱은 401/로그인 리다이렉트가 정상일 수 있다. 해당 원본 앱의 기대 결과를 기준으로 판단한다. TLS 검증 비활성화 옵션으로 성공을 만들어서는 안 된다.

LLM의 완료 보고에는 다음을 포함한다.

- 사용한 원본 Target과 고정 Identity
- 실행 계정·식별키 폴더 경로 **(키 원문 제외)**
- 실제 발급 URL과 연결 상태
- 로컬·공개 응답 검증 결과
- 재접속 전후 URL 동일 여부
- 실행/중지 방법과 설정 위치
- 외부 접속 또는 서버 재시작 등 수행하지 못한 검증

## 9. 증상별 조치

| 증상 | 우선 확인 | 하면 안 되는 조치 |
|---|---|---|
| SSH 연결 timeout/refused | SSH 호스트·포트, 수신 상태, 방화벽·NAT·아웃바운드 경로 | 브라우저 443을 SSH Server 포트로 무작정 대체 |
| 인증 실패 | User/AccessKey, 계정 상태, 중계 서버가 맞는지 | 기존 계정 키를 임의 재발급 |
| `relay host fingerprint mismatch` | 운영자가 제공한 SSH 지문과 서버 호스트 키 변경 이력 | 호스트 검증 제거 또는 접속한 서버 지문을 무조건 신뢰 |
| 자동 터널 비활성/권한 없음 | 자동 터널 설정·계정 권한·전체 접속 중지 상태 | 불필요한 일반 SSH 포워딩 권한까지 전부 허용 |
| 내장 HTTPS 준비 안 됨 | 서버 인증서·443 수신·웹 설정 | 인증서 검증 우회 |
| `이미 사용 중인 터널 키` | 이전 도우미 프로세스/연결이 남았는지, 키를 다른 PC에 복사했는지 | 새 TunnelKey를 만들어 우회 |
| 키 파일 손상/잠금/접근 거부 | 실행 계정·전용 폴더 ACL·파일 상태·동시 실행 | 키 파일 삭제 후 새 발급 |
| URL은 받았지만 DNS/TLS 오류 | 와일드카드 DNS·유효한 인증서·실제 발급 호스트 | 기본 도메인/IP만 접속하고 터널 실패로 단정 |
| 404 | 터널 온라인 여부, 공개 Host, 실제 앱 경로; 원본 앱도 404를 낼 수 있음 | 예약 주소 목록을 임의 삭제 |
| 502 또는 로컬 대상 접속 실패 | Target의 HTTP 리스너·포트·원본 프로세스, LastStreamError | Target에 중계 서버 주소를 입력 |
| 로그인 후 localhost 이동 | 원본 앱의 public/base URL·프록시 헤더·OAuth 설정 | 터널 URL 문자열만 UI에서 교체 |
| 재접속마다 새 주소 | Identity·Ephemeral·TunnelKey 저장·실행 계정·KeyDirectory·서버 데이터 폴더 | 표시 이름만 같게 맞추면 고정된다고 가정 |

중계 서버 `events.jsonl`에는 연결·터널 이벤트가 있지만 클라이언트 버전·실제 키 보관 위치까지 나오지는 않는다. 중복 이름이 보인다는 이유만으로 PC들을 병합하지 않는다. 이름이 같은 서로 다른 장비가 있을 수 있다.

## 10. HTTP와 TCP의 차이

| 항목 | HTTP 웹 터널 | TCP 터널 |
|---|---|---|
| Protocol | `http` | `tcp` 또는 생략 |
| 공개 주소 | `Endpoint().URL` | `Endpoint().Address` |
| 외부 형태 | `https://6자리.기본도메인` | `공개호스트:배정포트` |
| 원본 | 로컬 HTTP 서버 | 일반 TCP 리스너 |
| 재접속 후 주소 유지 | 같은 계정·식별키·서버 예약이면 유지 | 같은 키여도 배정 포트 유지 보장 없음 |
| 외부 수신 | 웹 공통 443 | 서버 설정의 배정 포트 범위 |
| 외부 TLS | 웹 게이트웨이/프록시에서 처리 | 원본 프로토콜에서 필요 시 제공 |

웹 고정 주소가 목표인 이 문서의 도우미는 HTTP 모드 전용이다. RDP·DB·자체 바이너리 TCP를 공개하려면 TCP 모드로 별도 연동하고 `Address`를 사용한다. 이 경우 일반 브라우저용 고정 HTTPS URL과 동일한 동작을 기대하지 않는다. UDP·SSH 셸·SFTP·L3 VPN은 이 자동 터널 기능의 대상이 아니다.

## 11. SDK 없이 다른 언어로 직접 구현해야 할 때

가능하면 검증된 Go SDK 또는 위 도우미를 사용한다. 자체 SSH 구현이 필요한 경우에만 다음 wire protocol을 구현한다.

1. 중계 서버에 SSH 연결하고 **신뢰된 Fingerprint와 호스트 키를 비교**한다.
2. SSH 사용자 이름은 User, SSH password 인증 데이터는 AccessKey다.
3. 연결 이후 전역 요청 `tunnel-open@ontech.dev`를 `want-reply=true`로 보낸다. payload는 아래 JSON의 UTF-8 바이트다. SSH 문자열로 이중 포장하거나 HTTP POST로 보내지 않는다.
4. 성공 응답의 URL을 사용하고 SSH 연결을 계속 유지한다.
5. 서버가 여는 `tunnel-connection@ontech.dev` 채널을 받아 원본 TCP 서비스와 양방향 전달한다.
6. 종료/keepalive를 처리하고 단절 시 같은 TunnelKey로 새 SSH 연결을 만든다.

웹 터널 요청 예시:

```json
{"protocol":"http","key":"<영구 저장한 64자리 소문자 hex TunnelKey>","name":"MyApp"}
```

웹 성공 응답 예시:

```json
{"id":"a7k2m9","name":"MyApp","address":"a7k2m9.tunnel.example.com","host":"a7k2m9.tunnel.example.com","port":0,"protocol":"http","url":"https://a7k2m9.tunnel.example.com"}
```

실패는 SSH 요청 실패 응답과 `{"error":"사유"}` payload다.

| SSH 메시지 | 방향·의미 |
|---|---|
| `tunnel-open@ontech.dev` | 클라이언트→서버, 위 JSON으로 개설 |
| `tunnel-connection@ontech.dev` | 서버→클라이언트 새 SSH 채널. ExtraData는 `{"tunnelId":"...","origin":"IP:포트"}` |
| `tunnel-close@ontech.dev` | 클라이언트→서버 전역 요청. `{"id":"..."}`, 해당 SSH 연결 소유 터널만 종료 |
| `tunnel-closed@ontech.dev` | 서버→클라이언트 종료 알림. `{"id":"..."}`, want-reply=false |
| `keepalive@openssh.com` | 양쪽의 생존 확인 요청. 응답 요청이 있으면 응답 |

추가 구현 조건:

- 새 데이터 채널의 tunnelId를 자신이 소유한 터널과 대조하고 로컬 대상 연결에 성공한 뒤 채널을 수락한다.
- 양방향 바이트를 전달하며 한 방향 EOF는 half-close로 전달한다. 한쪽 읽기가 끝났다고 반대 방향 응답까지 바로 끊지 않는다.
- 개설 요청은 최대 4096바이트, Name은 UTF-8 기준 최대 160바이트·제어문자 불가다.
- HTTP 응답은 protocol=http, port=0, address=host, URL의 host가 동일한지 확인한다. 운영 환경에서는 HTTPS만 허용한다.
- SDK의 기본 keepalive는 20초 간격, 응답 대기 10초다. 자체 구현도 연결 손실을 감지하고 백오프로 재접속해야 한다.
- TunnelKey는 처음 한 번 안전한 난수로 생성하여 영구 저장한다. PC 이름/IP/접속 비밀번호를 TunnelKey로 쓰지 않는다.
- SSH 연결을 닫으면 활성 터널도 닫히지만 웹 예약은 남는다. URL만 받아 둔 뒤 SSH 프로세스를 끝내면 서비스를 이용할 수 없다.

## 12. 이 문서를 전달받은 LLM의 실행 지시

> 이 문서를 기준으로 대상 프로그램에 SSHTunnel 웹 터널 연결 기능을 구현하라.
> 먼저 기존 서비스의 로컬 HTTP 주소, 접속 설정, 실행 계정, 이전 Identity와 식별키 저장 위치를 확인하라.
> 중계 서버가 이미 운영 중이면 초기화하거나 재설치하지 말고 그 서버에 연결하라.
> Go 프로그램은 SDK를 직접 사용하고, 다른 언어는 이 문서의 연결 도우미를 빌드하여 자식 프로세스로 사용할 수 있다.
> 고정 Identity와 영구 KeyDirectory를 사용하고, 이미 발급받은 키는 보존하라.
> 실제 SDK가 반환한 URL을 앱의 공개 주소·API·MCP 경로·필요한 콜백 설정에 반영하라.
> 연결 실패와 주소 발급 성공, 실제 외부 접속 성공을 구분하여 상태를 표시하라.
> 같은 URL로 자동 재접속하고, 정상 종료와 자식 프로세스 수명까지 구현하라.
> 로컬 응답·공개 응답·재접속 후 URL 유지의 핵심 검증을 수행하라.
> 접속 인증 정보가 없다면 Server/User/AccessKey/Fingerprint 중 누락된 값만 운영자에게 요청하라.
> 실제로 연결하지 못했다면 발급 URL이나 성공 결과를 만들어 보고하지 말라.

## 13. 기준 자료와 검증 범위

- 저장소: https://github.com/OnTechnologyCo/SSHTunnel
- 고정 버전: https://github.com/OnTechnologyCo/SSHTunnel/releases/tag/v1.5.0
- SDK API·연결 수명: `pkg/tunnel/tunnel.go`
- 식별키 저장: `pkg/tunnel/identity.go`, `internal/securefile`
- 주소 예약: `internal/server/web_addresses.go`
- 개설 프로토콜: `internal/server/broker.go`
- HTTP 전달: `internal/server/web_tunnel.go`
- 기존 CLI 예제: `examples/publish/main.go`

기존 문서에는 초기 버전의 “재접속 시 새 주소” 설명이 일부 남아 있다. 이 문서의 HTTP 주소 유지 설명은 v1.5.0 코드 기준이며 TCP 포트 유지와 구분한다.

검증 완료: 위 Go 도우미를 별도 임시 프로젝트로 추출하여 Windows / Go 1.26.1 / SDK v1.5.0(38b7873)에서 빌드했다. 필수 Server 설정이 없으면 네트워크 접속 전에 오류와 종료 코드 1을 반환하는 것도 확인했다. 이 문서의 예시 계정·키·URL로 실제 운영 접속이 확인된 것은 아니다. 실제 접속 검증은 각 대상 앱의 설정으로 8절에 따라 수행한다.
