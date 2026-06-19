# vssh — 사용 매뉴얼 (CLI + MCP)

`vssh`는 플릿과 AI 에이전트를 위해 임시 SSH를 대체하는 AI-native 원격 실행
데몬·클라이언트입니다. 노드별 Ed25519 키로 TLS 1.3 위에서 간결한 네이티브
프로토콜을 사용하고, 키별 capability·policy를 강제하며, 모든 명령을 해시체인
감사 로그에 남깁니다. 두 가지 방식으로 동작합니다:

- **CLI** — 터미널의 사람용 (`vssh run`, `vssh facts` 등)
- **MCP** — MCP JSON-RPC 서버(`vssh mcp`)로, AI 모델이 구조화된 증거를 돌려받는
  도구로 플릿을 직접 운용

English: [MANUAL.md](MANUAL.md).

---

## 1. 설치

```bash
# 원라인 설치 (체크섬 검증)
curl -fsSL https://raw.githubusercontent.com/zeus-kim/vssh/main/install.sh | bash

# 또는 소스 빌드 (Go 1.25+)
git clone https://github.com/zeus-kim/vssh && cd vssh
make build          # ./vssh 생성
```

> 프로젝트 재이전 시 clone/설치 URL이 바뀝니다. 최신 위치는 레포 README 참고.

## 2. 신원과 신뢰 (공유 시크릿 없음)

최초 실행 시 `~/.vssh/vssh_id`에 호스트별 Ed25519 신원을 생성합니다(공개키는
`vssh_id.pub`). 신뢰 모델은 PKI가 아니라 raw-key 핀입니다:

- **데몬**은 `~/.vssh/authorized_keys`(또는 `/etc/vssh/authorized_keys`)에 등록된
  클라이언트 키만 인가합니다. 라인 형식: `<pubB64> [caps=exec,file,rpc,shell,forward] [policy=<name>] [주석]`.
- **클라이언트**는 각 데몬 키를 `~/.vssh/known_hosts`에 핀합니다(최초 접속 TOFU,
  이후 불일치는 평문으로 내려가지 않고 하드 실패).

오퍼레이터 키 인가 + 필요 시 로테이션:

```bash
vssh keygen                 # 이 호스트 신원 생성/표시
vssh keygen --rotate        # 신원 로테이션 (docs/KEY_ROTATION.md 참고)
# 노드의 authorized_keys에 "<pubB64> caps=exec,rpc fleet-operator" 추가
```

## 3. 데몬 실행

```bash
vssh server                 # :48291 리슨 (VSSH_PORT로 변경)
```

운영 노드는 systemd(Linux)/launchd(macOS)로 구동합니다. 강화용 환경변수(아래)는
대화형이 아니라 서비스 유닛에 설정하세요.

## 4. CLI 레퍼런스

실행:

```bash
vssh run <host> "<cmd>"            # 명령 실행 (네이티브 프로토콜, TLS)
vssh exec <host> "<cmd>"           # run 별칭
vssh run-many h1,h2,h3 "<cmd>"     # 호스트 병렬 실행
vssh run-async <host> "<cmd>" --wait 5   # 5초 내 끝나면 인라인, 아니면 job id 반환
vssh shell <host[:port]>           # 대화형 셸
vssh bench <host> [count]          # 네이티브 실행 지연 측정
```

타입드 조회 (구조화 JSON, 자동화에 권장):

```bash
vssh facts <host>                  # 타입드 노드 facts (os, arch, disk 등)
vssh facts-many h1,h2              # 병렬 facts
vssh rpc <host> <method> [json]    # 타입드 RPC, 예: node_info, get_disk
vssh rpc-many h1,h2 <method> [json]
```

장기 작업(job):

```bash
vssh job-start <host> "<cmd>"      # 백그라운드 job 시작 -> job id
vssh job-status <host> <id>
vssh job-logs <host> <id>
vssh job-cancel <host> <id>
vssh artifact-collect <host> <path> [max-bytes]   # 파일/디렉터리 아티팩트 메타데이터
```

파일·배포:

```bash
vssh put <file> <host:path>        # 업로드
vssh get <host:path> <file>        # 다운로드
vssh deploy-binary <local> <host> <remote-path> \
    --service <svc> --mode 0755 --verify "<cmd>"   # atomic 설치+재시작+검증, 감사됨
```

관리·진단:

```bash
vssh status                        # 연결 상태
vssh list                          # 피어 목록
vssh doctor [--json]               # 바이너리/인증모델/피어/MCP 준비 상태 진단
```

## 5. 보안과 환경변수

| 변수 | 효과 |
| --- | --- |
| `VSSH_PORT` | 데몬 포트 (기본 `48291`). |
| `VSSH_REQUIRE_TLS` | `1`/`true`/`yes` = 비-TLS(평문) 연결 거부; 강제 상태가 `node_info` 보고값과 일치. |
| `VSSH_REQUIRE_VAUTH` | `1`/`true`/`yes` = 노드별 Ed25519 인증 필수(유일한 인증 모델; 비-VAUTH1 라인 거부). |
| `VSSH_ALLOW_CONFIG_WRITE` | `1` = 이 호스트에서 게이트된 MCP `vssh_config_*` 쓰기 도구 허용. |
| `VSSH_NO_HOSTKEY_VERIFY` | `1` = 호스트 신원 검증 해제 (권장 안 함). |
| `VSSH_NO_TLS` | `1` = 디버깅 탈출구; `VSSH_REQUIRE_TLS`가 항상 우선. |
| `VSSH_NO_AUTOSETUP` | `1` = 최초 호출 시 호스트키 자동 프로비저닝 비활성화. |

플릿 권장 자세: 모든 데몬에 `VSSH_REQUIRE_VAUTH=1` + `VSSH_REQUIRE_TLS=1`,
호스트 신원 검증은 ON 유지.

## 6. MCP 모드 — AI 모델용

`vssh mcp`는 플릿을 MCP 클라이언트(Claude Desktop, Claude Code, Cursor, Codex 등)에
JSON-RPC 도구로 노출합니다. 터미널 텍스트가 아니라 구조화된 증거를 돌려주므로
모델이 화면 파싱 없이 실행·검증할 수 있습니다.

수동 편집 없이 부착:

```bash
# 클라이언트: claude (Desktop) | claude-code | cursor | gemini (Google AI Studio) | codex
vssh mcp-config  --client cursor   # 클라이언트용 설정 스니펫 출력
vssh mcp-install --client cursor   # 해당 클라이언트 설정에 병합
vssh mcp                           # (클라이언트가 실행하는) JSON-RPC 서버
```

기본적으로 도구는 **그룹 action-도구**로 노출됩니다(약 12개; 예: `vssh_exec`를 `action: safe`로 호출). 그룹과 action:

| 그룹 | 도구 | 용도 |
| --- | --- | --- |
| 발견 | `vssh_doctor`, `vssh_status`, `vssh_list`, `vssh_hosts_list`, `vssh_setup` | 상태·인벤토리·자기 부트스트랩. |
| 실행 | `vssh_exec`, `vssh_exec_safe`, `vssh_exec_routed`, `vssh_exec_many` | 명령 실행 (단일/정책검사/라우팅/병렬). |
| 타입드 조회 | `vssh_facts`, `vssh_facts_many`, `vssh_rpc_call`, `vssh_rpc_many` | 구조화 노드 facts·타입드 RPC. |
| 라우팅·정책 | `vssh_route_select`, `vssh_policy_check` | 경로 선택; 자문용 정책 사전검사(데몬이 최종 권위). |
| Job | `vssh_job_start`, `vssh_job_status`, `vssh_job_logs`, `vssh_job_cancel` | 장기 작업. |
| 아티팩트·전송 | `vssh_artifact_collect`, `vssh_tunnel` | 파일/디렉터리 증거 수집; 포트 포워딩. |
| 플릿 상태 | `vssh_fleet_state` | 컨트롤러 서명 스냅샷(인벤토리+키+생존성). |
| 설정 (게이트) | `vssh_config` (list/authorize_key/revoke_key/set_node/pin_node) | 로컬 설정 관리. 쓰기는 `VSSH_ALLOW_CONFIG_WRITE=1` 필요. |
| 메모리 | `vssh_memory` (get/set/note/auto_note/find/ask) | 노드별 역할/서비스/태그/노트. |
| 워크플로 | `vssh_workflow` (list/run/status) | 사전 정의 다단계 플로. |
| NL·diff | `vssh_intent`, `vssh_diff` | 자연어→명령 계획; 감사로그 변경 요약. |

에이전트 안전 모델: 모든 호출은 오퍼레이터 키로 인증되고, 그 키의 capability와
선택적 policy(명령 allow/deny, 경로 범위, forward 대상, rate, 위험 사전승인)로
제한되며, 키·전송(tls/plain)·매칭 룰과 함께 해시체인 감사 로그에 기록됩니다.
설정 변경 도구는 명시적으로 켜기 전엔 OFF입니다.

전형적 에이전트 루프: `vssh_doctor` → `vssh_facts`/`vssh_fleet_state`로 파악 →
`vssh_exec_safe`/`vssh_exec_routed`로 실행 → `vssh_rpc_call node_info` /
`vssh_artifact_collect`로 검증.

> 토큰 비용 절감을 위해 그룹 action-도구가 기본입니다. 레거시 verb별 이름은
> `VSSH_MCP_TOOLSET=flat`, 최소 세트는 `=core`. `docs/MCP_TOOLSET.md` 참고.

## 7. 정책(Policy)

키별 `policy=<name>` 태그는 `authorized_keys` 라인을 `~/.vssh/policies/<name>.json`
정책에 묶습니다: deny 우선 → 위험 사전승인 → allow → 그 외 거부; 앵커드 전체
문자열 매칭; symlink/`..` 해석을 포함한 경로 범위; 정책 파일 없으면 fail-closed.
[policies/README.md](../policies/README.md),
[SECURITY_TRANSPORT_MIGRATION.md](SECURITY_TRANSPORT_MIGRATION.md) §6 참고.

## 8. 문제 해결

```bash
vssh doctor                 # 1순위: 바이너리 충돌, 인증 모델, 피어, MCP 준비 상태
```

- "AUTH_FAILED" → 클라이언트 키가 노드 `authorized_keys`에 없거나,
  `VSSH_REQUIRE_TLS=1`인데 클라이언트가 평문으로 접속.
- "host identity mismatch" → 도달한 데몬 키가 핀과 다름; 미스라우트 또는 노드 키
  변경(로테이션 후 갱신 필요).
- 버전 불일치 → `vssh doctor`가 PATH의 낡은/충돌 바이너리를 보고.
- 컨트롤러가 자기 자신을 대상으로 할 때(예: `m1`에서 `vssh exec m1`)도 동작:
  self-target(루프백/자기 IP 또는 OS/Tailscale 호스트명으로 판별)은 접속 불가한
  자기 IP에서 멈추지 않고 `127.0.0.1`로 연결.
- fleet state가 오래됨(stale) → 컨트롤러에서 주기적 재빌드 스케줄 등록:
  `scripts/install_fleet_state_refresh.sh install` (macOS는 launchd, Linux는 cron;
  기본 8시간 간격, `INTERVAL_HOURS=`로 조정, `REPLICATE=1`이면 노드에도 배포).
  `… status` / `… uninstall`로 관리.

참고: [README](../README.ko.md), [HELP](../HELP.md),
[WHY_VSSH](WHY_VSSH.ko.md), [KEY_ROTATION](KEY_ROTATION.md),
[PYTHON_SDK](PYTHON_SDK.ko.md).
