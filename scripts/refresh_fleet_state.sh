#!/usr/bin/env bash
# refresh_fleet_state.sh — rebuild the controller's signed fleet_state snapshot
# so consumers never read a stale inventory. This is the JOB a scheduler runs;
# see install_fleet_state_refresh.sh to register it (launchd/cron, every 6–12h).
#
# Env:
#   VSSH       vssh binary (default: vssh on PATH; the installer pins an abspath
#              because launchd/cron run with a minimal PATH)
#   LIVE       1 = probe node reachability live during rebuild (default 1)
#   REPLICATE  1 = push the fresh snapshot to nodes afterwards (default 0)
#   LOG        append-only log file (default ~/.vssh/fleet_state_refresh.log)
set -uo pipefail
VSSH="${VSSH:-vssh}"
LIVE="${LIVE:-1}"
REPLICATE="${REPLICATE:-0}"
LOG="${LOG:-$HOME/.vssh/fleet_state_refresh.log}"

ts() { date -u +%Y-%m-%dT%H:%M:%SZ; }
mkdir -p "$(dirname "$LOG")"

args=(fleet-state build)
[ "$LIVE" = "1" ] && args+=(--live)

if out=$("$VSSH" "${args[@]}" 2>&1); then
  echo "$(ts) OK   $out" >>"$LOG"
else
  echo "$(ts) ERR  $out" >>"$LOG"
  exit 1
fi

if [ "$REPLICATE" = "1" ]; then
  if "$(cd "$(dirname "$0")" && pwd)/replicate_fleet_state.sh" >>"$LOG" 2>&1; then
    echo "$(ts) OK   replicated to nodes" >>"$LOG"
  else
    echo "$(ts) WARN replicate failed (snapshot still rebuilt locally)" >>"$LOG"
  fi
fi
