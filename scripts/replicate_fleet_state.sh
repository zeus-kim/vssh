#!/bin/bash
# replicate_fleet_state.sh — push the controller's signed fleet_state.json to nodes
# as read-only durability copies (~/.vssh/fleet_state.json on each node).
# The controller is the only author; nodes hold verifiable, timestamped replicas.
set -u
SRC="${SRC:-$HOME/.vssh/fleet_state.json}"
NODES="${*:-${NODES:-node1 node2 node3}}"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=8"
SCP="scp -q -o BatchMode=yes -o ConnectTimeout=8"
[ -f "$SRC" ] || { echo "no fleet_state at $SRC — run: vssh fleet-state build"; exit 1; }
echo "== replicate $SRC -> <node>:~/.vssh/fleet_state.json =="
for n in $NODES; do
  $SSH "$n" 'mkdir -p "$HOME/.vssh"' 2>/dev/null || { echo "[$n] OFFLINE — skip"; continue; }
  if $SCP "$SRC" "$n:.vssh/fleet_state.json" 2>/dev/null; then echo "[$n] replicated"; else echo "[$n] FAILED"; fi
done
echo "== DONE =="
