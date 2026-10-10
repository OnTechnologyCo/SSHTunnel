# 브라우저 통신 테스트

기존 서버 프로그램을 변경하지 않고 터널을 통한 HTTP 및 메시지 왕복 통신을 확인하는 예제입니다.

1. 터널링 서버 관리 화면에서 자동 터널 기능, 공개 주소, 포트 범위를 설정합니다.
2. 테스트 계정을 만들고 API 인증키와 서버 호스트 지문을 확인합니다.
3. 이 폴더의 `demo.example.json`을 저장소 루트의 `data/demo/demo.json`으로 복사하고 접속 정보를 입력합니다. `data` 폴더의 인증 정보는 Git에 포함되지 않습니다. 해당 폴더는 본인만 읽을 수 있도록 권한을 설정하세요.
4. 저장소 루트에서 다음 명령으로 실행합니다.

```powershell
go run ./examples/web-demo --config ./data/demo/demo.json
```

Windows 실행 파일로 빌드할 수도 있습니다.

```powershell
go build -trimpath -ldflags "-s -w -H=windowsgui" -o ./output/SSHTunnelDemo.exe ./examples/web-demo
.\output\SSHTunnelDemo.exe --config .\data\demo\demo.json
```

- 실제 웹 서버는 `127.0.0.1`의 빈 포트에만 바인딩합니다.
- 터널 주소와 테스트 프로세스 정보는 설정 파일 옆의 `endpoint.json`에 기록됩니다.
- 로그는 같은 폴더의 `demo.log`에서 확인합니다.
- 외부 접속 시에는 발급된 터널 포트가 중계 서버의 방화벽 및 NAT에서 허용되어야 합니다. 다른 서버의 포트나 규칙을 변경하지 마세요.
- 페이지에서 연결 상태, 응답 시간, 메시지 왕복 결과를 확인할 수 있습니다.
- 자동 재접속은 하지 않습니다. 연결이 끊기면 종료되며 재실행 시 다른 주소가 발급될 수 있습니다.
- 이 예제는 HTTP 테스트용입니다. 비밀번호나 개인정보를 입력하지 마세요.
- 종료할 때는 실행한 테스트 프로세스를 종료하거나 관리 화면에서 해당 터널만 닫습니다. 직접 추가한 테스트 방화벽 규칙도 정리하세요.

## English

This example starts a loopback-only HTTP server, opens a tunnel using the Go SDK, and shows the assigned public address in `endpoint.json`. Open that HTTP address in a browser to check connectivity and echo a short message. Keep the access key in a private local configuration file. Do not send sensitive data over this HTTP demonstration page.