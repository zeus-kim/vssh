# vssh — Key Rotation & Recovery Runbook

vssh authentication is key-only (per-node Ed25519, VAUTH1). Two kinds of key
matter: a host's **identity** (`~/.vssh/vssh_id`, presented during auth and TLS)
and the **authorized operator keys** (`authorized_keys`, who may connect). This
runbook covers rotating each safely, plus recovery.

## 1. Rotate a host's identity key
On the host whose identity you want to replace:

```
vssh keygen --rotate
```

This backs up the old key to `vssh_id.bak.<UTC-timestamp>` and writes a fresh
Ed25519 identity, printing the NEW public key. Then:

1. **Restart the daemon** — it caches the identity at startup and keeps serving
   the OLD key until restarted:
   - linux:  `sudo systemctl restart vsshd`
   - darwin: `launchctl bootout gui/$(id -u)/<label> && launchctl bootstrap gui/$(id -u) <plist>`
2. **Re-pin on controllers** so host-identity verification trusts the new key:
   `scripts/build_node_registry.sh` (refreshes `node_keys`).
3. If the host is also an **operator** (authenticates TO others), publish its new
   pubkey and retire the old one — see §2.

> Inspect the current identity any time with `vssh keygen` (no `--rotate`).

## 2. Rotate an operator key across the fleet (add → verify → remove)
Never remove the old key before the new one is proven. Order:

```
# 1) ADD the new key everywhere (non-destructive, idempotent)
scripts/rotate_authorized_key.sh add "<NEW_PUBB64> caps=exec,rpc operator-2026"

# 2) VERIFY connectivity with the new key (e.g. from the operator host)
vssh run d1 'echo ok'        # or handshake-test against a sample of nodes

# 3) REMOVE the old key everywhere (backs up authorized_keys per node)
scripts/rotate_authorized_key.sh remove "<OLD_PUBB64>"
```

Options: `DRY_RUN=1` previews without changes; `NODES="d1 g1"` limits scope.
Node path: linux `/etc/vssh/authorized_keys` (sudo), darwin `$HOME/.vssh/authorized_keys`.

## 3. Recovery
- **Locked out after identity rotation** (forgot to re-add/re-pin): restore the
  backup on the host — `cp ~/.vssh/vssh_id.bak.<ts> ~/.vssh/vssh_id` — and restart
  the daemon; the old (still-authorized) key works again.
- **authorized_keys mistake**: each `remove` writes `authorized_keys.bak.<ts>` on
  the node; restore it and restart is not needed (the daemon re-reads the file).
- Keep at least one **break-glass** operator key authorized fleet-wide that is not
  rotated in the same batch, so a bad rotation never locks everyone out.

## 4. Caveats
- The daemon reads its identity once at startup → a rotation needs a restart.
- Host-identity verification only enforces nodes present in `node_keys`; re-pin
  after rotating a node's identity or its verification silently stops matching.
- Removing a key does not terminate live sessions; it blocks new auth.
