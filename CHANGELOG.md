# Changelog

Notable changes to **vssh**. Versioning is semantic-ish (`0.MINOR.PATCH`).

## [Unreleased]

### Added
- **File transfer over MCP** — `vssh_put`/`vssh_get` (in the `vssh_transport`
  group) upload/download files over the native protocol, checksum-verified and
  policy-gated. Previously only the CLI (`vssh put`/`get`) had this; the MCP gap
  is closed so an agent can move files directly. `vssh_deploy_binary`
  ships a binary (upload → atomic install → optional service restart → verify).
- **Fleet selectors** — `vssh intent`/`vssh_intent` `--target` accepts memory-backed
  selectors (`@gpu`, `@role:gpu`, `@tag:prod`, `@service:ollama`, `@all`) and
  comma-separated hosts, run in parallel with per-node results. One request fans
  out across the fleet instead of a shell loop over ssh.
- **Auto-discovery** — `vssh memory discover [--apply]` infers each node's
  role/services/tags from what it actually runs (GPUs, running units, listening
  ports, containers, largest data volume). Rule-based, no LLM. Wired into
  `refresh_fleet_state.sh` so the selectors stay current with no hand-maintenance.
- **Fleet health** — `vssh fleet-health`/`vssh_fleet_health` probes every node in
  parallel and reports a worst-first summary: down nodes, disk pressure, high
  per-core load, memory pressure, and failed systemd units, with the reason per
  node.
- **Persistent MUX exec pool** — the long-lived `vssh mcp` server reuses one
  authenticated connection per host, so an agent's repeated calls skip the
  TCP+TLS+VAUTH handshake (~1.4× faster). No daemon change; falls back to a
  one-shot connection on any daemon without MUX.

### Performance
- **TLS 1.3 session resumption** — daemon issues resumption tickets and the client
  caches them, so repeated connections from one process skip the full asymmetric
  handshake. VAUTH1 still authenticates every connection.
- **Larger transfer buffers** — file streaming uses a 256 KiB copy buffer so TLS
  batches more per syscall on high-bandwidth·high-latency links.

### Fixed
- **diff false positives** — piped `... | sed 's/x/y/'` and `2>/dev/null` are no
  longer misreported as file edits/writes; only in-place (`-i`) sed/perl edits
  that name a file count as changes.
- **MUX double-execution** — a reused session that times out mid-command is no
  longer retried (the command may have run); only idle-closed sessions retry.
- **Empty-selector footgun** — `@`, `@role:`, `@tag:`, `@service:` now error
  instead of silently matching the entire fleet.
- **Cross-platform commands** — built-in workflow/intent `ps` and flag parsing
  (`--flag value` and `--flag=value`) work on macOS and Linux alike.

### Changed
- **Go toolchain 1.26.5** — patches `GO-2026-5856` in the standard-library
  `crypto/tls`.
- **Deploy transfer uses `scp -O`** — the legacy protocol so Synology/old-SFTP
  nodes accept transfers; a no-op elsewhere.

### Documentation
- **AI Runtime documentation** — new `docs/AI_RUNTIME.md` explaining Memory,
  Intent, Workflow, and Diff capabilities.
- **README positioning** — reframed as "AI Execution Runtime" with architecture
  diagram showing Intent → Policy → Execution → Evidence → Audit flow.
- **Examples directory** — sample files for fleet inventory, workflows, memory,
  and structured execution output.

### Changed
- **MCP tool documentation expanded** — README now highlights AI-native
  capabilities (Memory, Intent, Workflow, Diff) alongside core execution tools.

## [0.7.48] — transport robustness

### Changed
- **Overlay failover everywhere** — every non-interactive resolve path
  (`facts`, fan-out `rpc`/`exec`, `bench`, `cp`/file transfer, MCP `facts`/exec)
  now uses the concurrent reachability-probing resolver instead of blindly
  dialing the first candidate. A node reachable on either Tailscale **or** wire
  (WireGuard) is found automatically when its preferred overlay IP is stale.
- **Cached Tailscale status** — `tailscale status --json` is read through a
  short-TTL (5s) cache shared across self-info, per-host IP lookup, and peer
  discovery, so a fleet-wide loop no longer spawns one subprocess per node while
  still refreshing for long-lived daemons.

## [0.7.47] — initial public release

The first public release of vssh. Summary of the current state.

### Security
- **Key-only auth** — per-node Ed25519 challenge–response (VAUTH1); no shared
  secret or HMAC anywhere. Challenge signatures are domain-separated.
- **Transport** — TLS 1.3 with raw-public-key pinning (not PKI). `VSSH_REQUIRE_TLS`
  refuses plaintext; `VSSH_REQUIRE_VAUTH` refuses anonymous/non-VAUTH1 auth.
- **Host-identity verification** (default-on) via a trusted name→key registry,
  defending against name→wrong-host misroutes.
- **Authorization** — per-key capabilities (`exec` / `file` / `rpc` / `forward` /
  `shell`) plus an opt-in per-key command **policy** (allowlist, path scope,
  forward targets, rate limit) — deny-first, anchored, fail-closed, and enforced
  on every verb including typed RPC.
- The safe-exec path flags download-piped-to-shell (`curl … | bash`) and
  credential-file reads (`/etc/shadow`, `~/.ssh/id_*`, …) for approval.
- **Hash-chained audit log** — every action attributed to a key, with transport
  and matched policy rule; tamper-evident (`vssh audit-verify`).
- Internal security review performed; see `docs/SECURITY_AUDIT_PACKAGE.md`.
  Independent third-party review recommended.

### Capabilities
- Typed remote exec returning structured evidence (stdout/stderr/exit/duration/
  transport); parallel fan-out (`run-many`, `facts-many`, `rpc-many`).
- File transfer (`put`/`get`), atomic `deploy-binary`, port-forward tunnels,
  long-running jobs, artifact collection, interactive shell.
- Typed RPCs (node facts, disk/memory/load, services, …).
- Signed, replicable fleet-state snapshot; fleet memory; rule-based intent;
  predefined workflows; audit-diff.

### MCP / AI
- An **MCP server built into the binary**; grouped action-tools minimise the
  per-session token cost of the tool surface.
- One-command attach (`vssh mcp-install`) for **Claude, Claude Code, Cursor,
  Gemini (Google AI Studio), and Codex**.
- Zero-touch host-identity auto-provisioning on first operational call.

### Robustness & ops
- Accept-loop backoff (never busy-spins on a persistent error), structured
  daemon log, periodic self-health.
- One-line checksum-verified installer; `pip install vssh`; key rotation tooling.

### Platforms
- Linux (`amd64`/`arm64`/`arm`/`386`/`riscv64`/`ppc64le`/`s390x`), macOS;
  FreeBSD is an experimental build.

Licensed under Apache-2.0.
