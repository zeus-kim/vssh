#!/usr/bin/env bash
# build_node_registry.sh — build a TRUSTED name->daemon-TLS-key registry.
#
# Each node's daemon TLS key is captured via a LOOPBACK handshake ON the node
# (handshake-test --tls 127.0.0.1 = that node's own daemon), which cannot be
# misrouted by name resolution. Writes ~/.vssh/node_keys ("<name> <pubB64>"),
# the authoritative source for client host-identity verification — independent
# of the HOME-dependent vssh_id path confusion (daemon uses /etc/vssh when HOME
# is unset under systemd, /root/.vssh otherwise) and of stale config IPs.
#
# Usage: scripts/build_node_registry.sh   (NODES=... to override)
set -uo pipefail
NODES="${NODES:-node1 node2 node3}"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=8"
OUT="$HOME/.vssh/node_keys"
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
ok=0; fail=0
for n in $NODES; do
  os=$($SSH "$n" 'uname -s' 2>/dev/null)
  [ -z "$os" ] && { echo "  [$n] OFFLINE — skip"; fail=$((fail+1)); continue; }
  if [ "$os" = Darwin ]; then VB='$HOME/.local/bin/vssh'; PFX=''; else VB='/usr/local/bin/vssh'; PFX='sudo'; fi
  key=$($SSH "$n" "$PFX $VB handshake-test --tls 127.0.0.1 2>/dev/null | awk -F'\"' '/server_key/{print \$4}'" 2>/dev/null)
  if [ -n "$key" ]; then echo "$n $key" >> "$TMP"; echo "  [$n] $key"; ok=$((ok+1)); else echo "  [$n] no key (daemon unreachable on loopback?)"; fail=$((fail+1)); fi
done
# merge: keep existing entries for nodes we couldn't refresh, overwrite refreshed
if [ -f "$OUT" ]; then
  while read -r nm ky; do
    [ -z "$nm" ] && continue
    grep -qE "^$nm " "$TMP" || echo "$nm $ky" >> "$TMP"
  done < "$OUT"
fi
sort -u "$TMP" > "$OUT"
chmod 600 "$OUT"
echo "== node_keys: $(wc -l < "$OUT" | tr -d ' ') entries (ok=$ok fail=$fail) -> $OUT =="
