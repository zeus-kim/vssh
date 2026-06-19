# vssh — External Security Audit: Request for Proposal (DRAFT)

> **Status:** DRAFT for internal review. Bracketed `[TBD]` fields (dates, legal
> entity, budget, contact) are filled in before this is sent to a vendor.
> Companion technical scope + evidence: `docs/SECURITY_AUDIT_PACKAGE.md`
> (read that alongside this RFP — it points reviewers at exact code paths,
> the crypto rationale, residual risks, and how to reproduce every validation).

## 1. Engagement summary

We are seeking a fixed-scope, source-assisted (white-box) security audit of
**vssh**, a mesh-native remote-execution daemon and client/MCP that replaces ad
hoc SSH for fleet and agent automation. The review targets the
security-critical paths: transport, peer authentication, host-identity
verification, the authorization/policy engine, the tamper-evident audit chain,
and the tunnel/multiplexing layer.

The codebase is Go (single module, ~no third-party crypto — Go stdlib
`crypto/tls` and `crypto/ed25519` only). It runs on a production fleet of
roughly fourteen nodes across Linux (amd64/arm64) and macOS (arm64), with a
single controller host.

**Primary objective:** an independent, adversarial assessment of whether the
authentication, transport-confidentiality, authorization, and audit guarantees
hold as designed — and an enumeration of any path that can bypass them.

## 2. System security model (one paragraph)

Identity is a per-node Ed25519 key. Transport is TLS 1.3 (Go stdlib) with
raw-public-key pinning, not PKI — the daemon pins client keys against
`authorized_keys`, the client pins the daemon key against `known_hosts` (TOFU on
first contact). Authentication is a per-node Ed25519 challenge–response (VAUTH1);
there is no shared secret or HMAC anywhere (the legacy path was removed in
v0.7.39). Authorization is per-key capabilities plus an opt-in per-key policy
(command allow/deny, path scope, forward targets, rate, danger pre-approval).
Every command is hash-chain audited with the authenticated key, the transport
(`tls`|`plain`), and the matched policy rule.

## 3. In scope (priority order)

The following map 1:1 to `docs/SECURITY_AUDIT_PACKAGE.md` §2, which cites exact
files and functions:

1. **Transport (VTLS1)** — TLS config, raw-key pin verification, the 0x16
   first-byte sniff, and the `VSSH_REQUIRE_TLS` kill-switch. Key question:
   channel binding between the TLS layer and the in-band VAUTH1 identity
   (daemon checks cert key == VAUTH1 key).
2. **Peer authentication** — Ed25519 challenge–response, `authorized_keys`
   parsing, downgrade resistance (no plaintext/legacy fallback; pinned-key
   mismatch is a hard fail).
3. **Host-identity verification** — the node-key registry and its auto-setup
   bootstrap; confirm it cannot be induced to pin a wrong key.
4. **Authorization / policy engine** — deny-first → danger_preapproved →
   allow → no-match-refuse; anchored full-string matching vs metachar/newline
   smuggling; path scope with symlink/`..` resolution; fail-closed on a missing
   policy file.
5. **Audit chain** — per-record hash chaining, key attribution, transport
   tagging; confirm no exec/forward path skips it and that tampering is evident.
6. **Tunnels / multiplexing** — FMUX stream auth and reverse-forward control
   channel; confirm per-stream authorization and no unauthenticated injection.
7. **Kill-switch bypass hunt** — every accept/dial path that should honor
   `VSSH_REQUIRE_VAUTH` / `VSSH_REQUIRE_TLS` / host-identity; confirm none can be
   forced to plaintext or skipped (FMUX/RCONN/RPC included).

## 4. Out of scope

Interactive TTY over MCP; generic (non-mesh) SSH server emulation; the
`vssh_tunnel` MCP surface (the underlying forward/FMUX primitives ARE in scope).
Underlay network controls (WireGuard, tailnet ACLs) are context, not the audit
target — though the reviewer should assess what breaks if the underlay is
assumed hostile (see threat model). Physical, social-engineering, and
denial-of-service testing against production are excluded.

## 5. Threat model & specific questions

Adversaries the design must withstand: (A1) an on-path peer inside the mesh that
terminates the underlay and sees vssh traffic; (A2) a daemon impersonator
(name→address drift, ARP, a port squatter); (A3) a stolen node key. Reviewers
are asked to specifically opine on:

- Channel binding: is tying cert key to the VAUTH1 line sufficient to prevent
  identity confusion between the TLS and application layers?
- Downgrade: with `VSSH_REQUIRE_TLS` not yet enforced fleet-wide, can an attacker
  force a plaintext-transport VAUTH1 session, and does the planned flip fully
  close it?
- Host-identity: is the registry's fail-open-by-absence (unknown nodes skip
  verification) an acceptable posture, and can auto-setup be steered to a wrong
  key?
- Policy: can command-string whitelisting be smuggled past (metachars, encoding,
  argv vs shell), and is the caps-verb + path-scope floor the right hard boundary?

## 6. Methodology

Source-assisted / white-box. We provide the full repository at a tagged commit,
the technical package (§ref above), build and test instructions, and a
non-production test deployment that mirrors the fleet topology. The reviewer is
expected to combine code review with dynamic testing against the test
deployment (never against production).

## 7. Deliverables

- A written report: executive summary; methodology; per-finding entries with
  severity (we suggest CVSS 3.1 plus a short exploitability narrative),
  affected code path, reproduction, and concrete remediation.
- A findings spreadsheet/tracker suitable for triage.
- A read-out call.
- One remediation retest round after fixes, confirming closure.
- Clear statement of residual risk for anything left unmitigated.

## 8. Rules of engagement

- Testing only against the provided non-production environment.
- No exfiltration of real fleet data; any incidental secrets discovered are
  reported, not retained.
- Coordinated disclosure: findings shared only with the named contact until
  remediation, then on a mutually agreed timeline.
- A mutual NDA is executed before access is granted.

## 9. Access & materials provided

- Repository snapshot at commit/tag `[TBD]`.
- `docs/SECURITY_AUDIT_PACKAGE.md` (scope + evidence) and the transport
  migration design doc.
- Build/test reproduction: `go vet ./... && go test ./...` (unit + in-process
  e2e), plus the fleet conformance and transport-gate scripts.
- A scoped, non-production test fleet with seeded keys and policies.

## 10. Timeline (placeholder)

- RFP issued: `[TBD]` · Proposals due: `[TBD]` · Vendor selected: `[TBD]`
- Engagement window: `[TBD]` (target ~2–3 weeks of review + 1 retest round)
- Final report: `[TBD]`

## 11. Reviewer qualifications

Demonstrated experience auditing Go network services and applied cryptography
(TLS, Ed25519, key-pinning/TOFU trust models); prior remote-execution,
SSH-alternative, or agent-infrastructure assessments preferred. Please include
redacted sample findings and references.

## 12. Proposal submission (placeholder)

Please submit: approach and methodology; team and qualifications; effort and
timeline; fixed price or rate card; assumptions and exclusions. Submit to
`[TBD contact]` by `[TBD date]`. Questions to the same contact.

## 13. Contact & confidentiality

Primary contact: `[TBD name, role, email]`. This RFP and all materials are
confidential and provided solely to evaluate and perform this engagement.
