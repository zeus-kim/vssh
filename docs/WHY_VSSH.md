# Why vssh — an SSH alternative for fleets and AI agents

`vssh` is **not** "SSH with a shorter command." It is a different product
abstraction: a typed, key-only remote-execution daemon for private networks,
designed for the case where the operator is increasingly an **AI agent or
automation runtime**, not a human at a terminal.

If all you want is an interactive shell on a box that already runs `sshd`, use
OpenSSH — `vssh` deliberately does not wrap it. `vssh` exists for everything
around that: typed actions in, structured evidence out, with authentication,
policy, audit, long-running jobs, tunnels, and whole-fleet state as first-class
concepts.

## Why an SSH alternative at all?

SSH was designed in 1995 for a human typing into one machine. Modern fleet and
agent automation pushes against that design in five ways:

1. **No sshd should be required.** Standing up `sshd` + users + host keys + PAM
   on every node is a large, stateful surface. `vssh` needs only one static
   binary and private reachability.
2. **Output should be data, not a text stream.** An LLM (or any program) parsing
   a terminal stream is guessing. `vssh` returns `stdout/stderr/exit/duration/
   transport` as a typed envelope.
3. **Authorization should be finer than "shell = root."** SSH access is usually
   all-or-nothing. `vssh` has per-key capabilities, command allow/deny, path
   scoping, and rate limits.
4. **Actions should be auditable by construction.** `vssh` hash-chains an audit
   record per action, attributed to the authenticated key.
5. **Long-running and multi-node work should be first-class**, not tmux/nohup
   folklore. `vssh` has jobs, artifacts, fan-out, and capability routing.

## ssh vs vssh

| | OpenSSH | vssh |
|---|---|---|
| **Target prerequisite** | `sshd` + users + host keys + PAM | one static binary (`vssh server`), no sshd |
| **Auth** | passwords / keys via PAM | per-node Ed25519 challenge–response (VAUTH1); no shared secret |
| **Transport** | SSH wire protocol | TLS 1.3 + Ed25519 **raw-key pinning** (no CA) |
| **Host identity** | `known_hosts` TOFU | pinned name→key registry, **default-on**, anti-misroute |
| **Command result** | raw text stream | typed evidence: stdout/stderr/exit/duration/transport |
| **Authorization** | shell = full access | per-key **capabilities** + command **policy** (allow/deny, path, rate) |
| **Audit** | none built in | **hash-chained** audit record per action |
| **Long-running work** | tmux / nohup | `job_start/status/logs/cancel` + artifact collection |
| **Multi-node** | scripting around `ssh` | native fan-out + capability/health **routing** |
| **Addressing** | `user@ip` | node **name** + capability/tag/health |
| **AI / automation** | parse terminal text | **MCP-native** typed tools, evidence envelopes |
| **Fleet state** | none | **signed, timestamped** fleet snapshot, replicable to nodes |
| **Onboarding** | manual config per host | zero-touch auto-setup; one-command MCP attach |
| **Tunnels** | `-L/-R/-D` | `-L/-R/-D` over the same authenticated, audited channel |
| **Key rotation** | manual | `vssh keygen --rotate` + fleet `authorized_keys` tooling |

## What ships today (the proof, not just the pitch)

Every row above maps to shipped capability:

- **Key-only auth**: TLS 1.3 + Ed25519 VAUTH1; the daemon rejects any non-VAUTH1
  line. No shared secret or HMAC anywhere in the code.
- **Host-identity verification**: default-on pinned registry guards against a
  name resolving to the wrong daemon.
- **Policy + audit**: deny-first command classification, per-key caps, path
  scope, rate limits; tamper-evident hash-chained audit log.
- **Typed APIs**: `facts`, disk/mem/gpu/load/processes/logs/service/docker, and
  a consolidated `node_info` (version, uptime, arch, enforcement posture).
- **Jobs + artifacts + tunnels (-L/-R/-D, incl. an MCP `vssh_tunnel` tool).**
- **Fleet state**: a controller-signed, timestamped snapshot (inventory + node
  keys + caps + liveness), readable/verifiable anywhere and replicable to nodes.
- **Zero-touch onboarding**: setup auto-runs on first use; `vssh mcp-install`
  attaches vssh to an AI client in one command.
- **AI-driven config (gated)**: the agent can manage local config
  (authorize/revoke keys, set node/ip, pin host keys) behind an explicit opt-in.
- **Key rotation + recovery runbook** (`docs/KEY_ROTATION.md`).
- **Distribution**: checksum-verified one-line install, `pip install vssh`,
  releases across 7 Linux arches + macOS (FreeBSD experimental).

## Where OpenSSH is still the right tool

- A human opening an interactive terminal on a box that already runs `sshd`.
- Ad-hoc shell work, port forwards, and file copies to a known host.
- Anything where you do not want a second daemon or a typed control plane.

`vssh` should not compete there — wrapping `ssh user@ip` is not enough value.

## The product rule

Every `vssh` feature should pass at least one test: it works without sshd; it
produces structured evidence an agent can trust; it adds policy/control SSH does
not; it improves long-running or multi-node automation; or it exposes node/fleet
state as typed data instead of terminal text. If a feature is only "SSH, but
shorter," it does not belong.
