# vssh

[![CI](https://github.com/zeus-kim/vssh/actions/workflows/ci.yml/badge.svg)](https://github.com/zeus-kim/vssh/actions/workflows/ci.yml) [![PyPI](https://img.shields.io/pypi/v/vssh.svg)](https://pypi.org/project/vssh/) [![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE) [![Release](https://img.shields.io/github/v/release/zeus-kim/vssh)](https://github.com/zeus-kim/vssh/releases/latest)

<div align="center">

# AI asks. vssh answers.

*"Show me my fleet"*  
*"What services are running?"*  
*"Check GPU status"*  
*"Find all services on all servers"*  
*"Suggest reallocation"*  

Just connect Claude and ask.

![demo](docs/demo.gif)

</div>

---

## Why vssh

`vssh` is not "SSH with a shorter command" — it is a different abstraction for
when the operator is often an **AI agent or automation runtime**, not a person
at a terminal.

| | OpenSSH | vssh |
|---|---|---|
| Target prerequisite | `sshd` + users + host keys + PAM | one static binary, **no sshd** |
| Auth | passwords / keys via PAM | per-node **Ed25519 VAUTH1**; no shared secret |
| Transport | SSH protocol | **TLS 1.3 + Ed25519 raw-key pinning** |
| Command result | raw text stream | **typed evidence** (stdout/stderr/exit/duration) |
| Authorization | shell = full access | per-key **capabilities** + command **policy** |
| Audit | none built in | **hash-chained** record per action |
| AI / automation | parse text | **MCP-native** typed tools |

Full comparison: [docs/WHY_VSSH.md](docs/WHY_VSSH.md)

---

## AI Runtime Features

This is what makes vssh an **AI execution runtime**, not just an SSH replacement.

| Feature | What it does |
|---------|--------------|
| **Memory** | AI remembers each node's role, services, history |
| **Intent** | "Check disk on web servers" → verified command plan |
| **Workflow** | Predefined multi-step operations, invokable by name |
| **Diff** | Human-readable audit log summaries |
| **Policy** | Dangerous commands blocked by default |
| **Evidence** | Typed results (stdout/stderr/exit/duration), not text |
| **Audit** | Hash-chained, key-attributed, tamper-evident |

### MCP Tools

| Category | Tools |
|----------|-------|
| **Memory** | `vssh_memory` — per-node role, services, tags, notes |
| **Intent** | `vssh_intent` — natural language → command plan |
| **Workflow** | `vssh_workflow` — multi-step flows by name |
| **Diff** | `vssh_diff` — what changed, when, by whom |
| **Execution** | `vssh_exec`, `vssh_query`, `vssh_job` |
| **Fleet** | `vssh_fleet`, `vssh_route`, `vssh_config` |

---

## Install

**Recommended** — download, review, execute:

```bash
curl -fsSLO https://raw.githubusercontent.com/zeus-kim/vssh/main/install.sh
less install.sh        # inspect before running
chmod +x install.sh && ./install.sh
```

> vssh recommends reviewing scripts before execution.

**Fast** — convenience install:

```bash
curl -fsSL https://raw.githubusercontent.com/zeus-kim/vssh/main/install.sh | bash
```

**pip** — Python SDK wrapper (auto-downloads Go binary on first run):

```bash
pip install vssh
```

---

## Connect to Claude

```bash
vssh mcp-install --client claude   # or: cursor, codex, gemini
```

That's it. Restart Claude and ask about your fleet.

---

## Quick start

On the **target** node, start the daemon:

```bash
vssh server                  # listens on :48291
```

Authorize a client by adding its public key to `~/.vssh/authorized_keys`.
Then, from the client:

```bash
vssh run web1 "df -h"        # run a command, get structured evidence
vssh web1                    # interactive shell (PTY)
vssh put ./app web1:/tmp/    # upload a file
vssh get web1:/var/log/x .   # download a file
vssh fwd web1 -L 8080:localhost:80   # port-forward
vssh                         # fleet dashboard
```

---

## How it works

```text
vssh client ──TLS 1.3──▶ vssh server ──▶ typed exec / file / job / RPC ──▶ structured evidence
            (Ed25519 pinned)   (:48291)
```

1. **Transport** — TLS 1.3 with Ed25519 public key pinned (not a CA)
2. **Host identity** — verified against trusted registry (default-on)
3. **Authentication** — per-node Ed25519 challenge–response (VAUTH1)
4. **Policy + audit** — commands gated, every action hash-chained
5. **Fleet state** — signed, timestamped snapshot replicable to nodes

---

## CLI reference

```
Execution
  vssh <node>                 Interactive PTY shell
  vssh run <node> <cmd>       Run a command (structured result)
  vssh run-many <n1,n2> <cmd> Run across nodes

Typed APIs
  vssh facts <node>           Typed host facts
  vssh rpc <node> <method>    Call a typed daemon RPC

Jobs
  vssh job-start <node> <cmd> / job-status / job-logs / job-cancel

Files & tunnels
  vssh put / get              Upload / download
  vssh fwd <node> -L/-R/-D    Port forwarding

Fleet
  vssh / vssh status          Dashboard
  vssh list                   List nodes
  vssh doctor                 Diagnose setup
```

---

## Python SDK

```python
from vssh import VSSH

client = VSSH()
client.exec("web1", "uptime")              # -> ExecResult
client.exec_many(["web1", "db1"], "uptime")
client.facts("web1")                        # typed host facts
```

See [docs/PYTHON_SDK.md](docs/PYTHON_SDK.md).

---

## Security

- **Key-only auth** — per-node Ed25519 (VAUTH1), no shared secret
- **Transport** — TLS 1.3 with raw-key pinning
- **Policy** — per-key capabilities + command allowlist
- **Audit** — hash-chained, attributed, tamper-evident

Report vulnerabilities via [GitHub Security Advisories](https://github.com/zeus-kim/vssh/security/advisories/new).

Full model: [SECURITY.md](SECURITY.md) · [docs/SECURITY_AUDIT_PACKAGE.md](docs/SECURITY_AUDIT_PACKAGE.md)

---

## Documentation

- [AI Runtime](docs/AI_RUNTIME.md) — Memory, Intent, Workflow, Diff
- [Why vssh](docs/WHY_VSSH.md) — positioning, ssh vs vssh
- [Python SDK](docs/PYTHON_SDK.md) · [Usage manual](docs/MANUAL.md)
- [Key rotation](docs/KEY_ROTATION.md) · [Examples](examples/)
- [CHANGELOG.md](CHANGELOG.md) · [한국어 README](README.ko.md)

## Building & contributing

```bash
make build      # build ./vssh
make test       # run tests
make release    # cross-compile for all platforms
```

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache-2.0 — see [LICENSE](LICENSE).
