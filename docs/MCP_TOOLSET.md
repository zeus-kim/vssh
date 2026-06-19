# vssh MCP toolset — token-cost reduction

## Problem

`vssh mcp` advertises ~27 tools via `tools/list`. MCP clients load every tool's
name + description + input schema into the model context at session start, so a
large tool surface costs tokens on every conversation, whether or not the tools
are used.

## What ships now (0.7.44)

`tools/list` is served by `filteredMCPTools()` (cmd/vssh/mcp.go), which trims the
advertised set:

- **Gated config-write tools are hidden by default.** The four mutating tools
  (`vssh_config_authorize_key`, `vssh_config_revoke_key`, `vssh_config_set_node`,
  `vssh_config_pin_node`) are advertised only when `VSSH_ALLOW_CONFIG_WRITE` is
  set. They cannot run without that gate anyway, so advertising them otherwise is
  pure token waste. `vssh_config_list` (read-only) always stays.
- **Opt-in core subset.** `VSSH_MCP_TOOLSET=core` advertises only a curated
  essential set — `vssh_doctor`, `vssh_list`, `vssh_fleet_state`, `vssh_exec`,
  `vssh_exec_safe`, `vssh_facts`, `vssh_rpc_call` — for the smallest footprint.
- **Default (`full`)** = everything minus the disabled config writes.

Set the env on the MCP server (the process the client launches), e.g. in the
`vssh mcp-config` JSON `"env"` block.

## Grouped dispatcher tools (default since 0.7.44)

The per-verb tools are collapsed into a few **grouped tools** that take an
`action` parameter — "enter in big chunks" — advertised by DEFAULT, dropping the
surface from ~38 to 12 while keeping every capability. Flat names stay callable
(`VSSH_MCP_TOOLSET=flat` re-advertises them). Implementation:
`cmd/vssh/mcp_grouped.go` (`groupedMCPTools`, `resolveGroupedTool`):

| Grouped tool | `action` values | Replaces |
| --- | --- | --- |
| `vssh_exec` | `plain` \| `safe` \| `routed` \| `many` | the 4 exec tools |
| `vssh_query` | `facts` \| `facts_many` \| `rpc` \| `rpc_many` | facts/rpc tools |
| `vssh_job` | `start` \| `status` \| `logs` \| `cancel` | the 4 job tools |
| `vssh_fleet` | `doctor` \| `status` \| `list` \| `hosts` \| `state` \| `setup` | discovery + fleet_state |
| `vssh_transport` | `tunnel` \| `artifact_collect` | tunnel + artifact |
| `vssh_route` | `select` \| `policy_check` | routing/policy |
| `vssh_config` | `list` \| `authorize_key` \| `revoke_key` \| `set_node` \| `pin_node` | config (writes still gated) |

Implementation notes for the next session:

- Tools are defined in `cmd/vssh/mcp_schema.go` (`getMCPTools`) and dispatched by
  a `switch` in `cmd/vssh/mcp.go`. Add the grouped schema entries (one `action`
  enum + a union of per-action args) and a dispatcher that maps
  `(tool, action)` to the existing internal handlers — reuse the current case
  bodies, no logic rewrite.
- The repo is being re-published fresh, so there are no legacy MCP consumers to
  keep the old flat tool names for; the contract change is low-cost now. If
  desired, keep the flat names as hidden aliases for one release behind
  `VSSH_MCP_TOOLSET=flat`.
- Add `tools/list` tests per toolset and dispatch tests per `(tool, action)`
  (mirror `cmd/vssh/mcp_toolset_test.go`).

---

# vssh MCP 도구 세트 — 토큰 비용 절감 (한국어)

## 문제

`vssh mcp`는 `tools/list`로 약 27개 도구를 노출합니다. MCP 클라이언트는 세션
시작 시 모든 도구의 이름·설명·입력 스키마를 모델 컨텍스트에 로드하므로, 도구가
많을수록 사용 여부와 무관하게 매 대화마다 토큰을 소모합니다.

## 현재 적용된 것 (0.7.44)

`tools/list`는 `filteredMCPTools()`(cmd/vssh/mcp.go)가 제공하며 노출 세트를
줄입니다:

- **게이트된 설정-쓰기 도구는 기본 숨김.** 변경형 4개
  (`vssh_config_authorize_key`, `vssh_config_revoke_key`, `vssh_config_set_node`,
  `vssh_config_pin_node`)는 `VSSH_ALLOW_CONFIG_WRITE`가 켜진 경우에만 노출됩니다.
  게이트 없이는 어차피 실행 불가라, 그 전엔 노출 자체가 토큰 낭비입니다.
  읽기 전용 `vssh_config_list`는 항상 유지.
- **옵트인 core 세트.** `VSSH_MCP_TOOLSET=core`는 핵심 세트만 노출 —
  `vssh_doctor`, `vssh_list`, `vssh_fleet_state`, `vssh_exec`, `vssh_exec_safe`,
  `vssh_facts`, `vssh_rpc_call`.
- **기본(`full`)** = 비활성 설정-쓰기를 뺀 전체.

환경변수는 MCP 서버(클라이언트가 띄우는 프로세스)에 설정합니다. 예: `vssh
mcp-config` JSON의 `"env"` 블록.

## 그룹 디스패처 도구 (0.7.44부터 기본)

verb별 도구를 `action` 인자를 받는 **그룹 도구**로 묶어 **기본 노출**합니다
("큰 덩어리로 묶어서 진입"). 표면이 약 38 → 12로 줄고 기능은 그대로. 평면
이름은 `VSSH_MCP_TOOLSET=flat`로 계속 호출 가능. 구현:
`cmd/vssh/mcp_grouped.go`(`groupedMCPTools`, `resolveGroupedTool`):

| 그룹 도구 | `action` 값 | 대체 대상 |
| --- | --- | --- |
| `vssh_exec` | `plain` \| `safe` \| `routed` \| `many` | exec 4종 |
| `vssh_query` | `facts` \| `facts_many` \| `rpc` \| `rpc_many` | facts/rpc |
| `vssh_job` | `start` \| `status` \| `logs` \| `cancel` | job 4종 |
| `vssh_fleet` | `doctor` \| `status` \| `list` \| `hosts` \| `state` \| `setup` | 발견 + fleet_state |
| `vssh_transport` | `tunnel` \| `artifact_collect` | tunnel + artifact |
| `vssh_route` | `select` \| `policy_check` | 라우팅/정책 |
| `vssh_config` | `list` \| `authorize_key` \| `revoke_key` \| `set_node` \| `pin_node` | 설정 (쓰기는 여전히 게이트) |

다음 세션 구현 메모:

- 도구는 `cmd/vssh/mcp_schema.go`(`getMCPTools`)에 정의되고 `cmd/vssh/mcp.go`의
  `switch`로 디스패치됩니다. 그룹 스키마 항목(`action` enum + action별 인자
  유니온)과 `(tool, action)`→기존 내부 핸들러 매핑 디스패처를 추가하면 됩니다 —
  기존 case 본문을 재사용, 로직 재작성 불필요.
- 레포를 fresh로 재배포하므로 옛 평면 도구명을 유지할 레거시 MCP 소비자가
  없습니다. 지금이 계약 변경 비용이 가장 낮은 시점입니다. 필요하면 한 릴리스 동안
  `VSSH_MCP_TOOLSET=flat`로 평면명을 숨김 별칭으로 유지.
- 토큰 세트별 `tools/list` 테스트와 `(tool, action)` 디스패치 테스트 추가
  (`cmd/vssh/mcp_toolset_test.go` 참고).
