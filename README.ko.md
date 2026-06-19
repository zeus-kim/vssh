# vssh

[![CI](https://github.com/zeus-kim/vssh/actions/workflows/ci.yml/badge.svg)](https://github.com/zeus-kim/vssh/actions/workflows/ci.yml) [![PyPI](https://img.shields.io/pypi/v/vssh.svg)](https://pypi.org/project/vssh/) [![Python](https://img.shields.io/pypi/pyversions/vssh.svg)](https://pypi.org/project/vssh/) [![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE) [![Release](https://img.shields.io/github/v/release/zeus-kim/vssh)](https://github.com/zeus-kim/vssh/releases/latest) [![CodeQL](https://github.com/zeus-kim/vssh/actions/workflows/codeql.yml/badge.svg)](https://github.com/zeus-kim/vssh/actions/workflows/codeql.yml) [![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/zeus-kim/vssh/badge)](https://securityscorecards.dev/viewer/?uri=github.com/zeus-kim/vssh)

**AI 시대의 원격 실행 — 구조화·범위제한·감사. `sshd` 없이.**

AI 에이전트가 여러 서버에서 명령을 돌려야 할 때, SSH는 **풀 인터랙티브 셸**을
줍니다 — 로그인 사용자가 할 수 있는 모든 것을, 가공 안 된 텍스트로, 의도 기록도
없이. `vssh`는 대신 **범위가 정해진 계약**을 줍니다:

- **범위 제한.** 키마다 정확한 **capability**(exec / file / rpc / forward)와
  선택적 **policy**(명령 allowlist, 경로 범위, rate)로 제한 — deny 우선,
  fail-closed, **데몬이 강제**. safe-exec 경로는 `curl … | bash`·자격증명 파일
  읽기를 승인 대상으로 표시.
- **구조화.** 모든 결과는 타입드 **증거**(stdout/stderr/exit/duration/transport)
  — 파싱할 텍스트가 아님.
- **감사.** 모든 행위가 키 귀속 **해시체인**으로 기록.
- **셸이 아니라 키.** **TLS 1.3 + 노드별 Ed25519**, 공유 시크릿 없음, **타깃에
  `sshd` 없음**.

사람에게도 그대로 좋고(`vssh run`, `shell`, 파일 전송, 터널, job), **바이너리에
MCP 서버 내장**이라 Claude·Cursor·Codex·Gemini를 한 줄로 부착합니다.

```bash
# 설치 (Linux x86-64/arm64/arm/386/riscv64/ppc64le/s390x, macOS; FreeBSD 실험적)
curl -fsSL https://raw.githubusercontent.com/zeus-kim/vssh/main/install.sh | bash
# 또는
pip install vssh
```

---

## 왜 vssh인가

`vssh`는 "명령어가 짧아진 SSH"가 아니라, **조작 주체가 사람이 아니라 AI
에이전트·자동화 런타임**인 상황을 위한 다른 추상화입니다. 간단 비교:

| | OpenSSH | vssh |
|---|---|---|
| 타깃 전제조건 | `sshd` + 사용자 + 호스트키 + PAM | 단일 정적 바이너리, **sshd 불필요** |
| 인증 | PAM 기반 비밀번호/키 | 노드별 **Ed25519 VAUTH1**; 공유 시크릿 없음 |
| 전송 | SSH 프로토콜 | **TLS 1.3 + Ed25519 raw-key 핀** |
| 호스트 신원 | `known_hosts` TOFU | 핀 레지스트리, **기본 ON**, 오(誤)라우팅 차단 |
| 명령 결과 | 원시 텍스트 스트림 | **타입 증거**(stdout/stderr/exit/duration/transport) |
| 인가 | 셸 = 전체 접근 | 키별 **capability** + 명령 **정책** |
| 감사 | 내장 없음 | 액션별 **해시체인** 기록 |
| 장기 작업 | tmux / nohup | `job_start/status/logs/cancel` + 아티팩트 |
| 멀티노드 | 스크립팅 | 네이티브 fan-out + capability/health **라우팅** |
| AI / 자동화 | 텍스트 파싱 | **MCP 네이티브** 타입 도구 |
| 플릿 상태 | 없음 | **서명된** 플릿 스냅샷, 노드 복제 가능 |
| 온보딩 | 수동 | 제로터치 자동 셋업; 한 명령 MCP 부착 |

vssh는 OpenSSH를 감싸지 않습니다: vssh가 안 깔린 박스에 한 번 들어갈 거면 무설치인
그냥 `ssh`가 편합니다. 하지만 vssh가 도는 플릿에선 CLI가 대화형 셸(`vssh shell`)·
파일 전송·터널까지 다 커버 — `ssh` 필요 없습니다. 전체 근거:
[docs/WHY_VSSH.ko.md](docs/WHY_VSSH.ko.md).

**범위 밖:** VPN 메시 운영과 모니터링 대시보드 — 직접 준비하세요(예: 네트워크
계층은 Tailscale/WireGuard, 모니터링은 본인 스택).

---

## AI 클라이언트에 붙이기 (한 명령)

`vssh`는 바이너리에 내장된 MCP 서버입니다. JSON을 손으로 편집하지 않고 한 번에
AI 클라이언트에 붙입니다:

```bash
vssh mcp-install --client claude     # 또는: claude-code, cursor, gemini, codex
# 쓰기 대신 미리보기/출력:
vssh mcp-config  --client claude
```

클라이언트 설정에 `vssh` MCP 서버 항목(절대 바이너리 경로)을 기존 서버를 보존하며
병합합니다. 클라이언트를 재시작하면 AI가 감사 흔적과 함께 플릿 작업을 라우팅·게이팅·
실행할 수 있습니다. 최초 사용 시 `vssh`가 호스트 신원검증을 자동 프로비저닝합니다
(제로터치) — 수동 셋업 단계 없음.

---

## 설치

### 원라인 설치 (권장)

```bash
curl -fsSL https://raw.githubusercontent.com/zeus-kim/vssh/main/install.sh | bash
```

설치 스크립트는 OS/아치를 감지해
[최신 GitHub 릴리스](https://github.com/zeus-kim/vssh/releases/latest)에서 맞는
바이너리를 받고, **공개된 `checksums.txt`로 SHA-256을 검증**한 뒤 `~/bin`에
설치합니다. 릴리스는 Linux `amd64/arm64/arm/386/riscv64/ppc64le/s390x`와 macOS
`amd64/arm64`를 포함합니다(FreeBSD는 실험적 빌드).

```bash
curl -fsSL .../install.sh | VSSH_VERSION=0.7.42 bash   # 버전 고정
curl -fsSL .../install.sh | INSTALL_DIR=/usr/local/bin bash
```

### pip (CLI + Python SDK)

```bash
pip install vssh
```

`vssh` CLI(첫 실행 시 플랫폼에 맞는 Go 바이너리를 받아 체크섬 검증, `~/.vssh/bin`에
캐시)와 Python SDK(`from vssh import VSSH`)를 함께 설치합니다.

### 소스에서

```bash
git clone https://github.com/zeus-kim/vssh && cd vssh
make build          # ./vssh 빌드   (Go 1.25+)
make install        # /usr/local/bin 설치 (sudo)
```

---

## 빠른 시작

**타깃** 노드에서 데몬 실행 (키 전용 인증 — 설정할 것 없음):

```bash
vssh server                  # :48291 수신
```

클라이언트를 인가하려면 그 공개키를 서버의 `~/.vssh/authorized_keys`에 추가합니다
(클라이언트에서 `vssh keygen`으로 키 출력). 플릿이면 컨트롤러에서
`scripts/enroll.sh <node>`가 자동 처리합니다. 그다음 클라이언트에서:

```bash
vssh run web1 "df -h"        # 명령 실행, 구조화된 증거 수신
vssh web1                    # 대화형 셸(PTY)
vssh put ./app web1:/tmp/    # 파일 업로드
vssh get web1:/var/log/x .   # 파일 다운로드
vssh fwd web1 -L 8080:localhost:80   # 로컬 포트포워드 (-R, -D SOCKS도)
vssh                         # 플릿 대시보드
vssh doctor --json           # 바이너리/인증모델/설정/피어/MCP 진단
```

`vssh run`은 종료코드·소요시간·전송·시도한 엔드포인트가 담긴 증거 봉투를
돌려줍니다 — 원시 텍스트가 아니라.

---

## 동작 원리

```text
vssh client ──TLS 1.3──▶ vssh server ──▶ 타입드 exec / file / job / tunnel / RPC ──▶ 구조화된 증거
            (Ed25519 핀)     (:48291)
```

1. **전송** — 데몬의 **Ed25519 공개키를 핀**한 TLS 1.3(raw-key, CA 아님).
   TLS 우선이며 `VSSH_REQUIRE_TLS=1`로 평문을 거부합니다.
2. **호스트 신원** — 클라이언트가 신뢰 레지스트리의 데몬 키와 대조해 *의도한*
   호스트에 도달했는지 검증(0.7.33부터 기본 ON) → 이름이 엉뚱한 머신으로 조용히
   라우팅되지 않습니다.
3. **인증** — 노드별 **Ed25519 챌린지–응답(VAUTH1)** 전용. 공유 시크릿 없음;
   클라이언트는 `~/.vssh/authorized_keys`(또는 `/etc/vssh/authorized_keys`)에
   공개키를 올려 인가됩니다. 데몬은 VAUTH1이 아닌 모든 인증 라인을 거부합니다.
4. **정책 + 감사** — 명령은 실행 전 분류·게이팅 가능(키별 allow/deny, 경로 스코프,
   레이트, capability), 모든 액션은 키에 귀속된 해시체인 감사 로그에 기록됩니다.
5. **플릿 상태** — 컨트롤러가 전체 플릿의 **서명·타임스탬프** 스냅샷을 만들고
   내구성을 위해 읽기전용 복제본을 노드에 배포할 수 있습니다.

---

## CLI 레퍼런스

```
실행
  vssh <node>                 대화형 PTY 셸
  vssh run <node> <cmd>       명령 실행 (구조화된 결과)
  vssh run-many <n1,n2> <cmd> 쉼표 구분 노드들에 실행
  vssh run-batch <node> ...   한 세션에서 여러 명령 실행

타입드 API
  vssh rpc <node> <method> [json]   타입드 데몬 RPC 호출 (node_info 포함)
  vssh rpc-many <nodes> <method>    노드들에 RPC
  vssh facts <node>                 타입드 호스트 facts
  vssh facts-many <nodes>           노드들에 facts

잡 (장기 실행)
  vssh job-start <node> <cmd>  / job-status / job-logs / job-cancel
  vssh artifact-collect <node> 출력 아티팩트 수집

파일 & 터널
  vssh put <local> <node:path> 업로드
  vssh get <node:path> <local> 다운로드
  vssh fwd <node> -L [bind:]<lport>:<host>:<port>   로컬 포워드
  vssh fwd <node> -R [bind:]<rport>:<host>:<port>   리버스 포워드
  vssh fwd <node> -D [bind:]<port>                  다이내믹 SOCKS5

신원 & 플릿 상태
  vssh keygen [--rotate]       이 호스트의 Ed25519 신원 출력(또는 회전)
  vssh pubkey                  이 호스트의 공개키 출력
  vssh fleet-state [build [--live]|show|verify]   서명된 플릿 스냅샷 (--live=실시간 도달성 프로브)

셋업 & MCP
  vssh mcp                     MCP 서버 실행 (AI 에이전트용)
  vssh mcp-install --client <c>  AI 클라이언트에 vssh 부착 (claude|claude-code|cursor|gemini|codex)
  vssh mcp-config  [--client c]  MCP 설정 스니펫 출력
  vssh setup                   최초 실행 자가 구성

플릿 & 운영
  vssh / vssh status           대시보드
  vssh list                    알려진 노드 목록
  vssh doctor [--json]         로컬 설정 진단
  vssh deploy <node>           원자적 바이너리 설치 + 재기동 + 검증
  vssh server                  데몬 실행
  vssh version / help
```

---

## MCP 서버 (AI 에이전트용)

`vssh mcp`는 실행 증거를 돌려주는 타입드 도구를 노출합니다. 현재 도구:

| 그룹 | 도구 |
|------|-------|
| 탐지 / 상태 | `vssh_setup`, `vssh_doctor`, `vssh_hosts_list`, `vssh_list`, `vssh_status`, `vssh_fleet_state` |
| 라우팅 | `vssh_route_select`, `vssh_exec_routed` |
| 실행 | `vssh_exec`, `vssh_exec_safe`, `vssh_exec_many`, `vssh_policy_check` |
| 타입드 RPC / facts | `vssh_rpc_call`, `vssh_rpc_many`, `vssh_facts`, `vssh_facts_many` |
| 잡 / 아티팩트 | `vssh_job_start`, `vssh_job_status`, `vssh_job_logs`, `vssh_job_cancel`, `vssh_artifact_collect` |
| 터널 | `vssh_tunnel` (로컬/리버스/SOCKS 포워드 start/list/stop) |
| 설정 (게이트) | `vssh_config_list`, 그리고 — `VSSH_ALLOW_CONFIG_WRITE=1`일 때 — `vssh_config_authorize_key`, `vssh_config_revoke_key`, `vssh_config_set_node`, `vssh_config_pin_node` |

파괴적 명령(`rm -rf`, `shutdown`, `reboot`, `docker rm`, `kubectl delete`,
`systemctl restart` …)은 호출자가 명시적 사람 승인 후 `allow_dangerous: true`를
설정하지 않는 한 **차단**됩니다. 설정 변경 도구는 **기본 OFF**이며
`VSSH_ALLOW_CONFIG_WRITE=1`(운영자가 AI에게 로컬 설정 관리를 명시 위임)이 필요합니다.
모든 응답은 증거 봉투입니다. [docs/MANUAL.ko.md](docs/MANUAL.ko.md) 참고.

---

## Python SDK

```python
from vssh import VSSH

client = VSSH()                            # 키 전용 인증; 설정할 시크릿 없음
client.exec("web1", "uptime")              # -> ExecResult(stdout, exit_code, ...)
client.exec_many(["web1", "db1"], "uptime")
client.facts("web1")                        # 타입드 호스트 facts
job = client.job_start("web1", "long-task")
client.job_status("web1", job["job_id"])
client.doctor()
```

SDK는 설치된 `vssh` 바이너리 위의 얇은 클라이언트입니다(프로토콜을 재구현하지
않음). 따라서 동일한 전송·인증·정책을 그대로 상속합니다.
[docs/PYTHON_SDK.ko.md](docs/PYTHON_SDK.ko.md) 참고.

---

## 설정

### 노드 인벤토리 — `~/.vssh/servers.json`

```json
{
  "web1": { "ip": "192.0.2.10", "user": "deploy", "tags": ["linux", "web"], "capabilities": ["docker"] },
  "gpu1": { "ip": "192.0.2.20", "user": "ubuntu", "tags": ["gpu"], "capabilities": ["cuda", "ollama"], "monitor_port": 8721 }
}
```

노드는 Wire 코디네이터, Tailscale, 로컬 캐시에서도 자동 발견됩니다. **실제
`servers.json`·호스트명·VPN IP·키를 커밋하지 마세요** — 인벤토리는 레포 밖에 두고
문서엔 예시 값을 쓰세요.

### 환경 변수

| 변수 | 용도 |
|----------|---------|
| `VSSH_PORT` | 데몬 수신 포트 (기본 **48291**). |
| `VSSH_REQUIRE_TLS` | `1` = 비-TLS 연결 거부. |
| `VSSH_REQUIRE_VAUTH` | `1` = 노드별 키 인증 강제(플릿 전체 적용 중). |
| `VSSH_NO_HOSTKEY_VERIFY` | `1` = 호스트 신원검증 해제(비권장). |
| `VSSH_NO_AUTOSETUP` | `1` = 첫 MCP exec의 제로터치 자동셋업 비활성. |
| `VSSH_ALLOW_CONFIG_WRITE` | `1` = 게이트된 AI 설정관리 MCP 도구 허용. |
| `VSSH_VERSION` | (설치기/pip 래퍼) 받을 바이너리 릴리스 고정. |
| `VSSH_HOME` | `~/.vssh` 디렉터리 재정의. |

---

## 보안

- 데몬은 인가된 피어에게 **구성된 사용자 권한으로** 명령 실행·파일 전송을 허용합니다
  — 접근을 root 동급으로 취급하세요.
- 인증은 **노드별 Ed25519 키(VAUTH1)** 전용이며 공유 시크릿이 없습니다.
  `~/.vssh/authorized_keys`로 클라이언트를 인가하고, `VSSH_REQUIRE_TLS=1`로 암호화,
  `VSSH_REQUIRE_VAUTH=1`로 키 인증을 강제하세요.
- VPN(WireGuard/Tailscale)은 터널을 암호화하지만 vssh 인증의 대체가 **아닙니다**
  — 키를 인가하고 수신 포트를 방화벽으로 막으세요.
- 키 회전은 `vssh keygen --rotate` + `scripts/rotate_authorized_key.sh`;
  [docs/KEY_ROTATION.md](docs/KEY_ROTATION.md) 참고.
- 취약점은
  [GitHub Security Advisories](https://github.com/zeus-kim/vssh/security/advisories/new)로
  비공개 제보.

전체 모델과 외부 감사 패키지: [SECURITY.md](SECURITY.md) ·
[docs/SECURITY_AUDIT_PACKAGE.md](docs/SECURITY_AUDIT_PACKAGE.md).

---

## 문서

- [왜 vssh인가](docs/WHY_VSSH.ko.md) — 포지셔닝, ssh vs vssh, 오늘 shipped된 것
- [Python SDK](docs/PYTHON_SDK.ko.md) · [Codex/에이전트 오케스트레이션](docs/MANUAL.ko.md)
- [키 회전 & 복구](docs/KEY_ROTATION.md) · [보안 감사 패키지](docs/SECURITY_AUDIT_PACKAGE.md)
- [SECURITY.md](SECURITY.md) · [CONTRIBUTING.md](CONTRIBUTING.md) · [CHANGELOG.md](CHANGELOG.md) · [English README](README.md)

## 빌드 & 기여

```bash
make build      # ./vssh 빌드
make test       # go test ./... + Python SDK 테스트
make release    # 9개 타깃(리눅스 7아치 + macOS amd64/arm64) 크로스컴파일 -> dist/
```

[CONTRIBUTING.md](CONTRIBUTING.md) 참고.

## 라이선스

MIT
