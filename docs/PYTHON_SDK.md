# Python SDK

`pip install vssh` gives you the `vssh` CLI **and** an importable Python client.
The SDK is a thin wrapper over the installed `vssh` binary — it shells out to the
CLI rather than reimplementing the protocol, so it inherits the same transport
(TLS 1.3 + Ed25519), authentication, and policy.

## Install

```bash
pip install vssh
```

On first use the CLI binary is downloaded for your platform, checksum-verified,
and cached under `~/.vssh/bin`. To point at an existing binary instead, set
`VSSH_BIN=/path/to/vssh`.

## Quick start

```python
from vssh import VSSH

client = VSSH()   # key-only auth (per-node Ed25519 VAUTH1); no secret needed

res = client.exec("web1", "uptime")
print(res.exit_code, res.stdout)
```

`VSSH(binary=..., timeout=...)` — all optional. vssh authenticates with per-node
Ed25519 keys (VAUTH1); there is no shared secret to configure.

## Methods

| Method | Returns | Purpose |
|--------|---------|---------|
| `exec(target, command, *, timeout=None)` | `ExecResult` | Run one command |
| `exec_many(targets, command, *, timeout=None)` | `list[MultiExecResult]` | Run on many nodes |
| `rpc(target, method, params=None, *, timeout=None)` | `dict` | Typed daemon RPC |
| `rpc_many(targets, method, params=None, *, timeout=None)` | `list[MultiRPCResult]` | RPC on many nodes |
| `facts(target, *, timeout=None)` | `dict` | Typed host facts |
| `facts_many(targets, *, timeout=None)` | `dict` | Facts on many nodes |
| `job_start(target, command, *, timeout=None)` | `dict` | Start a background job |
| `job_status(target, job_id, *, timeout=None)` | `dict` | Job status |
| `job_logs(target, job_id, *, timeout=None)` | `dict` | Job logs |
| `job_cancel(target, job_id, *, timeout=None)` | `dict` | Cancel a job |
| `artifact_collect(target, path, *, max_bytes=None, timeout=None)` | `dict` | Collect output artifacts |
| `doctor(*, timeout=None)` | `dict` | Diagnose local setup |

## `ExecResult`

```python
ExecResult(
    success: bool,
    command: str,
    stdout: str,
    stderr: str,
    exit_code: int,
    duration_ms: int,
    error: str = "",
)
```

## Examples

```python
from vssh import VSSH, VSSHError

client = VSSH()   # key-only auth (per-node Ed25519 VAUTH1); no secret to configure

# Fan out across nodes -> list[MultiExecResult]
for r in client.exec_many(["web1", "db1"], "df -h /"):
    if r.result:
        print(r.target, r.result.exit_code, r.result.stdout)
    else:
        print(r.target, "error:", r.error)

# Typed facts for routing decisions
facts = client.facts("gpu1")

# Long-running job
job = client.job_start("web1", "backup.sh")
status = client.job_status("web1", job["job_id"])

try:
    client.exec("web1", "false")
except VSSHError as e:
    print("SDK error:", e)
```

`VSSHError` is raised when the CLI cannot complete an operation (e.g. the binary
is missing or the daemon is unreachable). A command that runs but exits non-zero
is **not** an error — inspect `ExecResult.exit_code`.
