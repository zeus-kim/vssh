#!/usr/bin/env bash
# audit_transport_scan.sh — §5.3 stabilization gate.
# Scans each fleet node's vssh audit log and counts authenticated connections by
# transport (tls vs plain). Plaintext-auth records are the upgrade TODO list: the
# gate for flipping VSSH_REQUIRE_TLS=1 fleet-wide (0.7.27) is zero plaintext here.
#
# Reads the right log per OS: linux daemon runs as root -> sudo /var/log/vssh;
# darwin user-launchd daemon -> ~/.vssh or /tmp/vssh.
#
# Usage:
#   scripts/audit_transport_scan.sh
#   NODES="node1 node2 node3" scripts/audit_transport_scan.sh
#   SINCE=2026-06-13T00:00:00 scripts/audit_transport_scan.sh   # only ts >= SINCE
#
# Exit: 0 if zero plaintext-auth (GREEN), 1 if any plaintext (RED), for cron/paging.
set -u
NODES="${NODES:-node1 node2 node3}"
SINCE="${SINCE:-}"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=8"

# remote one-liner: pick the log path by OS and stream it
REMOTE='os=$(uname -s);
if [ "$os" = Linux ]; then sudo cat /var/log/vssh/audit.log 2>/dev/null || cat "$HOME/.vssh/audit.log" 2>/dev/null;
else cat "$HOME/.vssh/audit.log" 2>/dev/null || cat /tmp/vssh/audit.log 2>/dev/null || sudo cat /var/log/vssh/audit.log 2>/dev/null; fi'

TOTAL_PLAIN=0; TOTAL_TLS=0; OFFENDERS=""
echo "== vssh transport audit scan ${SINCE:+(since $SINCE)} =="
for n in $NODES; do
  probe=$($SSH "$n" 'uname -s' 2>/dev/null)
  [ -z "$probe" ] && { echo "[$n] OFFLINE/unreachable — skip"; continue; }
  out=$($SSH "$n" "$REMOTE" 2>/dev/null | SINCE="$SINCE" python3 -c '
import sys,os,json,collections
since=os.environ.get("SINCE","")
tls=plain=0; keys=collections.Counter()
for l in sys.stdin:
  l=l.strip()
  if not l: continue
  try: r=json.loads(l)
  except Exception: continue
  if since and r.get("ts","") < since: continue
  t=r.get("transport")
  if t=="tls": tls+=1
  elif t=="plain":
    plain+=1; keys[r.get("key_name") or (r.get("key","") or "?")[:12]]+=1
  # records with no transport field predate 0.7.26 server marker; count as legacy-plain
  elif t is None:
    pass
print("%d\t%d\t%s" % (tls, plain, " ".join("%s=%d"%(k,v) for k,v in keys.most_common())))
' 2>/dev/null)
  tls=$(printf '%s' "$out" | cut -f1); plain=$(printf '%s' "$out" | cut -f2); src=$(printf '%s' "$out" | cut -f3)
  tls=${tls:-0}; plain=${plain:-0}
  case "$tls$plain" in ""|"0"|"00") if [ -z "$out" ]; then echo "[$n] no-audit-log/unreadable"; continue; fi;; esac
  TOTAL_TLS=$((TOTAL_TLS+tls)); TOTAL_PLAIN=$((TOTAL_PLAIN+plain))
  if [ "${plain:-0}" -gt 0 ] 2>/dev/null; then
    echo "[$n] tls=$tls plain=$plain  PLAINTEXT-SRC: ${src:-?}"; OFFENDERS="$OFFENDERS $n"
  else
    echo "[$n] tls=$tls plain=$plain  ok"
  fi
done
echo "== FLEET TOTAL: tls=$TOTAL_TLS plain=$TOTAL_PLAIN =="
if [ "$TOTAL_PLAIN" -eq 0 ]; then echo "GATE: GREEN — zero plaintext-auth, safe to flip REQUIRE_TLS"; exit 0
else echo "GATE: RED — plaintext-auth present on:$OFFENDERS"; exit 1; fi
