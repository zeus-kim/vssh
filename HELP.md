# vssh — help & usage

`vssh` is an sshd-free, AI-native remote execution daemon for private networks.
It runs commands, transfers files, and manages jobs on remote nodes over TLS 1.3
with Ed25519 key authentication, returning structured execution evidence.

- No `sshd` required on the target
- Built-in PTY, typed RPC, file transfer, and long-running jobs
- TLS 1.3 + Ed25519 key auth (legacy shared-secret path for bootstrap)
- Node-name routing over Tailscale, Wire/WireGuard, LAN, or configured addresses
- A built-in MCP server (`vssh mcp`) for AI agents

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/zeus-kim/vssh/main/install.sh | bash
# or
pip install vssh
```

## Quick start

```bash
# On the target node (no shared secret — key-authenticated)
vssh server                     # listens on :48291

# On the client: print your key, add it to the server's ~/.vssh/authorized_keys
vssh pubkey
vssh run web1 "uptime"          # structured result
vssh web1                       # interactive shell
vssh doctor --json              # diagnose before AI/MCP use
```

## Command reference

### Execution
```bash
vssh <node>                  # interactive PTY shell
vssh shell <node>            # interactive PTY shell
vssh run <node> <command>    # run a command (structured result)
vssh exec <node> <command>   # alias for run
vssh run-many <n1,n2> <cmd>  # run across comma-separated nodes
vssh run-batch <node> ...    # run several commands on one session
```

### Typed APIs
```bash
vssh rpc <node> <method> [json]        # call a typed daemon RPC
vssh rpc-many <nodes> <method> [json]  # RPC across nodes
vssh facts <node>                      # typed host facts
vssh facts-many <nodes>                # facts across nodes
```

### Jobs (long-running)
```bash
vssh job-start <node> <command>   # start a background job
vssh job-status <node> <id>       # job status
vssh job-logs <node> <id>         # job logs
vssh job-cancel <node> <id>       # cancel a job
vssh artifact-collect <node>      # collect output artifacts
```

### Files
```bash
vssh put <local> <node:path>   # upload
vssh get <node:path> <local>   # download
```

### Fleet & ops
```bash
vssh                 # dashboard (default)
vssh status          # dashboard
vssh list            # list known nodes
vssh doctor [--json] # diagnose binary, secret, config, peers, MCP readiness
vssh deploy <node>   # atomic binary install + restart + verify
vssh server          # run the daemon
vssh mcp             # run the MCP server (for AI agents)
vssh setup           # first-run self-configuration
vssh version         # show version
vssh help            # full help
```

## Discovery

`vssh` finds nodes from, in order: Tailscale, the config file
(`~/.vssh/servers.json`), and a local cache. See [Peer Discovery](docs/PEER_DISCOVERY.md). You can always address a node
by an explicit IP as well.

## Configuration

### Node inventory — `~/.vssh/servers.json`
```json
{
  "web1": { "ip": "192.0.2.10", "user": "deploy" },
  "db1":  { "ip": "192.0.2.20", "user": "postgres" }
}
```

### Per-host users — `~/.wire/users.json` (root: `/etc/wire/users.json`)
```json
{ "web1": "deploy", "db1": "postgres" }
```

> Keep real inventory, hostnames, VPN IPs, and secrets **out of source control**.

## Environment variables

| Variable | Description |
|----------|-------------|
| `VSSH_PORT` | Daemon listen port (default **48291**). |
| `VSSH_REQUIRE_TLS` | `1`/`true`/`yes` = refuse non-TLS (plaintext) connections; matches the state `node_info` reports. |
| `VSSH_NO_HOSTKEY_VERIFY` | `1` = opt out of host-identity verification (not recommended). |
| `VSSH_BIN` / `VSSH_VERSION` / `VSSH_HOME` | pip-wrapper/installer overrides (see README). |

## Security

`vssh server` authenticates peers with **TLS 1.3 + Ed25519 keys (VAUTH1)** only —
no shared secret. Authorize clients via `~/.vssh/authorized_keys`. Commands can be
gated with optional per-key policy. The VPN encrypts the tunnel but does **not**
replace vssh authentication. Full details and
a hardening checklist: [SECURITY.md](SECURITY.md).

## More

- CLI + MCP usage guide: [docs/MANUAL.md](docs/MANUAL.md)
- Python SDK: [docs/PYTHON_SDK.md](docs/PYTHON_SDK.md)
- Why vssh: [docs/WHY_VSSH.md](docs/WHY_VSSH.md)
