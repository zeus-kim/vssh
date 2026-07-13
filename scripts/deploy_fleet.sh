#!/bin/bash
# deploy_fleet.sh — build vssh for every arch and roll it out to the whole fleet.
#
# Idempotent + safe:
#   * stages to a UNIQUE temp path and removes any stale file/dir first
#     (fixes the bug where a leftover /tmp/vssh-new DIRECTORY made scp nest
#      the binary inside it and the deploy silently used the old binary)
#   * replaces the running binary with an atomic `mv -f` (survives ETXTBSY)
#   * backs up the previous binary to vssh.pre_fix_<date>.bak
#   * restarts the right daemon: systemd (linux/root) or launchd (darwin/user)
#   * verifies multi-line/newline preservation on each node after restart
#   * skips offline / unresolved / unknown-arch nodes without aborting the run
#
# Usage:
#   scripts/deploy_fleet.sh                 # build + deploy to default NODES
#   NODES="node1 node2" scripts/deploy_fleet.sh
#   SKIP_BUILD=1 scripts/deploy_fleet.sh    # reuse existing /tmp/vssh-* binaries
set -u

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
STAMP="$(date +%Y%m%d)"
NODES="${NODES:-node1 node2 node3}"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=8"
# -O forces the legacy SCP transfer protocol. Modern scp defaults to SFTP, which
# fails on nodes whose SFTP subsystem is old/absent (e.g. Synology: "dest open
# ...: No such file or directory"). -O is a client-side flag and harmless on
# every other node, so it makes one deploy path cover the whole fleet.
SCP="scp -O -q -o BatchMode=yes -o ConnectTimeout=8"

BIN_LINUX_AMD64=/tmp/vssh-linux-amd64
BIN_LINUX_ARM64=/tmp/vssh-linux-arm64
BIN_DARWIN_ARM64=/tmp/vssh-darwin-arm64

build() {
  echo "== building (from $REPO_DIR) =="
  ( cd "$REPO_DIR" || exit 1
    CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -o "$BIN_LINUX_AMD64"  ./cmd/vssh || exit 1
    CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -o "$BIN_LINUX_ARM64"  ./cmd/vssh || exit 1
    # darwin must be built natively on a mac for the MCP-attach to work; adhoc-sign it
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$BIN_DARWIN_ARM64" ./cmd/vssh || exit 1
    codesign --force --sign - "$BIN_DARWIN_ARM64" 2>/dev/null
  ) || { echo "BUILD FAILED"; exit 1; }
  echo "built: $(ls -la $BIN_LINUX_AMD64 $BIN_LINUX_ARM64 $BIN_DARWIN_ARM64 2>/dev/null | awk '{print $NF":"$5}')"
}

verify_newline() {  # $1 = node, $2 = remote vssh path, $3 = optional cmd prefix (sudo)
  # $3=sudo runs the check with the node's ROOT identity — required on nodes
  # with VSSH_REQUIRE_VAUTH=1, where the plain ssh user's key is not authorized.
  local n="$1" vbin="$2" pfx="${3:-}"
  local res
  res=$($SSH "$n" "$pfx $vbin run localhost \"\$(printf 'echo A\necho B\necho C')\" 2>/dev/null | tr '\n' '/'")
  case "$res" in *A/B/C*) echo "NEWLINE_OK";; *) echo "NEWLINE_FAIL[$res]";; esac
}

deploy_linux() {  # $1=node $2=localbin
  local n="$1" bin="$2" stage="/tmp/vssh.deploy.$STAMP.$$"
  $SSH "$n" "rm -rf '$stage'" 2>/dev/null            # guard: never let stage be a dir
  $SCP "$bin" "$n:$stage" 2>/dev/null || { echo "[$n] scp fail"; return; }
  $SSH "$n" "test -f '$stage' || exit 9
    sudo cp -p /usr/local/bin/vssh /usr/local/bin/vssh.pre_fix_$STAMP.bak 2>/dev/null
    sudo cp '$stage' /usr/local/bin/vssh.staged && sudo chmod 755 /usr/local/bin/vssh.staged \
      && sudo mv -f /usr/local/bin/vssh.staged /usr/local/bin/vssh \
      && sudo systemctl restart vsshd && sleep 2 && rm -f '$stage'" >/dev/null 2>&1 \
    || { echo "[$n] deploy fail"; return; }
  echo "[$n] $($SSH "$n" '/usr/local/bin/vssh --version 2>/dev/null|head -1') | $(verify_newline "$n" /usr/local/bin/vssh sudo)"
}

deploy_darwin() {  # $1=node $2=localbin  (user launchd daemon ai.vssh.vsshd)
  local n="$1" bin="$2"
  local vpath; vpath=$($SSH "$n" 'echo $HOME/.local/bin/vssh')
  local stage="$vpath.deploy.$STAMP"
  $SSH "$n" "rm -rf '$stage'" 2>/dev/null
  $SCP "$bin" "$n:$stage" 2>/dev/null || { echo "[$n] scp fail"; return; }
  $SSH "$n" "test -f '$stage' || exit 9
    cp '$vpath' '$vpath.pre_fix_$STAMP.bak' 2>/dev/null
    codesign --force --sign - '$stage' 2>/dev/null && chmod 755 '$stage' && mv -f '$stage' '$vpath' \
      && launchctl kickstart -k gui/\$(id -u)/ai.vssh.vsshd && sleep 2" >/dev/null 2>&1 \
    || { echo "[$n] deploy fail"; return; }
  echo "[$n] $($SSH "$n" "$vpath --version 2>/dev/null|head -1") | $(verify_newline "$n" "$vpath")"
}

[ "${SKIP_BUILD:-0}" = 1 ] || build

echo "== deploying to: $NODES =="
for n in $NODES; do
  info=$($SSH "$n" 'echo "$(uname -s) $(uname -m)"' 2>/dev/null)
  if [ -z "$info" ]; then echo "[$n] OFFLINE/unreachable — skip"; continue; fi
  case "$info" in
    "Linux x86_64")  deploy_linux  "$n" "$BIN_LINUX_AMD64" ;;
    "Linux aarch64") deploy_linux  "$n" "$BIN_LINUX_ARM64" ;;
    "Darwin arm64")  deploy_darwin "$n" "$BIN_DARWIN_ARM64" ;;
    *) echo "[$n] unknown arch ($info) — skip" ;;
  esac
done
echo "== DONE =="
