#!/bin/bash
# pin_fleet_keys.sh — populate ~/.vssh/known_hosts with every fleet daemon's
# Ed25519 key, pinned under BOTH the node name and the resolved address, so
# the TLS dialer (0.7.26+, TLS-first by default) never has to TOFU a node we
# already manage. Idempotent; safe to re-run after key rotation with PRUNE=1.
#
# Mechanism: `vssh handshake-test --tls <node>` performs the real VTLS1
# handshake and prints the daemon key it saw (server_key) — the same trust
# path the dialer uses, no out-of-band ssh required.
#
# Usage:
#   scripts/pin_fleet_keys.sh                # default NODES
#   NODES="node1 node2" scripts/pin_fleet_keys.sh
#   PRUNE=1 scripts/pin_fleet_keys.sh        # drop existing pins for NODES first
set -u

VSSH_BIN="${VSSH_BIN:-vssh}"
NODES="${NODES:-node1 node2 node3}"
KH="$HOME/.vssh/known_hosts"
mkdir -p "$HOME/.vssh"
touch "$KH"
chmod 600 "$KH"

pin() { # host key — append if not already pinned with this exact pair
  grep -q "^$1 $2$" "$KH" 2>/dev/null && return 0
  if [ "${PRUNE:-0}" = "1" ]; then
    grep -v "^$1 " "$KH" > "$KH.tmp" 2>/dev/null; mv "$KH.tmp" "$KH"
  elif grep -q "^$1 " "$KH" 2>/dev/null; then
    echo "  !! $1 already pinned with a DIFFERENT key — refusing to overwrite (rotate with PRUNE=1)"
    return 1
  fi
  echo "$1 $2" >> "$KH"
  echo "  pinned $1"
}

ok=0; skip=0; conflict=0
for node in $NODES; do
  out="$($VSSH_BIN handshake-test --tls "$node" 2>/dev/null)"
  key="$(printf '%s' "$out" | sed -n 's/.*"server_key": "\([^"]*\)".*/\1/p' | head -1)"
  if [ -z "$key" ]; then
    # JSON may be multi-line; try line-by-line
    key="$(printf '%s\n' "$out" | grep '"server_key"' | sed 's/.*: "\(.*\)".*/\1/' | head -1)"
  fi
  if [ -z "$key" ]; then
    echo "[$node] unreachable or no TLS — skipped"
    skip=$((skip+1))
    continue
  fi
  echo "[$node] key ${key:0:12}…"
  pin "$node" "$key" || conflict=$((conflict+1))
  ok=$((ok+1))
done

echo "== pins: $ok ok, $skip skipped, $conflict conflicts =="
echo "known_hosts: $KH ($(grep -c . "$KH") lines)"
