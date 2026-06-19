# vssh — Development Roadmap (as of v0.7.47, 2026-06-17)

Direction (decided 2026-06-13): **P0 security validation + P1 onboarding friction
come before new features.** Priorities below are ordered by commercialization
leverage ("get someone other than the author to adopt it").

## Status 2026-06-17 (v0.7.47)
- **Transport hardening DONE**: `VSSH_REQUIRE_TLS=1` flipped fleet-wide (canary →
  fleet → controller); daemon refuses plaintext. `VSSH_REQUIRE_VAUTH=1` already
  live. Verified on all 14 nodes + controller.
- **Security audit (internal/AI) DONE**: adversarial source review found + fixed
  one HIGH — RPC dispatch enforced only the `rpc` cap, letting a `caps=rpc` key
  reach `file_*`/`job_start` and bypass file/exec caps + policy (v0.7.45,
  deployed). Defense-in-depth: VAUTH1 signature domain separation (v0.7.47),
  `INFO` verb gated, classifier fuzzed (no further findings). External audit
  still recommended.
- **Robustness DONE** (v0.7.46): accept-loop backoff (no busy-spin on fd
  exhaustion), structured `~/.vssh/daemon.log`, self-health loop.
- **MCP**: grouped action-tools default (~38→12 surface, token cut); one-command
  attach for Claude / Claude Code / Cursor / Gemini / Codex; fleet memory,
  intent, workflows, audit-diff.
- **Controllers**: m1 primary + **d2 promoted as secondary controller** (key
  authorized fleet-wide + host-key registry) — stationary brain, no laptop needed.
- Fleet: 14 online nodes + controller all on **0.7.47**, key-only, REQUIRE_TLS/VAUTH.
- Remaining: external audit; migration (new account/repos, PyPI); Windows port.

## Status 2026-06-15 (v0.7.42)
- **Distribution (was P1): DONE** — one-line `install.sh` (checksum-verified),
  `pip install vssh` (CLI binary downloader + SDK), GitHub Releases via CI.
- **Key-only auth (was P4): DONE** — daemon authenticates with per-node Ed25519
  (VAUTH1) only; the legacy shared-secret/HMAC path was removed from the code and
  `VSSH_SECRET` was stripped from every node unit + the controller. No shared
  secret exists anywhere. `scripts/enroll.sh` onboards key-only.
- Fleet: 14 nodes + m1 controller all on 0.7.38, secret-free. Public: GitHub
  release v0.7.38 (Latest), PyPI `vssh` 4.3.1.
- v0.7.39 (2026-06-14): ALL dead shared-secret machinery removed from code +
  scripts + docs (the daemon was already VAUTH1-only). `getSecret()` returns "";
  `vssh setup` no longer passes `VSSH_SECRET=`; `vssh doctor` now reports a
  positive `auth_model` check; `deploy_fleet.sh` and the other fleet scripts no
  longer require `VSSH_SECRET`. No auth behavior change (binary redeploy optional).
- v0.7.40 (2026-06-14): **P2 zero-touch onboarding DONE** — MCP auto-runs
  `vssh_setup` on the first operational tool call (marker-gated; opt-out via
  `VSSH_NO_AUTOSETUP`), so host-identity verification self-provisions.
- P3 DONE: `docs/SECURITY_AUDIT_PACKAGE.md` refreshed to the key-only model and
  made handoff-ready (verified enforcement posture; auto-setup path in scope).
- P5 DONE (v0.7.41): `vssh_tunnel` MCP tool (start/list/stop detached forwards);
  expanded release to 9 arches (linux arm/386/riscv64/ppc64le/s390x added); static
  landing site + Pages workflow; `node_info` read RPC. Done since: FreeBSD experimental build (pty_freebsd), and key-rotation
  tooling (`vssh keygen --rotate` + `scripts/rotate_authorized_key.sh` +
  `docs/KEY_ROTATION.md`). Future ideas: full Windows port (ConPTY).

## ✅ Done (this line)
- Transport: TLS 1.3 (stdlib) + Ed25519 raw-key pinning, first-byte sniff,
  `VSSH_REQUIRE_TLS` kill-switch (0.7.25/0.7.26).
- Host-identity verification DEFAULT-ON (0.7.33) via trusted `node_keys` registry
  (loopback handshakes) + resolver excludes local/self IPs from remote candidates
  (0.7.35) — two layers against name->wrong-host misroute.
- P1b whitelist COMPLETE (0.7.27–0.7.32): per-key exec allow/deny + file_paths +
  fwd_targets + rate (daemon, deny-first/fail-closed/anchored/audited) + MCP
  danger_preapproved auto-approve (policy_check RPC). All opt-in (`policy=` tag).
- P1a onboarding: `scripts/enroll.sh` (one-command idempotent node onboarding).
- MCP self-bootstrap: `vssh_setup` tool (models self-configure on first connect);
  cross-client compatible (Claude/Cursor/Codex/AI Studio) — see VSSH_MCP_CLIENTS.md.
- Tooling: audit_transport_scan.sh (gate), build_node_registry.sh, enable_require_tls.sh.
- External-audit package: docs/SECURITY_AUDIT_PACKAGE.md.

## Priority 1 — Distribution pipeline (BUILT + VERIFIED 0.7.36; publish pending)
Local pipeline complete and tested on m1 (CHANGELOG 2026-06-14):
- `make release` builds 4-arch binaries + `checksums.txt`; VERSION single-sourced
  from `cmd/vssh/main.go` (no more drift). `install.sh` fetches release assets with
  SHA-256 verification (fail-closed) + `VSSH_VERSION` pinning + `INSTALL_DIR`.
- `pip install vssh` = unified wheel: CLI binary-downloader (console-script, stdlib,
  checksum-verified, cached to `~/.vssh/bin`) + importable `from vssh import VSSH`.
  Version single-sourced from `src/vssh/_version.py`. Verified happy-path + tamper
  rejection + `VSSH_BIN` override in a fresh venv.
- REMAINING (needs credentials): push tag
  `v0.7.36`, create the GitHub release + upload `dist/vssh-{linux,darwin}-{amd64,
  arm64}` + `checksums.txt`, then `twine upload dist/vssh-0.7.36*`. After the first
  GitHub release exists, `install.sh` and the pip wheel are live end-to-end.
- Optional: brew formula (after the GitHub release exists).

## Priority 2 — True zero-touch onboarding
- Auto-trigger `vssh_setup` on first exec (no explicit tool call needed).
- Fold `build_node_registry` into enroll/deploy so the trust registry self-maintains.
- Script the m1 controller binary swap safely (avoid the codesign-wedge: build ->
  xattr -c + re-sign -> atomic mv -> launchctl bootout/bootstrap, never cp/kickstart).

## Priority 3 — Close the security gates (time / external / operational)
- §5.3 REQUIRE_TLS: after Claude Desktop restart -> 7-day plaintext-free window
  (daily scan `vssh-tls-gate-scan`) -> `scripts/enable_require_tls.sh` flip
  (+ m1 coordinator unit).
- External security audit: engage a reviewer with docs/SECURITY_AUDIT_PACKAGE.md.
- d2 host recovery (host down — needs provider console; vssh auto-re-registers on return).

## Priority 4 — Production hardening
- Assign least-privilege policy templates (backup/ci/deploy/readonly) to real agent
  keys (not just the demo) — the "auditable AI ops" story in practice.
- DONE: key rotation tooling + recovery runbook (`docs/KEY_ROTATION.md`); legacy shared `VSSH_SECRET`/HMAC fully removed (v0.7.39).
  once REQUIRE_VAUTH/REQUIRE_TLS are universal.

## Priority 5 — Expansion
- Broader OS/arch, docs/landing site, `vssh_tunnel` MCP tool (P2, after transport),
  additional RPC verbs.

## Open investigations / notes
- Daemon TLS key = `/etc/vssh/vssh_id` when `$HOME` unset (systemd); tooling reading
  `~/.vssh` sees a different key. `node_keys` registry uses the daemon's real
  loopback-presented key, so verification is correct. Consider making vsshConfigDir
  deterministic in a future release (disruptive — would re-key).
- Some `~/.vssh/config` IPs are stale vs live tailscale; resolver prefers live
  tailscale + probes candidates, and now excludes self-IPs.
