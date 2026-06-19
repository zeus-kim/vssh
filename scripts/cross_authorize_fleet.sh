#!/usr/bin/env bash
# cross_authorize_fleet.sh — full-mesh VAUTH1 trust.
#
# Collects every online node's Ed25519 public key (the root daemon identity on
# Linux nodes, the user identity on macOS nodes, plus this controller's key) and
# appends ALL of them to EVERY node's authorized_keys — idempotent, additive only.
# Also merges the keys into this controller's own ~/.vssh/authorized_keys so
# nodes can VAUTH1 back into the controller's daemon.
#
# After this, any node can authenticate to any other node with per-node keys,
# which is the precondition for disabling the legacy shared-HMAC token
# (VSSH_REQUIRE_VAUTH=1).
#
# Usage:
#   scripts/cross_authorize_fleet.sh
#   NODES="node1 node2" scripts/cross_authorize_fleet.sh
set -uo pipefail
LINUX_NODES="${NODES:-node1 node2 node3}"
MAC_NODES="${MAC_NODES:-macnode1}"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=8"
SCP="scp -q -o BatchMode=yes -o ConnectTimeout=8"
VSSH="${VSSH:-/opt/homebrew/bin/vssh}"; [ -x "$VSSH" ] || VSSH="$(command -v vssh)"

TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT
echo "$("$VSSH" pubkey | tail -1) m1" >> "$TMP"

echo "== collecting node pubkeys =="
COLLECTED="m1"
for n in $LINUX_NODES; do
  pub=$($SSH "$n" "sudo /usr/local/bin/vssh pubkey" 2>/dev/null | tail -1)
  if [ -n "$pub" ]; then echo "$pub $n" >> "$TMP"; COLLECTED="$COLLECTED $n"; echo "  [$n] ok"
  else echo "  [$n] FAIL/offline"; fi
done
for n in $MAC_NODES; do
  pub=$($SSH "$n" '$HOME/.local/bin/vssh pubkey' 2>/dev/null | tail -1)
  if [ -n "$pub" ]; then echo "$pub $n" >> "$TMP"; COLLECTED="$COLLECTED $n"; echo "  [$n] ok"
  else echo "  [$n] FAIL/offline"; fi
done
echo "== $(wc -l < "$TMP" | tr -d ' ') keys collected =="

echo "== distributing to fleet =="
for n in $LINUX_NODES; do
  if $SCP "$TMP" "$n:/tmp/vssh_keys.$$" 2>/dev/null && \
     $SSH "$n" "sudo mkdir -p /etc/vssh && sudo touch /etc/vssh/authorized_keys && sudo bash -c 'while read -r pub rest; do grep -q \"^\$pub \" /etc/vssh/authorized_keys || grep -q \"^\$pub\$\" /etc/vssh/authorized_keys || echo \"\$pub \$rest\" >> /etc/vssh/authorized_keys; done < /tmp/vssh_keys.$$'; rm -f /tmp/vssh_keys.$$" 2>/dev/null; then
    echo "  [$n] merged"
  else echo "  [$n] FAIL/offline"; fi
done
for n in $MAC_NODES; do
  if $SCP "$TMP" "$n:/tmp/vssh_keys.$$" 2>/dev/null && \
     $SSH "$n" "mkdir -p ~/.vssh && touch ~/.vssh/authorized_keys && while read -r pub rest; do grep -q \"^\$pub \" ~/.vssh/authorized_keys || grep -q \"^\$pub\$\" ~/.vssh/authorized_keys || echo \"\$pub \$rest\" >> ~/.vssh/authorized_keys; done < /tmp/vssh_keys.$$; rm -f /tmp/vssh_keys.$$" 2>/dev/null; then
    echo "  [$n] merged"
  else echo "  [$n] FAIL/offline"; fi
done
# controller itself
mkdir -p ~/.vssh && touch ~/.vssh/authorized_keys
while read -r pub rest; do
  grep -q "^$pub " ~/.vssh/authorized_keys || grep -q "^$pub\$" ~/.vssh/authorized_keys || echo "$pub $rest" >> ~/.vssh/authorized_keys
done < "$TMP"
echo "  [m1] merged"

# NOTE: direct node-to-node TCP paths are NOT guaranteed by the tailnet ACLs
# (m1 reaches every node, nodes may not reach each other). So the mesh proof is:
#  (a) every node VAUTH1s its OWN daemon over loopback with its OWN key
#      (full sign/verify round trip using the distributed authorized_keys), and
#  (b) the controller VAUTH1s every node (existing authorize_fleet check).
echo "== verify: per-node loopback VAUTH1 + key count =="
ok=0; fail=0
for n in $LINUX_NODES; do
  out=$($SSH "$n" "sudo /usr/local/bin/vssh handshake-test 127.0.0.1 2>/dev/null | grep -c AUTH_OK; sudo wc -l < /etc/vssh/authorized_keys" 2>/dev/null | tr '\n' ' ')
  case "$out" in
    1\ *) ok=$((ok+1)); echo "  [$n] loopback OK, keys=$(echo $out | awk '{print $2}')";;
    *) fail=$((fail+1)); echo "  [$n] FAIL ($out)";;
  esac
done
for n in $MAC_NODES; do
  out=$($SSH "$n" '$HOME/.local/bin/vssh handshake-test 127.0.0.1 2>/dev/null | grep -c AUTH_OK; wc -l < ~/.vssh/authorized_keys' 2>/dev/null | tr '\n' ' ')
  case "$out" in
    1\ *) ok=$((ok+1)); echo "  [$n] loopback OK, keys=$(echo $out | awk '{print $2}')";;
    *) fail=$((fail+1)); echo "  [$n] FAIL ($out)";;
  esac
done
echo "== loopback verify OK=$ok FAIL=$fail =="
[ "$fail" -eq 0 ] && echo "CROSS-AUTHORIZED" || exit 1
