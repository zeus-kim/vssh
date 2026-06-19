# vssh — External Security Audit Package

Prepared 2026-06-13, refreshed 2026-06-17 for **v0.7.47**. This is the scoping +
evidence package for an external/adversarial review of vssh's security-critical
paths. It points reviewers at exact
code, states the threat model, the crypto choices and why, the residual risks,
and how to reproduce every validation.

**Auth model: key-only.** Authentication is per-node Ed25519 challenge–response
(VAUTH1); there is no shared secret or HMAC anywhere in the codebase (the legacy
`VSSH_SECRET`/HMAC path and all secret sourcing were removed in v0.7.39). **Current
enforcement posture (verified 2026-06-14):** `VSSH_REQUIRE_VAUTH=1` is set
fleet-wide — controller plus linux (systemd drop-in) and darwin (launchd plist)
nodes — so key authentication is mandatory and the daemon rejects any non-VAUTH1
auth line. **`VSSH_REQUIRE_TLS=1` is now enforced fleet-wide too** (verified
2026-06-17), so the daemon refuses plaintext — on-wire confidentiality no longer
relies on the WireGuard underlay alone (see §5).

**Audit history (internal/AI, 2026-06-17).** An adversarial source-level review
of the paths below was performed. It found and fixed one **HIGH**: the native RPC
dispatch enforced only the outer `rpc` capability, letting a `caps=rpc` key reach
`file_read`/`file_write`/`job_start`/`restart_service` and bypass the file/exec
capabilities + the per-key policy (fixed v0.7.45, daemon-side, deployed
fleet-wide; test `internal/server/rpc_authz_test.go`). Defense-in-depth
follow-ups: VAUTH1 signature domain separation (v0.7.47, dual-accept rollout) and
gating the `INFO` verb. The file-path and command-classifier paths were reviewed
(incl. fuzzing, 266k execs) with no further findings. An **independent
third-party** review is still recommended and is the purpose of this package.

## 1. What vssh is (security model in one paragraph)
A mesh-native remote-execution daemon (`vssh server`) + client/MCP, replacing ad
hoc SSH for fleet/agent automation. Identity = per-node Ed25519 keys. Transport =
TLS 1.3 (Go stdlib) with raw-public-key pinning (no PKI). Authorization = per-key
capabilities (`caps=exec,file,rpc,shell,forward`) plus an opt-in per-key policy
(command allow/deny, path scope, forward targets, rate, danger pre-approval).
Every command is hash-chain audited with the authenticated key, transport
(tls|plain), and the matched policy rule.

## 2. Audit scope (priority order)
1. **Transport (VTLS1)** — `internal/server/tlsident.go`: `ServerTLSConfig`,
   `ClientTLSConfig` + `VerifyPeerCertificate` (InsecureSkipVerify + raw-key pin),
   `PeerPubB64`, `IdentityCertificate` (self-signed Ed25519 cert from the node
   identity). First-byte 0x16 sniff in `internal/server/server.go handleConnection`.
   Kill-switch `VSSH_REQUIRE_TLS`. Question for reviewers: channel binding between
   the TLS layer and the in-band VAUTH1 identity (we check cert key == VAUTH1 key).
2. **Peer authentication** — `internal/server/identity.go`: `VerifyChallenge`
   (Ed25519 challenge–response, `VAUTH1`), `KeyCapabilities`/`KeyName`/`KeyPolicy`
   parsing of `authorized_keys`. Downgrade resistance: client legacy-HMAC fallback
   was removed (F3); pinned-key mismatch is a hard fail, never a plaintext fallback.
3. **Host-identity verification** — `internal/server/transfer.go dialAuth` +
   `NodeKey` registry (`internal/server/identity.go`, built by
   `scripts/build_node_registry.sh` via loopback handshakes). Default-ON
   (opt-out `VSSH_NO_HOSTKEY_VERIFY=1`). Defends against name→wrong-host misroute
   where an IP-keyed pin would otherwise match the wrong daemon. As of v0.7.40
   the registry also auto-provisions on the first operational MCP tool call
   (`callMCPTool` → `autoSetupOnce` → `toolSetup` loopback handshakes;
   marker-gated at `~/.vssh/.autosetup_done`, opt-out `VSSH_NO_AUTOSETUP`), so a
   fresh controller self-bootstraps host-identity verification with no manual
   step. Reviewers should confirm the auto-setup path cannot be induced to pin a
   wrong key (it handshakes each peer's own loopback daemon).
4. **Authorization / policy engine** — `internal/server/policy.go`: deny-first →
   danger_preapproved → exec_allow → no-match-refuse; anchored (`^...$`) full-string
   matching vs metachar/newline smuggling; `file_paths` with symlink/.. resolution;
   `fwd_targets` (host/CIDR:port); per-key `rate`; **fail-closed** when a tagged
   policy file is missing. MCP advisory auto-approve via `policy_check` RPC
   (`HandleRPCCommand`), daemon remains authoritative. Enforced on exec (EXEJ/mux/
   legacy EXE), file verbs (PUT/GET path-scoped; others fail-closed for policied
   keys), and FWD.
5. **Audit chain** — `internal/server/transfer.go auditLog`: per-record hash chain
   (`prev`), key attribution, `transport`, `policy_rule`/`preapproved`. Verify the
   chain is tamper-evident and that no exec/forward path skips it.
6. **Tunnels / multiplexing** — `internal/server/fmux.go`, `forward.go`: FMUX
   stream auth (one handshake, many streams), reverse-forward control channel.
   Confirm per-stream authorization and no unauthenticated stream injection.
7. **Kill-switch bypass hunt** — grep every accept/dial path that should honor
   `VSSH_REQUIRE_VAUTH` / `VSSH_REQUIRE_TLS` / host-identity; confirm none can be
   forced to plaintext or skipped (FMUX/RCONN/RPC included).

## 3. Threat model (from SECURITY_TRANSPORT_MIGRATION.md §2)
Adversaries: A1 on-path inside the mesh (compromised peer terminates WireGuard,
sees vssh plaintext), A2 impersonating daemon (DNS/hosts drift, ARP, :48291
squatter), A3 stolen key. Findings driving the design: F1 no post-auth
encryption/MAC (pre-VTLS), F2 no server authentication/channel binding, F3
client-side legacy downgrade, F4 no signature domain separation. Plus the
resolution-misroute class (name→wrong host) addressed by host-identity verify.

## 4. Crypto choices & rationale
TLS 1.3 only (`MinVersion: VersionTLS13`), Go stdlib `crypto/tls` (fuzzed,
maintained by the Go security team) — the answer to "who wrote the crypto?" is
"stdlib", not us. Ed25519 self-signed certs from the existing node identity; trust
is the raw-key pin (authorized_keys / known_hosts), not WebPKI. No resumption
tickets, no 0-RTT. ALPN `vssh/1`. No hand-rolled symmetric crypto.

## 5. Residual risks / known limitations (be explicit with reviewers)
- Transport confidentiality: `VSSH_REQUIRE_VAUTH=1` is live fleet-wide (key auth
  is mandatory — no anonymous and no shared-secret/HMAC path exists), but
  `VSSH_REQUIRE_TLS=1` is now ALSO enforced fleet-wide (verified 2026-06-17): the
  daemon refuses plaintext, so on-wire confidentiality no longer depends on the
  WireGuard underlay alone. (Historical plaintext-auth records predate the flip;
  measure a window with `scripts/audit_transport_scan.sh SINCE=<ts>`.)
- Host-identity verification only enforces nodes present in the local `node_keys`
  registry; unknown nodes skip (fail-open by absence). Registry must be refreshed
  after key changes (`build_node_registry.sh`).
- Identity path nuance: daemon TLS key = `/etc/vssh/vssh_id` when `$HOME` is unset
  (systemd); tooling that reads `~/.vssh` sees a different key. Documented; the
  registry uses the daemon's actual loopback-presented key.
- Node-to-node native reachability is not guaranteed by tailnet ACLs (controller
  reaches all; nodes may not reach each other). Regex command whitelisting of
  shell strings is best-effort — caps verb + path scope are the hard floor.
- No shared secret exists. Authentication is per-node Ed25519 (VAUTH1) only and
  the daemon rejects any non-VAUTH1 auth line; the legacy `VSSH_SECRET`/HMAC path
  was removed from the code entirely (v0.7.39).

## 6. Test coverage & reproduction
- `go vet ./... && go test ./...` (unit + in-process e2e). Key tests:
  `internal/server/policy_test.go` (EvalExec deny-first/anchoring/no-match,
  PathAllowed escape, fwdTargetMatch, rateExceeded, template compile+anchor),
  `policy_e2e_test.go` (real loopback daemon: allow/deny/smuggle/danger/mux/
  fail-closed + audit rule ids; `TestHostIdentityVerification`; `TestPolicyCheckRPC`).
- Fleet conformance: `test/agent_suite.sh` (four-laws contract), `/tmp/vtls_test.sh`
  (TLS matrix), `scripts/audit_transport_scan.sh` (plaintext-auth gate).
- Live validations performed 2026-06-13: TLS AUTH_OK fleet-wide; host-identity
  default-ON blocks a wrong-key/misrouted host and allows legit nodes; policy
  enforcement on a live daemon (d1) with the real controller key (allow/deny +
  audit attribution).

## 7. Out of scope (recorded decisions)
Interactive TTY over MCP; generic (non-mesh) SSH server emulation; the
`vssh_tunnel` MCP tool (planned P5 — the underlying forward/FMUX primitives in
§2.6 are in scope, but the MCP surface is not built yet). Note: zero-touch
onboarding (P2) shipped in v0.7.40 and its auto-setup path IS in scope (§2.3).
See docs/SECURITY_TRANSPORT_MIGRATION.md §7.
