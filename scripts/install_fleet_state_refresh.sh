#!/usr/bin/env bash
# install_fleet_state_refresh.sh — schedule periodic fleet_state rebuilds on the
# CONTROLLER so the signed snapshot never goes stale (it was observed 48h old).
# macOS controller -> launchd LaunchAgent; Linux -> user cron. Idempotent.
#
# Usage:
#   scripts/install_fleet_state_refresh.sh [install|uninstall|status]
# Env:
#   INTERVAL_HOURS  rebuild cadence, 6–12 recommended (default 8)
#   REPLICATE       1 = also push the fresh snapshot to nodes (default 0)
set -uo pipefail

ACTION="${1:-install}"
INTERVAL_HOURS="${INTERVAL_HOURS:-8}"
REPLICATE="${REPLICATE:-0}"
LABEL="ai.vssh.fleetstate"
SELF_DIR="$(cd "$(dirname "$0")" && pwd)"
JOB="$SELF_DIR/refresh_fleet_state.sh"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
CRON_MARK="# $LABEL"

case "$INTERVAL_HOURS" in
  ''|*[!0-9]*) echo "INTERVAL_HOURS must be an integer"; exit 1 ;;
esac
if [ "$INTERVAL_HOURS" -lt 6 ] || [ "$INTERVAL_HOURS" -gt 12 ]; then
  echo "warning: INTERVAL_HOURS=$INTERVAL_HOURS is outside the recommended 6–12h" >&2
fi

VSSH_BIN="$(command -v vssh || true)"
[ -n "$VSSH_BIN" ] || { echo "vssh not on PATH; install it first"; exit 1; }
chmod +x "$JOB" 2>/dev/null || true

install_launchd() {
  local secs=$((INTERVAL_HOURS * 3600))
  mkdir -p "$HOME/Library/LaunchAgents"
  cat >"$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>$JOB</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>VSSH</key><string>$VSSH_BIN</string>
    <key>LIVE</key><string>1</string>
    <key>REPLICATE</key><string>$REPLICATE</string>
  </dict>
  <key>StartInterval</key><integer>$secs</integer>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>$HOME/.vssh/fleet_state_refresh.out</string>
  <key>StandardErrorPath</key><string>$HOME/.vssh/fleet_state_refresh.err</string>
</dict>
</plist>
EOF
  launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$PLIST"
  echo "installed launchd agent $LABEL (every ${INTERVAL_HOURS}h, replicate=$REPLICATE)"
  echo "  plist: $PLIST"
  echo "  log:   $HOME/.vssh/fleet_state_refresh.log"
}

uninstall_launchd() {
  launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
  rm -f "$PLIST"
  echo "removed launchd agent $LABEL"
}

install_cron() {
  local entry="0 */$INTERVAL_HOURS * * * VSSH=$VSSH_BIN LIVE=1 REPLICATE=$REPLICATE $JOB $CRON_MARK"
  local current; current="$(crontab -l 2>/dev/null | grep -v "$CRON_MARK" || true)"
  printf '%s\n%s\n' "$current" "$entry" | sed '/^$/d' | crontab -
  echo "installed cron job (every ${INTERVAL_HOURS}h, replicate=$REPLICATE)"
  echo "  log: $HOME/.vssh/fleet_state_refresh.log"
}

uninstall_cron() {
  crontab -l 2>/dev/null | grep -v "$CRON_MARK" | sed '/^$/d' | crontab - || true
  echo "removed cron job $LABEL"
}

is_darwin() { [ "$(uname -s)" = "Darwin" ]; }

case "$ACTION" in
  install)
    if is_darwin; then install_launchd; else install_cron; fi
    echo "running an initial rebuild now..."
    VSSH="$VSSH_BIN" LIVE=1 REPLICATE="$REPLICATE" "$JOB" && echo "initial rebuild OK" || echo "initial rebuild FAILED (see log)"
    ;;
  uninstall)
    if is_darwin; then uninstall_launchd; else uninstall_cron; fi
    ;;
  status)
    if is_darwin; then
      launchctl print "gui/$(id -u)/$LABEL" 2>/dev/null | grep -E "state|StartInterval" || echo "not installed"
    else
      crontab -l 2>/dev/null | grep "$CRON_MARK" || echo "not installed"
    fi
    ;;
  *)
    echo "usage: $0 [install|uninstall|status]"; exit 1 ;;
esac
