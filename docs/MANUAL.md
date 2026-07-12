# vssh — User Manual (CLI + MCP)

`vssh` is an AI-native remote-execution daemon and client that replaces ad hoc
SSH for fleets and AI agents. It speaks a compact native protocol over TLS 1.3
with per-node Ed25519 keys, enforces per-key capabilities and policy, and
hash-chain audits every command. It works two ways:

- **CLI** — for humans at a terminal (`vssh run`, `vssh facts`, …).
- **MCP** — an MCP JSON-RPC server (`vssh mcp`) so AI models can drive a fleet
  with structured, evidence-returning tools.

Korean version: [MANUAL.ko.md](MANUAL.ko.md).

---

## 1. Install

```bash
# One-line installer (checksum-verified)
curl -fsSL https://raw.githubusercontent.com/zeus-kim/vssh/main/install.sh | bash

# Or build from source (Go 1.25+)
git clone https://github.com/zeus-kim/vssh && cd vssh
make build          # produces ./vssh
```

> The clone/install URL changes when the project is re-homed; see the repo's
> README for the current location.

## 2. Identity & trust (no shared secret)

On first run vssh generates a per-host Ed25519 identity at `~/.vssh/vssh_id`
(public half `vssh_id.pub`). Trust is raw-key pinning, not PKI:

- **Daemon** authorizes client keys listed in `~/.vssh/authorized_keys`
  (or `/etc/vssh/authorized_keys`). Each line: `<pubB64> [caps=exec,file,rpc,shell,forward] [policy=<name>] [comment]`.
- **Client** pins each daemon's key in `~/.vssh/known_hosts` (TOFU on first
  contact; a later mismatch is a hard failure, never a downgrade).

Authorize an operator key, then rotate when needed:

```bash
vssh keygen                 # create/show this host's identity
vssh keygen --rotate        # rotate identity (see docs/KEY_ROTATION.md)
# add "<pubB64> caps=exec,rpc fleet-operator" to a node's authorized_keys
```

## 3. Run the daemon

```bash
vssh server                 # listen on :48291 (override with VSSH_PORT)
```

Production nodes run it under systemd (Linux) or launchd (macOS). Set the
hardening envs (below) in the service unit, not interactively.

## 4. CLI reference

Execution:

```bash
vssh run <host> "<cmd>"            # run a command (native protocol, TLS)
vssh exec <host> "<cmd>"           # alias for run
vssh run-many h1,h2,h3 "<cmd>"     # parallel across hosts
vssh run-async <host> "<cmd>" --wait 5   # inline if it finishes in 5s, else a job id
vssh shell <host[:port]>           # interactive shell
vssh bench <host> [count]          # measure native exec latency
```

Typed queries (structured JSON, preferred for automation):

```bash
vssh facts <host>                  # typed daemon facts (os, arch, disk, …)
vssh facts-many h1,h2              # parallel facts
vssh rpc <host> <method> [json]    # typed RPC, e.g. node_info, get_disk
vssh rpc-many h1,h2 <method> [json]
```

Long jobs:

```bash
vssh job-start <host> "<cmd>"      # start a background job -> job id
vssh job-status <host> <id>
vssh job-logs <host> <id>
vssh job-cancel <host> <id>
vssh artifact-collect <host> <path> [max-bytes]   # file/dir artifact metadata
```

Files & deploys:

```bash
vssh put <file> <host:path>        # upload
vssh get <host:path> <file>        # download
vssh deploy-binary <local> <host> <remote-path> \
    --service <svc> --mode 0755 --verify "<cmd>"   # atomic install + restart + verify, audited
```

Admin & diagnostics:

```bash
vssh status                        # connection status
vssh list                          # list peers
vssh doctor [--json]               # diagnose binary, auth model, peers, MCP readiness
```

## 4a. AI Runtime — memory, intent, workflow, diff

Four standalone layers turn raw exec into an operator loop: **remember** the
fleet, **plan** from plain language, **run** repeatable playbooks, and **review**
what changed. All are rule-based (no LLM, no network beyond vssh's own transport)
and store state under `~/.vssh/`. Flags accept both `--flag value` and
`--flag=value`.

**Memory** — per-node role/services/tags and a rolling note log
(`~/.vssh/fleet_memory.json`):

```bash
vssh memory get [node]                              # show memory (all nodes, or one)
vssh memory set d1 --role gpu --services ollama,nvidia --tags prod,120b
vssh memory note d1 "swapped to 550W PSU"           # timestamped event note
vssh memory find --role gpu --tag prod [query]      # filter/search nodes
vssh memory auto-note d1 "<command output>"         # extract notes (df≥85%, failed units, load…)
vssh memory ask "which nodes run ollama"            # natural-language query
```

**Intent** — a plain-language request → a command plan (23 built-ins:
disk/log/service/gpu/process/memory/network/…). Plans by default; `--run` needs
`--target`. Override or add intents in `~/.vssh/intents.json`:

```bash
vssh intent "disk check"                            # show the plan only
vssh intent "service check nginx" --target d1 --run # plan + run on d1
vssh intent "gpu status" --target g1 --run --json   # structured output
```

`--target` is a **fleet selector**: a comma-separated mix of literal hosts and
memory-backed selectors, run in parallel with results returned per node — one
request fans out across the fleet instead of a shell loop over ssh:

| Selector | Expands to |
| --- | --- |
| `d1,g1` | those literal hosts |
| `@gpu` | every node whose role **or** tag **or** service is `gpu` |
| `@role:gpu` · `@tag:prod` · `@service:ollama` | that one facet |
| `@all` | every node in fleet memory |

```bash
vssh intent "gpu status" --target @gpu --run        # every GPU box, in parallel
vssh intent "disk check" --target @tag:prod --run --json   # per-node JSON
```

The same selector works from MCP (`vssh_intent` `target`), returning a `nodes`
array of per-node results — so an agent audits the whole fleet in one call.

**Workflow** — predefined multi-step playbooks with branching. `on_fail` per
step is `abort` | `continue` | `<step-id>` (jump); runs are recorded under
`~/.vssh/workflow_runs/`. Built-ins: `service-restart` (param `service`),
`health-check`, `disk-cleanup`, `log-collect`. Add your own as
`~/.vssh/workflows/*.json`:

```bash
vssh workflow list                                  # built-ins + your JSON
vssh workflow run health-check --target d1
vssh workflow run service-restart --target d1 --param service=nginx
vssh workflow run disk-cleanup --target d1 --dry-run   # plan without executing
vssh workflow status <run-id>                       # replay a past run
```

**Diff** — turn the append-only audit log into a human account of what was done.
Commands are grouped into operator sessions (same key+endpoint, 5-min gap) and
before/after is inferred from the command text (the log stores commands, not
output — e.g. `sed -i 's/listen 80/…/'` renders `listen 80 → 443`):

```bash
vssh diff                                           # local daemon's audit log
vssh diff --node d1 --since 2h                      # what changed on d1 recently
vssh diff --last 5 --json                           # newest 5 sessions, structured
```

## 5. Security & environment variables

| Variable | Effect |
| --- | --- |
| `VSSH_PORT` | Daemon port (default `48291`). |
| `VSSH_REQUIRE_TLS` | `1`/`true`/`yes` = refuse non-TLS (plaintext) connections; enforced state matches what `node_info` reports. |
| `VSSH_REQUIRE_VAUTH` | `1`/`true`/`yes` = require per-node Ed25519 auth (the only auth model; rejects any non-VAUTH1 line). |
| `VSSH_ALLOW_CONFIG_WRITE` | `1` = allow the gated MCP `vssh_config_*` write tools on this host. |
| `VSSH_NO_HOSTKEY_VERIFY` | `1` = opt out of host-identity verification (not recommended). |
| `VSSH_NO_TLS` | `1` = debugging escape hatch; `VSSH_REQUIRE_TLS` always wins. |
| `VSSH_NO_AUTOSETUP` | `1` = disable first-call host-key auto-provisioning. |

Recommended posture for a fleet: `VSSH_REQUIRE_VAUTH=1` and `VSSH_REQUIRE_TLS=1`
on every daemon, host-identity verification left ON.

## 6. MCP mode — for AI models

`vssh mcp` exposes the fleet to an MCP client (Claude Desktop, Claude Code,
Cursor, Codex, …) as JSON-RPC tools that return structured evidence rather than
terminal text — so a model can act and verify without screen-scraping.

Attach with no hand-editing:

```bash
# clients: claude (Desktop) | claude-code | cursor | gemini (Google AI Studio) | codex
vssh mcp-config  --client cursor   # print the config snippet for a client
vssh mcp-install --client cursor   # merge it into that client's config file
vssh mcp                           # (what the client runs) the JSON-RPC server
```

By default the toolset is advertised as **grouped action-tools** (~12 tools; call e.g. `vssh_exec` with `action: safe`). The groups and their actions:

| Group | Tools | Use |
| --- | --- | --- |
| Discovery | `vssh_doctor`, `vssh_status`, `vssh_list`, `vssh_hosts_list`, `vssh_setup` | Health, inventory, self-bootstrap. |
| Execution | `vssh_exec`, `vssh_exec_safe`, `vssh_exec_routed`, `vssh_exec_many` | Run commands (single / policy-checked / routed / parallel). |
| Typed queries | `vssh_facts`, `vssh_facts_many`, `vssh_rpc_call`, `vssh_rpc_many` | Structured node facts and typed RPCs. |
| Routing & policy | `vssh_route_select`, `vssh_policy_check` | Pick a path; advisory policy pre-check (daemon stays authoritative). |
| Jobs | `vssh_job_start`, `vssh_job_status`, `vssh_job_logs`, `vssh_job_cancel` | Long-running work. |
| Artifacts & transport | `vssh_artifact_collect`, `vssh_tunnel` | Collect file/dir evidence; port-forward. |
| Fleet state | `vssh_fleet_state` | Controller-signed snapshot (inventory + keys + liveness). |
| Config (gated) | `vssh_config` (list/authorize_key/revoke_key/set_node/pin_node) | Manage local config. Writes require `VSSH_ALLOW_CONFIG_WRITE=1`. |
| Memory | `vssh_memory` (get/set/note/auto_note/find/ask) | Per-node role/services/tags/notes. |
| Workflows | `vssh_workflow` (list/run/status) | Predefined multi-step flows. |
| NL & diff | `vssh_intent`, `vssh_diff` | NL request → command plan; human summary of audit-log changes. |

Safety model for agents: every call is authenticated by the operator key,
constrained by that key's capabilities and optional policy (command allow/deny,
path scope, forward targets, rate, danger pre-approval), and recorded in the
hash-chained audit log with the key, transport, and matched rule. Mutating
config tools are OFF unless explicitly enabled.

Typical agent loop: `vssh_doctor` → `vssh_facts`/`vssh_fleet_state` to orient →
`vssh_exec_safe`/`vssh_exec_routed` to act → `vssh_rpc_call node_info` /
`vssh_artifact_collect` to verify.

> Grouped action-tools are the default to cut token cost. Set
> `VSSH_MCP_TOOLSET=flat` for the legacy per-verb tool names, or `=core` for a
> minimal subset. See `docs/MCP_TOOLSET.md`.

## 7. Policies

A per-key `policy=<name>` tag binds an `authorized_keys` line to a JSON policy
under `~/.vssh/policies/<name>.json`: deny-first, then danger pre-approved, then
allow, else refuse; anchored full-string matching; path scope with symlink/`..`
resolution; fail-closed if the policy file is missing. See
[policies/README.md](../policies/README.md) and
[SECURITY_TRANSPORT_MIGRATION.md](SECURITY_TRANSPORT_MIGRATION.md) §6.

## 8. Troubleshooting

```bash
vssh doctor                 # first stop: binary conflicts, auth model, peers, MCP readiness
```

- "AUTH_FAILED" → the client key is not in the node's `authorized_keys`, or
  `VSSH_REQUIRE_TLS=1` is set and the client reached it in plaintext.
- "host identity mismatch" → the reached daemon key differs from the pin; a
  misroute or a changed node key (refresh after rotation).
- Version skew → `vssh doctor` reports stale/conflicting binaries on `PATH`.
- Targeting the controller itself (e.g. `vssh exec m1` on `m1`) works: a
  self-target — matched by loopback/own IP or by the OS/Tailscale hostname —
  resolves to `127.0.0.1` instead of stalling on an un-dialable self-IP.
- Stale fleet state → schedule periodic rebuilds on the controller:
  `scripts/install_fleet_state_refresh.sh install` (launchd on macOS, cron on
  Linux; default every 8h, `INTERVAL_HOURS=` to tune, `REPLICATE=1` to also push
  the snapshot to nodes). `… status` / `… uninstall` manage it.

See also: [README](../README.md), [HELP](../HELP.md),
[WHY_VSSH](WHY_VSSH.md), [KEY_ROTATION](KEY_ROTATION.md),
[PYTHON_SDK](PYTHON_SDK.md).
