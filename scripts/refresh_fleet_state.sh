#!/usr/bin/env bash
# refresh_fleet_state.sh — rebuild the controller's signed fleet_state snapshot
# AND re-derive fleet memory from what each node actually runs, so neither the
# inventory nor the @role/@tag/@service selectors ever go stale. This is the JOB
# a scheduler runs; see install_fleet_state_refresh.sh to register it
# (launchd/cron, every 6–12h).
#
# Env:
#   VSSH       vssh binary (default: vssh on PATH; the installer pins an abspath
#              because launchd/cron run with a minimal PATH)
#   LIVE       1 = probe node reachability live during rebuild (default 1)
#   DISCOVER   1 = re-derive fleet memory (role/services/tags) from live probes
#              (default 1). Notes are preserved; only derived fields change.
#   REPLICATE  1 = push the fresh snapshot to nodes afterwards (default 0)
#   LOG        append-only log file (default ~/.vssh/fleet_state_refresh.log)
set -uo pipefail
VSSH="${VSSH:-vssh}"
LIVE="${LIVE:-1}"
DISCOVER="${DISCOVER:-1}"
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

# Re-derive fleet memory from observed usage patterns (GPUs, running units,
# listening ports, containers). Keeps @role/@tag/@service selectors true to the
# fleet as nodes change roles, without anyone hand-editing fleet_memory.json.
# Non-fatal: a failed discovery must not lose the snapshot rebuild above.
if [ "$DISCOVER" = "1" ]; then
  # Capture without a pipe: `cmd | head` under `set -o pipefail` reports failure
  # when head closes the pipe early (SIGPIPE), which would log a false WARN.
  if dout=$("$VSSH" memory discover --apply 2>&1); then
    summary=$(printf '%s\n' "$dout" | grep -m1 'applied to' || true)
    [ -n "$summary" ] || summary=$(printf '%s\n' "$dout" | head -1)
    echo "$(ts) OK   memory discover: $summary" >>"$LOG"
  else
    echo "$(ts) WARN memory discover failed: $(printf '%s\n' "$dout" | head -3 | tr '\n' ' ')" >>"$LOG"
  fi
fi

if [ "$REPLICATE" = "1" ]; then
  if "$(cd "$(dirname "$0")" && pwd)/replicate_fleet_state.sh" >>"$LOG" 2>&1; then
    echo "$(ts) OK   replicated to nodes" >>"$LOG"
  else
    echo "$(ts) WARN replicate failed (snapshot still rebuilt locally)" >>"$LOG"
  fi
fi
