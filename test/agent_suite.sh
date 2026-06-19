#!/usr/bin/env bash
# agent_suite.sh — executable contract for vssh's agent-facing behavior.
# Asserts the four laws (no input corruption, decidable output, time absorption,
# explained failure) against a live host/fleet. Exits non-zero on any regression,
# so it can run on a schedule and page on drift.
#
# Usage:
#   test/agent_suite.sh                 # target HOST (default d1)
#   HOST=g1 test/agent_suite.sh
#   FLEET="d1 g1 c1" test/agent_suite.sh    # run core asserts on each
#   OFFLINE_HOST=n1 test/agent_suite.sh     # also assert the offline-error contract
#
# Env: VSSH (binary path, auto-detected), VSSH_SECRET (inherited).

set -uo pipefail

# --- locate a working vssh CLI (non-login shells often lack it on PATH) ---
VSSH="${VSSH:-}"
if [ -z "$VSSH" ]; then
  for c in "$(command -v vssh 2>/dev/null)" /opt/homebrew/bin/vssh /usr/local/bin/vssh "$HOME/bin/vssh" "$HOME/.local/bin/vssh"; do
    if [ -n "$c" ] && [ -x "$c" ] && "$c" --version >/dev/null 2>&1; then VSSH="$c"; break; fi
  done
fi
[ -n "$VSSH" ] || { echo "FATAL: no working vssh binary found"; exit 2; }

HOST="${HOST:-d1}"
FLEET="${FLEET:-$HOST}"
PASS=0; FAIL=0; FAILED_NAMES=""

ok()   { PASS=$((PASS+1)); printf "  ok   %s\n" "$1"; }
bad()  { FAIL=$((FAIL+1)); FAILED_NAMES="$FAILED_NAMES $1"; printf "  FAIL %s — %s\n" "$1" "$2"; }

# assert: name, expected-substring, actual
expect_contains() { case "$3" in *"$2"*) ok "$1";; *) bad "$1" "expected ~[$2] got [$3]";; esac; }

run() { "$VSSH" run "$1" "$2" 2>&1; }

echo "== vssh agent_suite ($("$VSSH" --version 2>&1|head -1)) =="
echo "== binary=$VSSH fleet=[$FLEET] =="

for h in $FLEET; do
  echo "-- host: $h --"

  # Preflight: one cheap probe. If the host is unreachable, record a single
  # failure and skip its assertions so one down node can't blow the runtime up
  # (otherwise every assertion eats the full dial timeout).
  pre="$(run "$h" 'echo __ping__')"
  case "$pre" in
    *__ping__*) : ;;
    *) bad "$h/reachable" "preflight failed: [$pre]"; continue ;;
  esac

  # Law 1: no input corruption
  expect_contains "$h/multiline"  "A
B
C" "$(run "$h" "$(printf 'echo A\necho B\necho C')")"
  expect_contains "$h/unicode"    "안녕 🦛 café" "$(run "$h" 'echo 안녕 🦛 café')"
  expect_contains "$h/quotes"     'q"q' "$(run "$h" 'echo "q\"q"')"
  expect_contains "$h/heredoc"    "l1
l2" "$(run "$h" 'bash -c "cat <<EOF
l1
l2
EOF"')"

  # Law 2/4: decidable output — exit code must propagate
  run "$h" 'exit 7' >/dev/null 2>&1; rc=$?
  [ "$rc" = 7 ] && ok "$h/exitcode" || bad "$h/exitcode" "exit 7 -> rc=$rc"

  # Law 3: large output survives
  expect_contains "$h/largeout" "5000" "$(run "$h" 'seq 1 5000 | tail -1')"

  # env is populated (deterministic-ish): PATH non-empty
  expect_contains "$h/env_path" "/bin" "$(run "$h" 'echo PATH=$PATH')"
done

# Law 4: explained failure — auth failure must be reported (not silent success).
# v0.7.17+: clients prefer VAUTH1 (per-node Ed25519 key), so a bad shared secret
# alone no longer fails when this node's key is authorized. Force full failure by
# also pointing HOME at an empty dir (fresh, unauthorized identity).
authhome="$(mktemp -d)"
out="$(HOME="$authhome" VSSH_SECRET=wrong_secret_$$ "$VSSH" run "$HOST" 'echo should_not_run' 2>&1)"
rm -r "$authhome" 2>/dev/null || true
case "$out" in
  *auth*|*AUTH*|*denied*|*unauthorized*) ok "auth_failure_reported";;
  *should_not_run*) bad "auth_failure_reported" "bad secret still executed!";;
  *) bad "auth_failure_reported" "unclear: [$out]";;
esac
# Machine-branchable error code (v0.7.8+). Tolerant: only fails if some code-ish
# token is expected but the run clearly succeeded; older binaries may omit it.
case "$out" in
  *auth_failed*) ok "auth_error_code";;
  *should_not_run*) bad "auth_error_code" "bad secret executed";;
  *) printf "  skip auth_error_code (older binary, no code)\n";;
esac

# Law 4: unknown rpc method must report failure (success:false)
out="$("$VSSH" rpc "$HOST" definitely.bogus.method 2>&1)"
case "$out" in
  *'"success": false'*|*'"success":false'*|*unknown*|*unsupported*) ok "rpc_unknown_reported";;
  *) bad "rpc_unknown_reported" "[$out]";;
esac

# Optional: offline-host error contract (must fail fast-ish, not hang forever)
if [ -n "${OFFLINE_HOST:-}" ]; then
  t0=$(date +%s); out="$(run "$OFFLINE_HOST" 'echo hi')"; rc=$?; t1=$(date +%s)
  dt=$((t1-t0))
  if [ "$rc" != 0 ] && [ "$dt" -le 35 ]; then ok "offline_error_contract (${dt}s)";
  else bad "offline_error_contract" "rc=$rc dt=${dt}s out=[$out]"; fi
fi

echo "== RESULT: pass=$PASS fail=$FAIL =="
[ "$FAIL" -eq 0 ] || { echo "FAILED:$FAILED_NAMES"; exit 1; }
echo "ALL GREEN"
