#!/usr/bin/env bash
# authorize_fleet.sh — trust a controller's Ed25519 public key across the fleet.
#
# Appends this host's vssh public key (or a key passed as $1) to every node's
# authorized_keys so the node accepts VAUTH1 challenge-response auth from it.
# Idempotent. Linux nodes use /etc/vssh/authorized_keys (root daemon, via sudo);
# the macOS user daemon uses ~/.vssh/authorized_keys.
#
# Usage:
#   scripts/authorize_fleet.sh                 # distribute THIS host's pubkey
#   scripts/authorize_fleet.sh "<base64pub>"   # distribute a specific pubkey
#   NODES="node1 node2" scripts/authorize_fleet.sh

set -uo pipefail
VSSH="${VSSH:-/opt/homebrew/bin/vssh}"; [ -x "$VSSH" ] || VSSH="$(command -v vssh)"
PUB="${1:-$("$VSSH" pubkey)}"
LINUX_NODES="${NODES:-node1 node2 node3}"
MAC_NODES="${MAC_NODES:-macnode1}"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=8"

echo "== authorizing key: $PUB =="
for n in $LINUX_NODES; do
  # /etc/vssh may not exist (secret often comes from the unit env) — create it.
  $SSH "$n" "sudo mkdir -p /etc/vssh && (sudo grep -q '$PUB' /etc/vssh/authorized_keys 2>/dev/null || echo '$PUB authorized' | sudo tee -a /etc/vssh/authorized_keys >/dev/null)" 2>/dev/null \
    && echo "  [$n] ok" || echo "  [$n] FAIL/offline"
done
for n in $MAC_NODES; do
  $SSH "$n" "mkdir -p ~/.vssh; grep -q '$PUB' ~/.vssh/authorized_keys 2>/dev/null || echo '$PUB authorized' >> ~/.vssh/authorized_keys" 2>/dev/null \
    && echo "  [$n] ok" || echo "  [$n] FAIL/offline"
done

echo "== verify VAUTH1 handshake =="
ok=0; fail=0
for n in $LINUX_NODES $MAC_NODES; do
  if "$VSSH" handshake-test "$n" 2>/dev/null | grep -q AUTH_OK; then
    ok=$((ok+1))
  else
    fail=$((fail+1)); echo "  VAUTH1 FAIL: $n"
  fi
done
echo "== VAUTH1 OK=$ok FAIL=$fail =="
[ "$fail" -eq 0 ] && echo "ALL AUTHORIZED" || exit 1
