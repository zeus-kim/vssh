# Security

## Reporting a vulnerability

Please report security issues **privately** via
[GitHub Security Advisories](https://github.com/zeus-kim/vssh/security/advisories/new).
Do not open public issues for vulnerabilities. We aim to acknowledge reports
promptly and coordinate a fix and disclosure timeline with you.

## Threat model in one line

An authenticated peer of `vssh server` can run commands and transfer files **as
the configured user** on that node. Treat daemon access as **root-equivalent**
and protect the keys/secret accordingly.

## How vssh secures a connection

1. **Transport — TLS 1.3 with key pinning.**
   The daemon presents an **Ed25519 raw public key** (no CA / no certificate
   chain); the client pins it. A first-byte check distinguishes TLS from legacy
   plaintext. Set `VSSH_REQUIRE_TLS=1` to refuse non-TLS connections outright.

2. **Host identity — you reach the host you named.**
   The client checks the reached daemon's key against a trusted registry
   (`~/.vssh/node_keys`). On by default; a mismatch is a hard failure, which
   prevents a name from being silently misrouted to the wrong machine. Opt out
   only with `VSSH_NO_HOSTKEY_VERIFY=1` (not recommended).

3. **Authentication — per-node keys, preferred.**
   **VAUTH1** is an Ed25519 challenge–response: the server sends a nonce, the
   client signs it with its per-node key. There is **no shared secret** on the
   wire and no replay. A client is authorized by listing its public key in the
   server's `~/.vssh/authorized_keys` (or `/etc/vssh/authorized_keys`).

4. **Authorization — optional per-key policy.**
   Each authorized key can be scoped with allow/deny command lists, file-path
   scoping for put/get, forward-target allow-lists, and rate limits. Policy is
   **deny-first and fail-closed** and every decision is audited. Templates live
   in `policies/` (`readonly`, `backup`, `ci`, `deploy`).

5. **Audit.**
   Executions are recorded as structured records attributed to the authenticated
   key, suitable for a tamper-evident (hash-chained) audit log.

## Keys, not secrets

- There is **no shared secret**. Each host has an Ed25519 identity
  (`vssh pubkey`); a daemon authorizes a client only if its public key is listed
  in `~/.vssh/authorized_keys` (or `/etc/vssh/authorized_keys`). Protect those
  private keys (`~/.vssh/`) like SSH keys.
- The VPN (WireGuard/Tailscale) encrypts the tunnel but does **not** authenticate
  vssh. Do not treat the VPN as a replacement for key authorization.

## Hardening checklist

- [ ] Enroll per-node Ed25519 keys (`scripts/enroll.sh`) and keep
      `~/.vssh/authorized_keys` minimal and reviewed.
- [ ] Set `VSSH_REQUIRE_TLS=1` once all peers speak TLS.
- [ ] Keep host-identity verification on (do not set `VSSH_NO_HOSTKEY_VERIFY`).
- [ ] Assign least-privilege policy templates to automation keys.
- [ ] Firewall the daemon port (default 48291) to the private network only.

## Scope

This policy covers the `vssh` binary and `vssh server` from this repository.
`vssh` does not wrap OpenSSH; for sshd-backed access use OpenSSH and its own
security model.
