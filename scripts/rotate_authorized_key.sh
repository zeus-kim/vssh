#!/bin/bash
# rotate_authorized_key.sh — fleet-wide authorized_keys management for key rotation.
#
# Safe rotation order: ADD the new key everywhere -> verify connectivity with it
# -> REMOVE the old key. Never remove before the new key is proven working.
#
# Usage:
#   scripts/rotate_authorized_key.sh add    "<pubB64> [caps=..] [policy=..] [comment]"  [nodes...]
#   scripts/rotate_authorized_key.sh remove "<pubB64>"                                   [nodes...]
#   DRY_RUN=1 scripts/rotate_authorized_key.sh add "<key>"      # preview only
#   NODES="node1 node2" scripts/rotate_authorized_key.sh remove "<pub>"
#
# Node path: linux -> /etc/vssh/authorized_keys (sudo); darwin -> $HOME/.vssh/authorized_keys.
set -u
ACTION="${1:-}"; KEY="${2:-}"
[ "$#" -ge 2 ] && shift 2 || shift "$#"
NODES="${*:-${NODES:-node1 node2 node3}}"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=8"
DRY="${DRY_RUN:-0}"
case "$ACTION" in add|remove) ;; *) echo "usage: $0 add|remove <key> [nodes...]"; exit 2;; esac
[ -n "$KEY" ] || { echo "missing key argument"; exit 2; }
PUB="${KEY%% *}"

edit_node() {
  local n="$1" os path pfx
  os=$($SSH "$n" 'uname -s' 2>/dev/null) || { echo "[$n] OFFLINE — skip"; return; }
  [ -z "$os" ] && { echo "[$n] OFFLINE — skip"; return; }
  if [ "$os" = "Darwin" ]; then path="$($SSH "$n" 'echo $HOME/.vssh/authorized_keys')"; pfx=""; else path="/etc/vssh/authorized_keys"; pfx="sudo"; fi
  if [ "$DRY" = 1 ]; then echo "[$n] ($os) would $ACTION on $path  (pub ${PUB:0:20}...)"; return; fi
  case "$ACTION" in
    add)
      $SSH "$n" "$pfx mkdir -p \"\$(dirname '$path')\"; $pfx touch '$path'; \
        if $pfx grep -qF '$PUB' '$path'; then echo '[$n] already present'; \
        else printf '%s\n' '$KEY' | $pfx tee -a '$path' >/dev/null && echo '[$n] added'; fi" 2>/dev/null \
        || echo "[$n] add FAILED"
      ;;
    remove)
      $SSH "$n" "if [ -f '$path' ]; then \
        $pfx cp '$path' '$path.bak.\$(date +%Y%m%d%H%M%S)'; \
        $pfx grep -vF '$PUB' '$path' | $pfx tee '$path.tmp' >/dev/null && $pfx mv '$path.tmp' '$path' && echo '[$n] removed (if present)'; \
        else echo '[$n] no authorized_keys'; fi" 2>/dev/null \
        || echo "[$n] remove FAILED"
      ;;
  esac
}
echo "== $ACTION across: $NODES =="
for n in $NODES; do edit_node "$n"; done
echo "== DONE =="
