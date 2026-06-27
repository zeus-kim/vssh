# vssh AI Runtime

> SSH was designed for humans. vssh was designed for AI agents.

vssh is not an SSH wrapper — it is an **AI Operating Layer** for remote
execution. This document describes the AI-native capabilities that make vssh
different from any shell-based tool.

---

## Architecture

```text
┌─────────────────────────────────────────────────────────────┐
│                        AI Agent                             │
│              (Claude, Cursor, Codex, Gemini)                │
└─────────────────────────┬───────────────────────────────────┘
                          │ MCP (Model Context Protocol)
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                      vssh AI Runtime                        │
│                                                             │
│  ┌─────────┐   ┌────────────┐   ┌────────┐   ┌───────────┐ │
│  │ Intent  │ → │Fleet Memory│ → │ Policy │ → │ Workflow  │ │
│  └─────────┘   └────────────┘   └────────┘   └───────────┘ │
│        │                                           │        │
│        └──────────────────┬────────────────────────┘        │
│                           ▼                                 │
│                    ┌───────────┐                            │
│                    │ Execution │                            │
│                    └─────┬─────┘                            │
│                          ▼                                  │
│              ┌──────────────────────┐                       │
│              │ Evidence + Audit     │                       │
│              └──────────────────────┘                       │
└─────────────────────────┬───────────────────────────────────┘
                          │ TLS 1.3 + Ed25519
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                       Your Fleet                            │
│        [ web1 ]  [ db1 ]  [ gpu1 ]  [ worker-* ]           │
└─────────────────────────────────────────────────────────────┘
```

---

## AI Runtime Capabilities

### 1. Intent — Natural Language to Command Plan

The AI doesn't need to construct shell commands from scratch. It describes
**what** it wants, and vssh plans **how**.

**MCP Tool:** `vssh_intent`

```json
{
  "request": "Check disk usage on all web servers",
  "context": { "fleet_tags": ["web"] }
}
```

**Response:**

```json
{
  "plan": [
    { "host": "web1", "command": "df -h", "policy_check": "passed" },
    { "host": "web2", "command": "df -h", "policy_check": "passed" },
    { "host": "web3", "command": "df -h", "policy_check": "passed" }
  ],
  "dangerous": false,
  "approval_required": false
}
```

The agent can then execute this plan with a single `vssh_exec(action: "many")`.

---

### 2. Fleet Memory — The AI Remembers Your Infrastructure

Traditional tools have no memory. Every session starts from zero. vssh maintains
**persistent per-node context** that the AI can read, write, and query.

**MCP Tools:**
- `vssh_memory(action: "get", host: "gpu1")` — retrieve node context
- `vssh_memory(action: "set", host: "gpu1", data: {...})` — store node context
- `vssh_memory(action: "note", host: "gpu1", note: "...")` — append observation
- `vssh_memory(action: "auto_note", host: "gpu1")` — AI summarizes recent activity
- `vssh_memory(action: "find", query: "CUDA version")` — semantic search
- `vssh_memory(action: "ask", question: "Which hosts have low disk?")` — Q&A

**Example memory record:**

```json
{
  "host": "gpu1",
  "role": "GPU compute node",
  "services": ["ollama", "cuda-toolkit"],
  "tags": ["gpu", "ml", "production"],
  "notes": [
    { "ts": "2025-06-15T10:30:00Z", "text": "Upgraded to CUDA 12.4" },
    { "ts": "2025-06-20T14:00:00Z", "text": "Memory pressure observed during batch inference" }
  ],
  "facts": {
    "gpu_model": "NVIDIA A100",
    "gpu_memory": "80GB",
    "cuda_version": "12.4"
  }
}
```

---

### 3. Workflow — Predefined Multi-Step Operations

Complex operations (deploy, rollback, health-check, backup) are defined once and
invoked by name. The AI doesn't reinvent the wheel each time.

**MCP Tools:**
- `vssh_workflow(action: "list")` — list available workflows
- `vssh_workflow(action: "run", name: "deploy-canary", params: {...})`
- `vssh_workflow(action: "status", run_id: "...")` — check progress

**Example workflow definition:**

```yaml
name: deploy-canary
description: Deploy to canary host, verify, then roll out to fleet
params:
  - name: artifact_url
    required: true
  - name: canary_host
    default: web1

steps:
  - name: deploy-canary
    exec:
      host: "{{ canary_host }}"
      command: "deploy.sh {{ artifact_url }}"

  - name: health-check
    exec:
      host: "{{ canary_host }}"
      command: "health-check.sh"
    on_failure: rollback

  - name: deploy-fleet
    exec:
      hosts: ["web2", "web3"]
      command: "deploy.sh {{ artifact_url }}"
      parallel: true

  - name: rollback
    exec:
      host: "{{ canary_host }}"
      command: "rollback.sh"
    trigger: on_failure
```

---

### 4. Diff — Audit Log Change Summary

The AI can ask "what changed?" and get a human-readable summary instead of
parsing raw logs.

**MCP Tool:** `vssh_diff`

```json
{
  "host": "db1",
  "since": "2025-06-01T00:00:00Z",
  "until": "2025-06-28T00:00:00Z"
}
```

**Response:**

```json
{
  "summary": "15 exec actions by 2 keys over 27 days",
  "changes": [
    {
      "date": "2025-06-15",
      "key": "deploy@controller",
      "actions": ["apt update", "systemctl restart postgres"],
      "outcome": "success"
    },
    {
      "date": "2025-06-20",
      "key": "admin@laptop",
      "actions": ["pg_dump > backup.sql"],
      "outcome": "success"
    }
  ],
  "dangerous_blocked": 0,
  "policy_violations": 0
}
```

---

## Why This Matters

| Traditional SSH | vssh AI Runtime |
|-----------------|-----------------|
| Agent constructs shell commands by trial and error | Agent states intent, gets a verified plan |
| No memory — every session starts blank | Fleet memory persists across sessions |
| Complex operations require scripting | Workflows are first-class, invokable by name |
| Audit logs are raw text to parse | Structured diffs summarize what changed |
| Output is text to scrape | Output is typed evidence |

---

## MCP Tool Reference

| Tool | Actions | Purpose |
|------|---------|---------|
| `vssh_intent` | — | Natural language → command plan |
| `vssh_memory` | get, set, note, auto_note, find, ask | Per-node persistent context |
| `vssh_workflow` | list, run, status | Multi-step operation execution |
| `vssh_diff` | — | Audit log change summary |
| `vssh_exec` | plain, safe, routed, many | Command execution |
| `vssh_query` | facts, facts_many, rpc, rpc_many | Host facts and RPC |
| `vssh_job` | start, status, logs, cancel | Long-running jobs |
| `vssh_fleet` | doctor, status, list, hosts, state, setup | Fleet discovery |
| `vssh_transport` | tunnel, artifact_collect | Tunnels and artifacts |
| `vssh_route` | select, policy_check | Routing and policy |
| `vssh_config` | list, authorize_key, revoke_key, set_node, pin_node | Configuration (gated) |

---

## See Also

- [README](../README.md) — Quick start
- [MANUAL](MANUAL.md) — Full CLI and MCP reference
- [WHY_VSSH](WHY_VSSH.md) — SSH vs vssh positioning
