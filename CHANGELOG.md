# Changelog

Notable changes to **vssh**. Versioning is semantic-ish (`0.MINOR.PATCH`).

## [Unreleased]

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
