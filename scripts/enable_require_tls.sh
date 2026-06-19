#!/usr/bin/env bash
# enable_require_tls.sh — flip the fleet to VSSH_REQUIRE_TLS=1 (§5.3 step 4),
# mirroring the require_vauth drop-in. Refuses to flip unless the transport gate
# is GREEN (zero plaintext-auth in the window) UNLESS FORCE=1.
#
# Per node: install an env drop-in (systemd vsshd.service.d/require_tls.conf, or
# launchd plist env), restart, and verify TLS+VAUTH1 AUTH_OK still works.
#
# Usage:
#   SINCE=2026-06-20T00:00:00 scripts/enable_require_tls.sh   # gate-check then flip
#   FORCE=1 scripts/enable_require_tls.sh                     # skip the gate check
#   CHECK=1 scripts/enable_require_tls.sh                     # gate-check only, no flip
set -uo pipefail
REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
NODES="${NODES:-node1 node2 node3}"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=8"
VSSH="${VSSH:-/opt/homebrew/bin/vssh}"

# 1) gate
if [ "${FORCE:-0}" != 1 ]; then
  echo "== transport gate check =="
  if SINCE="${SINCE:-}" bash "$REPO_DIR/scripts/audit_transport_scan.sh"; then
    echo "GATE GREEN"
  else
    echo "GATE RED — refusing to flip (set FORCE=1 to override after review)"; exit 1
  fi
fi
[ "${CHECK:-0}" = 1 ] && { echo "check-only; not flipping"; exit 0; }

# 2) flip per node
for n in $NODES; do
  os=$($SSH "$n" 'uname -s' 2>/dev/null)
  [ -z "$os" ] && { echo "[$n] OFFLINE — skip"; continue; }
  if [ "$os" = Darwin ]; then
    $SSH "$n" 'launchctl setenv VSSH_REQUIRE_TLS 1 2>/dev/null;
      plist=$HOME/Library/LaunchAgents/ai.vssh.vsshd.plist;
      /usr/libexec/PlistBuddy -c "Set :EnvironmentVariables:VSSH_REQUIRE_TLS 1" "$plist" 2>/dev/null \
        || /usr/libexec/PlistBuddy -c "Add :EnvironmentVariables:VSSH_REQUIRE_TLS string 1" "$plist" 2>/dev/null;
      launchctl bootout gui/$(id -u)/ai.vssh.vsshd 2>/dev/null; sleep 1;
      launchctl bootstrap gui/$(id -u) "$plist" 2>/dev/null' >/dev/null 2>&1 && echo "[$n] flipped (launchd)" || echo "[$n] flip FAIL"
  else
    $SSH "$n" 'sudo mkdir -p /etc/systemd/system/vsshd.service.d && \
      printf "[Service]\nEnvironment=VSSH_REQUIRE_TLS=1\n" | sudo tee /etc/systemd/system/vsshd.service.d/require_tls.conf >/dev/null && \
      sudo systemctl daemon-reload && sudo systemctl restart vsshd' >/dev/null 2>&1 && echo "[$n] flipped (systemd)" || echo "[$n] flip FAIL"
  fi
  sleep 1
  hs=$("$VSSH" handshake-test --tls "$n" 2>/dev/null | grep -c AUTH_OK)
  rt=$("$VSSH" rpc "$n" node_info 2>/dev/null | grep -c '"require_tls": true')
  if [ "${hs:-0}" -ge 1 ] && [ "${rt:-0}" -ge 1 ]; then echo "  [$n] verify TLS AUTH_OK + require_tls=true"; else echo "  [$n] VERIFY FAIL — tls_auth=$hs require_tls=$rt (env not reloaded or a path broke)"; fi
done
echo "== REQUIRE_TLS flip complete; also set it on the m1 coordinator unit + restart =="
