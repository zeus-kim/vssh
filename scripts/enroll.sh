#!/usr/bin/env bash
# enroll.sh — one-command node onboarding (P1a; DESIGN_RATIONALE §4 item 4).
# From the controller, idempotently bring a node to a fully-enrolled state:
#   1. detect reachability + OS/arch
#   2. install/upgrade the vssh binary (atomic mv; sudo on linux)
#   3. install + enable the daemon service if missing, with strict VAUTH
#      (systemd vsshd / launchd ai.vssh.vsshd ; VSSH_REQUIRE_VAUTH=1)
#   4. cross-authorize node <-> controller keys (additive, idempotent)
#   5. TOFU-pin the node's daemon key into the controller ~/.vssh/known_hosts
#   6. verify: controller TLS+VAUTH1 handshake AUTH_OK + agent_suite ALL GREEN
#
# Usage:
#   scripts/enroll.sh <node>
#   DRY_RUN=1 scripts/enroll.sh <node>          # print plan, change nothing
#   SKIP_BUILD=1 scripts/enroll.sh <node>
set -uo pipefail
NODE="${1:?usage: enroll.sh <node>}"
# Key-only (P4): no shared secret. Authorization is via cross-authorized Ed25519 keys.
DRY="${DRY_RUN:-0}"
REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=8"
SCP="scp -q -o BatchMode=yes -o ConnectTimeout=8"
VSSH="${VSSH:-/opt/homebrew/bin/vssh}"; [ -x "$VSSH" ] || VSSH="$(command -v vssh)"
say(){ echo "[$NODE] $*"; }

# 1. detect ---------------------------------------------------------------
INFO=$($SSH "$NODE" 'echo "$(uname -s) $(uname -m)"' 2>/dev/null) || true
[ -n "$INFO" ] || { say "OFFLINE/unreachable — abort"; exit 1; }
set -- $INFO; OS="$1"; MACH="$2"
case "$OS/$MACH" in
  Linux/x86_64)  GOOS=linux;  GOARCH=amd64; DARWIN=0 ;;
  Linux/aarch64) GOOS=linux;  GOARCH=arm64; DARWIN=0 ;;
  Darwin/arm64)  GOOS=darwin; GOARCH=arm64; DARWIN=1 ;;
  *) say "unsupported platform ($INFO) — abort"; exit 1 ;;
esac
if [ "$DARWIN" = 1 ]; then REMOTE_BIN="$($SSH "$NODE" 'echo $HOME/.local/bin/vssh')"; else REMOTE_BIN=/usr/local/bin/vssh; fi
say "platform=$OS/$MACH bin=$REMOTE_BIN"

# 2. binary ---------------------------------------------------------------
LOCALBIN="/tmp/vssh-$GOOS-$GOARCH"
if [ "${SKIP_BUILD:-0}" != 1 ] || [ ! -f "$LOCALBIN" ]; then
  ( cd "$REPO_DIR" && CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH go build -o "$LOCALBIN" ./cmd/vssh ) || { say "build FAIL"; exit 1; }
  [ "$DARWIN" = 1 ] && codesign --force --sign - "$LOCALBIN" 2>/dev/null
fi
# version comes from source (the cross-compiled binary can't run on the controller arch)
WANT=$(grep -oE '"[0-9]+\.[0-9]+\.[0-9]+"' "$REPO_DIR/cmd/vssh/main.go" | head -1 | tr -d '"')
if [ "$DARWIN" = 1 ]; then HAVE=$($SSH "$NODE" "$REMOTE_BIN version 2>/dev/null | head -1" 2>/dev/null)
else HAVE=$($SSH "$NODE" "sudo $REMOTE_BIN version 2>/dev/null | head -1" 2>/dev/null); fi
HAVEV=$(printf '%s' "$HAVE" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
if [ -n "$WANT" ] && [ "$WANT" = "$HAVEV" ]; then say "binary current ($HAVE)"
elif [ "$DRY" = 1 ]; then say "DRY: would install $WANT (was: ${HAVE:-none})"
else
  say "installing $WANT (was: ${HAVE:-none})"
  stage="/tmp/vssh.enroll.$$"
  $SSH "$NODE" "rm -rf '$stage'" 2>/dev/null
  $SCP "$LOCALBIN" "$NODE:$stage" || { say "scp FAIL"; exit 1; }
  if [ "$DARWIN" = 1 ]; then
    $SSH "$NODE" "mkdir -p \"\$(dirname '$REMOTE_BIN')\"; codesign --force --sign - '$stage' 2>/dev/null; chmod 755 '$stage'; mv -f '$stage' '$REMOTE_BIN'"
  else
    $SSH "$NODE" "sudo mkdir -p /usr/local/bin; sudo cp '$stage' /usr/local/bin/vssh.enroll && sudo chmod 755 /usr/local/bin/vssh.enroll && sudo mv -f /usr/local/bin/vssh.enroll '$REMOTE_BIN'; rm -f '$stage'"
  fi
fi

# 3. service (idempotent; always (re)assert strict VAUTH) -----------------
if [ "$DARWIN" = 1 ]; then
  if [ "$DRY" = 1 ]; then say "DRY: would ensure launchd ai.vssh.vsshd (+strict)"
  else
    NHOME=$($SSH "$NODE" 'echo $HOME')
    PL=/tmp/ai.vssh.vsshd.$$.plist
    cat > "$PL" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>ai.vssh.vsshd</string>
  <key>ProgramArguments</key><array><string>$REMOTE_BIN</string><string>server</string></array>
  <key>EnvironmentVariables</key><dict>
    <key>VSSH_REQUIRE_VAUTH</key><string>1</string>
  </dict>
  <key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$NHOME/Library/Logs/vsshd.log</string>
  <key>StandardErrorPath</key><string>$NHOME/Library/Logs/vsshd.log</string>
</dict></plist>
PLIST
    $SCP "$PL" "$NODE:$NHOME/Library/LaunchAgents/ai.vssh.vsshd.plist" && rm -f "$PL"
    $SSH "$NODE" 'launchctl bootout gui/$(id -u)/ai.vssh.vsshd 2>/dev/null; launchctl bootstrap gui/$(id -u) $HOME/Library/LaunchAgents/ai.vssh.vsshd.plist' 2>/dev/null
    say "launchd service asserted (+strict)"
  fi
else
  if [ "$DRY" = 1 ]; then say "DRY: would ensure systemd vsshd unit (+require_vauth drop-in) and restart"
  else
    $SSH "$NODE" "sudo bash -s" <<EOS
set -e
mkdir -p /etc/systemd/system/vsshd.service.d
if ! systemctl cat vsshd >/dev/null 2>&1; then
cat > /etc/systemd/system/vsshd.service <<UNIT
[Unit]
Description=VSSH Server Daemon
After=network.target
[Service]
Type=simple
ExecStart=/usr/local/bin/vssh server
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT
fi
cat > /etc/systemd/system/vsshd.service.d/require_vauth.conf <<DROP
[Service]
Environment=VSSH_REQUIRE_VAUTH=1
DROP
systemctl daemon-reload
systemctl enable vsshd >/dev/null 2>&1 || true
systemctl restart vsshd
EOS
    say "systemd vsshd asserted (+strict)"
  fi
fi
[ "$DRY" = 1 ] || sleep 2

# 4. cross-authorize node <-> controller ----------------------------------
if [ "$DRY" = 1 ]; then say "DRY: would cross-authorize node<->controller keys"
else
  CTRL_PUB=$("$VSSH" pubkey 2>/dev/null | tail -1)
  if [ "$DARWIN" = 1 ]; then
    NODE_PUB=$($SSH "$NODE" "$REMOTE_BIN pubkey 2>/dev/null | tail -1")
    $SSH "$NODE" "mkdir -p ~/.vssh; touch ~/.vssh/authorized_keys; grep -q '$CTRL_PUB' ~/.vssh/authorized_keys || echo '$CTRL_PUB m1' >> ~/.vssh/authorized_keys"
  else
    NODE_PUB=$($SSH "$NODE" "sudo $REMOTE_BIN pubkey 2>/dev/null | tail -1")
    $SSH "$NODE" "sudo mkdir -p /etc/vssh; sudo touch /etc/vssh/authorized_keys; sudo grep -q '$CTRL_PUB' /etc/vssh/authorized_keys || echo '$CTRL_PUB m1' | sudo tee -a /etc/vssh/authorized_keys >/dev/null"
  fi
  mkdir -p ~/.vssh; touch ~/.vssh/authorized_keys
  if [ -n "$NODE_PUB" ]; then grep -q "$NODE_PUB" ~/.vssh/authorized_keys || echo "$NODE_PUB $NODE" >> ~/.vssh/authorized_keys; fi
  say "keys cross-authorized"
fi

# 5. pin known_hosts (TOFU) ----------------------------------------------
if [ "$DRY" = 1 ]; then say "DRY: would TOFU-pin node key into ~/.vssh/known_hosts"
else "$VSSH" handshake-test --tls "$NODE" >/dev/null 2>&1 || true; say "known_hosts pinned"; fi

# 6. verify ---------------------------------------------------------------
if [ "$DRY" = 1 ]; then say "DRY COMPLETE (no changes made)"; exit 0; fi
hs=$("$VSSH" handshake-test --tls "$NODE" 2>/dev/null | grep -c AUTH_OK || true)
suite=$(VSSH="$VSSH" HOST="$NODE" bash "$REPO_DIR/test/agent_suite.sh" 2>&1 | tail -1)
if [ "${hs:-0}" -ge 1 ] && printf '%s' "$suite" | grep -q "ALL GREEN"; then
  say "ENROLLED OK — TLS AUTH_OK, agent_suite GREEN"
else
  say "ENROLL INCOMPLETE (handshake_AUTH_OK=$hs, suite_tail=[$suite])"; exit 1
fi
