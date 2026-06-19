# vssh — Transport Security Migration Design (P0)

> **Status: historical.** This records design/migration that is now complete —
> the legacy shared-secret/HMAC path was fully removed in v0.7.39 and the
> daemon accepts only per-node Ed25519 (VAUTH1). Present-tense HMAC/dual-auth
> references below describe the *pre-migration* state.


_Written 2026-06-13 (at v0.7.24). Status: DESIGN — no code yet, by decision._

This is the P0 transport item: move vssh from
a bespoke plaintext challenge–response to a standard, reviewed encrypted
transport. It also contains the threat model for the P1(b) unattended-automation
whitelist, because that feature widens the attack surface and must be designed
against the same adversary.

Companion docs: `SECURITY_AUDIT_PACKAGE.md` (scope + evidence) and
`ROADMAP.md` (current priorities).

---

## 1. What the wire looks like today (v0.7.24, verified in code)

One TCP connection to `:2222`. Everything below is **plaintext**:

```
C: VAUTH1 <client_pub_b64>\n
S: CHALLENGE <nonce_b64>\n          (32 random bytes)
C: SIG <ed25519_sig(nonce)>\n
S: AUTH_OK\n
C: <verb line> ...                  (EXEC / FMUX / GET / PUT / RPC / ...)
```

- `internal/server/server.go handleConnection()`: reads the auth line, runs the
  challenge–response (`identity.go VerifyChallenge`), then dispatches verbs on
  the same raw conn. Legacy path: a `ts:hmac` token line, refused when
  `VSSH_REQUIRE_VAUTH=1`.
- `internal/server/transfer.go dialAuth()`: client prefers VAUTH1 and **falls
  back to the legacy HMAC token on a fresh connection whenever VAUTH1 is
  rejected or unsupported**.
- Confidentiality/integrity today are delegated entirely to the underlying
  tailnet/WireGuard mesh.

## 2. Threat model — what is actually wrong

Adversary classes:
- **A1 — on-path inside the mesh**: a compromised mesh node, a misrouted
  packet path, or anyone who can MITM 10.98.x / 100.x traffic. WireGuard
  protects node↔node links, but a compromised *peer* terminates WireGuard and
  sees vssh plaintext; mesh membership ≠ trustworthy.
- **A2 — impersonating daemon**: anything that can get a client to connect to
  it (DNS/hosts drift — we already saw stale `/etc/hosts` WIRE blocks; ARP;
  a compromised node squatting :2222).
- **A3 — stolen key / leaked credential** (driver for caps and the whitelist).

Findings (ordered by severity):

| # | Finding | Class | Consequence |
|---|---------|-------|-------------|
| F1 | **No transport encryption/MAC after AUTH_OK.** | A1 | Command lines, outputs, file contents, forwarded tunnel bytes readable and **injectable** (session hijack: attacker injects verbs into an authenticated conn). |
| F2 | **No server authentication, no channel binding.** Client signs the raw nonce; signature is not bound to server identity, session, or transcript. | A2 | Full relay MITM: attacker accepts the client, relays CHALLENGE/SIG to the real daemon, and owns the resulting authenticated session. |
| F3 | **Client-side downgrade.** `dialAuth` falls back to legacy HMAC against any peer that rejects VAUTH1 — i.e. it *sends a valid 30s-replayable token to an unauthenticated peer that just refused our strong auth*. `VSSH_REQUIRE_VAUTH` is daemon-side only; nothing stops the client downgrade. | A2 | Token harvesting + replay within the window against any daemon still accepting legacy auth. |
| F4 | **No domain separation in signatures.** The Ed25519 signature is over 32 raw bytes; any other protocol that ever signs attacker-chosen 32-byte blobs with the same key enables cross-protocol forgery. | A2 | Low today (key only used here), but free to fix. |
| F5 | Nonce is random but **single-shot only by code path**, not tracked: a nonce is never reused because it's generated per conn, fine — but there is no transcript hash, so F2 dominates. | A2 | Covered by F2 fix. |

Conclusion: fixing F1–F3 with hand-rolled crypto would be repeating the
original mistake. Wrap the whole protocol in a reviewed channel.

## 3. Options considered

| | TLS 1.3 (Go stdlib `crypto/tls`) | Noise (e.g. `flynn/noise`, IK/XX) | ssh transport (`golang.org/x/crypto/ssh`) |
|---|---|---|---|
| Implementation review maturity | Highest available in Go; stdlib, fuzzed, maintained by the Go security team | Library is solid but far less reviewed than stdlib TLS; we assemble the handshake pattern ourselves (= new foot-guns) | Mature, but adopting it wholesale means becoming an ssh implementation — the thing vssh deliberately is not |
| Fits Ed25519 identities we already have | Yes — Ed25519 self-signed certs; raw key extracted and checked against `authorized_keys` | Yes (static keys are curve25519 though — would need new keys or conversion) | Yes |
| PFS / AEAD / downgrade protection | Built in (1.3 only, no legacy suites) | Built in for the chosen pattern | Built in |
| New dependency | none | one | one (x/crypto) |
| Extra RTT | 1-RTT handshake | 1-RTT (IK) | multiple |
| ALPN / version agility | yes | manual | n/a |

**Decision: TLS 1.3, stdlib, `MinVersion: tls.VersionTLS13`, both sides
presenting Ed25519 self-signed certs derived from the existing `~/.vssh/vssh_id`
key.** Noise is the fallback if cert plumbing turns out heavier than expected,
but stdlib TLS is the most defensible answer to "is your crypto reviewed?" —
which is the whole point of P0.

## 4. Target design (VTLS1)

### 4.1 Identity → certificate
- Each node already has an Ed25519 keypair (`~/.vssh/vssh_id`). At startup
  (daemon) / first dial (client), build an in-memory **self-signed X.509 cert**
  over that key (long validity, CN = node name; the cert is a container, not a
  trust statement).
- **Trust = raw public key, not PKI.** Both sides set
  `InsecureSkipVerify: true` + a custom `VerifyPeerCertificate` that extracts
  the Ed25519 public key and checks it:
  - **Daemon checking client**: key ∈ `authorized_keys` → caps/key_name exactly
    as today (`KeyCapabilities`, `KeyName`). Client identity comes from the TLS
    client cert, so the in-band `VAUTH1` line becomes redundant (kept during
    migration, see §5).
  - **Client checking daemon**: key must match `~/.vssh/known_hosts`
    (`<host> <pubB64>` lines, written by `cross_authorize_fleet.sh` /
    future `vssh enroll`). Unknown host → TOFU with explicit warning + record;
    mismatch → hard fail. This kills F2.
- `tls.Config` pinned: TLS 1.3 only, no resumption tickets initially
  (`SessionTicketsDisabled: true` — resumption is an optimization to revisit;
  0-RTT is never enabled), ALPN `vssh/1`.

### 4.2 Wire compatibility — same port, sniff the first byte
A TLS ClientHello starts with record byte `0x16`. Every legacy first line
starts with an ASCII letter/digit (`VAUTH1 `, `ts:hmac`). The daemon peeks one
byte:
- `0x16` → hand the conn to `tls.Server`, then run the **existing line protocol
  unchanged inside the TLS stream** (auth → verbs → FMUX/yamux; yamux nests in
  TLS fine).
- anything else → legacy plaintext path (until removed).

No new port, no flag day, mirrors the dual-auth migration that already worked.

### 4.3 Kill switches (mirror VSSH_REQUIRE_VAUTH)
- Daemon `VSSH_REQUIRE_TLS=1`: refuse plaintext entirely.
- Client `VSSH_REQUIRE_TLS=1`: never fall back to plaintext.
- **F3 fix, independent of TLS**: the legacy-HMAC client fallback in `dialAuth`
  is removed in the same release that ships VTLS1 (the fleet is already 100%
  strict VAUTH1, so the fallback is dead code that only an attacker can
  trigger). This closes the downgrade hole even before REQUIRE_TLS is set.

### 4.4 What happens to VAUTH1
- **Phase 1 (compat)**: VAUTH1 line still runs inside TLS; daemon checks that
  the VAUTH1 pubkey == TLS client cert key (mismatch → reject). Zero behavior
  change for old clients-inside-new-transport.
- **Phase 2**: client cert *is* the identity; client sends `VHELLO1` instead of
  the 2-RTT challenge dance (saves a round trip vs today even with TLS added).
  VAUTH1-inside-TLS still accepted.
- If VAUTH1 survives anywhere in plaintext during migration, add domain
  separation now (F4): sign `"vssh-vauth1\0" || nonce` — daemon accepts both
  forms for one release, then only the prefixed form.

### 4.5 Performance note
Handshake cost: TLS 1.3 is 1-RTT (vs 2-RTT VAUTH1 today), Ed25519 ops are the
same order. With FMUX everything already amortizes to **one handshake per
daemon session**, so the steady-state cost is ~zero; short-lived CLI calls pay
one 1-RTT handshake — likely *faster* than today's 2-RTT VAUTH1. Measure on d1
canary (agent_suite has timing) before/after.

## 5. Rollout plan (each step canaried on d1, then deploy_fleet)

1. **0.7.25 — daemon accepts TLS** (sniff + tls.Server + VerifyPeerCertificate
   → authorized_keys), client still plaintext by default; `vssh handshake-test
   --tls` for verification. Remove client legacy-HMAC fallback (F3). Ship the
   already-planned legacy FWD/RFWD client-fallback removal in the same release
   (both are client-path cleanups; CHANGELOG gate criteria still apply).
2. **0.7.26 — client prefers TLS**, plaintext fallback only when the daemon
   has no TLS (old daemon) AND `VSSH_REQUIRE_TLS` unset; every fallback logged
   loudly + audited. known_hosts distribution via cross_authorize_fleet.sh.
3. **Stabilization window** (same shape as the legacy-tunnel gate): 7 daily
   conformance greens, zero plaintext-auth audit records fleet-wide.
4. **0.7.27 — `VSSH_REQUIRE_TLS=1` fleet-wide** via the same drop-in mechanism
   as require_vauth; later release deletes the plaintext path and the sniff.
5. **External review** (P0.2) is scheduled *after* step 2: reviewers get a
   design where the answer to "who wrote the crypto?" is "Go stdlib", and the
   review scope shrinks to: handshake plumbing, VerifyPeerCertificate, caps
   enforcement, FMUX stream auth, audit chain, and the §6 policy engine.
6. Audits to run alongside (P0.3): downgrade matrix (old/new client × old/new
   daemon × kill-switch states — assert no silent plaintext), nonce/transcript
   review (moot once client-cert identity lands), `VSSH_REQUIRE_*` bypass hunt
   (grep every accept path; FMUX/RCONN included).

Rollback per step = revert commit + redeploy; kill switches are env-only.

## 6. P1(b) — unattended-automation whitelist: threat model & schema (design)

### 6.1 Problem
MCP policy blocks dangerous commands pending human approval (`allow_dangerous`)
— correct interactively, fatal for unattended runs (2026-06-13 MCP 실사용 점검).
We need *pre-approved* danger, scoped tightly, never "dangerous commands are
now fine".

### 6.2 Two enforcement points, deliberately different
- **Daemon-side (authoritative, per key)** — extends caps (`DESIGN_RATIONALE`
  §1 roadmap items 1–3): command allow/deny, path scope, forward targets.
  Survives a compromised/looser client.
- **MCP/client-side (advisory, per target)** — lets `vssh_exec` auto-approve a
  command that matches a pre-approved profile instead of demanding
  `allow_dangerous:true`. Defense in depth only; the daemon rule is the law.

### 6.3 Schema — `policy=<name>` tag + policy file
`authorized_keys` line stays one line: `<pub> caps=exec policy=backup m1-backup`.
Policy files: `/etc/vssh/policies/<name>.json` (or `~/.vssh/policies/`),
hot-reloaded like authorized_keys:

```json
{
  "name": "backup",
  "exec_allow": ["^/usr/bin/rsync -a(z|av)? /var/(lib|backups)/\\S+ vault:",
                  "^/usr/local/bin/backup\\.sh( --verify)?$"],
  "exec_deny":  [],
  "file_paths": ["/var/backups/**"],
  "fwd_targets": [],
  "rate": {"exec_per_min": 10},
  "danger_preapproved": ["^/usr/bin/systemctl restart vsshd$"]
}
```

Semantics: deny first, then allow; **no match = refuse** (typed
`policy_denied`, with rule id in the audit record). `danger_preapproved` is the
unattended-automation list: patterns here run without interactive approval but
are audited with `preapproved:<rule_id>`. Shipped templates: `readonly`
(rpc+facts only), `backup` (file path-scoped + rsync), `ci` (exec allow-listed
build commands), `deploy` (adds the systemctl/restart class, the only template
with `danger_preapproved` entries).

### 6.4 Threats against the whitelist itself
| Threat | Mitigation |
|---|---|
| Shell metachar smuggling (`;`, `&&`, `$()`, backticks, `\n`) inside an allowed prefix | Match the **entire** command string against anchored (`^...$`) regexes; templates reject commands containing `;`, `&`, `|`, `$(`, backtick, newline unless the rule explicitly allows them. Lint at policy load: warn on unanchored rules. |
| Argument injection (`rsync -e 'sh -c ...'`) | Templates enumerate full argv shapes, not bare binaries; docs state plainly that regex whitelisting of shell strings is best-effort — caps verb + path scope remain the hard floor. |
| Path scope escape via symlink/`..` | Daemon resolves (`filepath.Clean` + `EvalSymlinks`) **after** path extraction, before the glob check. |
| Policy file tamper | Root-owned 0644 in /etc, load events audited with file hash; hash chain already tamper-evident. |
| Downgrade by deleting the policy tag | A key with `policy=` but missing file → **fail closed** (key unusable, not unrestricted). |
| MCP auto-approve drift from daemon rules | MCP profile is generated *from* the same policy file (single source of truth), not maintained by hand. |

### 6.5 Order of work
Daemon `policy_denied` engine (exec_allow/deny + file_paths) → templates +
lint → MCP `danger_preapproved` auto-approve reading the same file → fwd
targets/rate. Code starts **after** this design is reviewed in-session against
DESIGN_RATIONALE §1, and lands behind per-key opt-in (`policy=` absent = current
behavior), so the fleet is untouched until policies are assigned.

## 7. Out of scope (recorded decisions)
- **Interactive TTY over MCP**: not planned; MCP is exec/file/rpc-shaped. CLI
  `vssh shell` remains the interactive path. (2026-06-13 점검 항목 2 결정.)
- **MCP tunnel tool** (`vssh_tunnel` open/status/close for -L/-R/-D): P2,
  after P0 transport lands — tunnels should be born TLS-wrapped.
- **Non-mesh targets**: vssh stays fleet-native; `vssh enroll` (P1a) is the
  answer to "new box", not generic ssh client emulation.
