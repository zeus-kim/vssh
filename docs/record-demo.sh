#!/bin/bash
# Demo recording script - vssh features showcase

type_slow() {
    text="$1"
    for ((i=0; i<${#text}; i++)); do
        printf "%s" "${text:$i:1}"
        sleep 0.04
    done
    echo
}

clear
echo ""
echo "  Claude + vssh"
echo ""
sleep 1

# 1. Fleet overview
printf "  > "
type_slow "Show me my fleet"
sleep 0.3
cat << 'EOF'

  | Host | Status | Role          | Mem  | Uptime |
  |------|--------|---------------|------|--------|
  | d1   | 🟢     | GPU workstation| 13% | 32d    |
  | d2   | 🟢     | GPU workstation| 52% | 10d    |
  | g1   | 🟢     | GB10 superchip | 15% | 36d    |
  | g2   | 🟢     | GB10 superchip | 57% | 36d    |
  | g3   | 🟢     | GB10 superchip | 8%  | 19d    |
  | g4   | 🟢     | GB10 superchip | 14% | 36d    |
  | v1-5 | 🟢     | VPS relay      | ~30%| 100d+  |

  14 online · 2 stale · 6 GPU nodes

EOF
sleep 1.5

# 2. Parallel query (vssh feature)
printf "  > "
type_slow "What services on all servers?"
sleep 0.3
echo ""
echo "  ⚡ Querying 14 servers in parallel..."
sleep 0.4
cat << 'EOF'

  | Host | Services                              |
  |------|---------------------------------------|
  | g1   | ollama, open-webui, flowise, langflow |
  | g3   | ollama, open-webui, rsshub            |
  | g4   | ollama, open-webui                    |
  | c2   | bookstack, signald                    |
  | d1-2 | ollama                                |
  | g2   | ollama                                |

  14 servers queried in 2.1s (parallel execution)

EOF
sleep 1.5

# 3. Security check (vssh feature - structured results)
printf "  > "
type_slow "Security status?"
sleep 0.3
cat << 'EOF'

  | Host | fail2ban | blocked | TLS   | Auth    |
  |------|----------|---------|-------|---------|
  | d1   | ✅ active| 3 IPs   | 1.3   | Ed25519 |
  | d2   | ✅ active| 12 IPs  | 1.3   | Ed25519 |
  | g1   | ✅ active| 0 IPs   | 1.3   | Ed25519 |
  | v1   | ✅ active| 47 IPs  | 1.3   | Ed25519 |
  | v2   | ✅ active| 23 IPs  | 1.3   | Ed25519 |

  All nodes: key-only · no passwords · hash-chained audit

EOF
sleep 1.5

# 4. Fleet Memory (vssh feature)
printf "  > "
type_slow "What do you know about g1?"
sleep 0.3
cat << 'EOF'

  📝 Fleet Memory for g1:

  Role: Primary AI development server
  Services: ollama, open-webui, flowise, langflow
  GPU: NVIDIA GB10 (Grace Blackwell)

  Notes:
  • [Jun 25] Deployed qwen2.5:72b
  • [Jun 20] Added langflow for workflows
  • [Jun 15] Docker cleanup, freed 200GB

EOF
sleep 1.5

# 5. Intent (vssh feature - natural language)
printf "  > "
type_slow "Restart nginx on all web servers"
sleep 0.3
cat << 'EOF'

  🎯 Intent → Plan

  Command: systemctl restart nginx
  Targets: v1, v2 (tagged: web)
  Policy:  ✅ allowed (service restart)

  ┌──────┬─────────────────────────┬──────────┐
  │ Host │ Command                 │ Policy   │
  ├──────┼─────────────────────────┼──────────┤
  │ v1   │ systemctl restart nginx │ ✅ allow │
  │ v2   │ systemctl restart nginx │ ✅ allow │
  └──────┴─────────────────────────┴──────────┘

  Execute on 2 hosts? [y/N]

EOF
sleep 1.5

# 6. Policy block (vssh feature - safety)
printf "  > "
type_slow "Delete all logs on d1"
sleep 0.3
cat << 'EOF'

  🚫 Blocked by policy

  Command: rm -rf /var/log/*
  Reason:  Destructive operation requires approval

  vssh blocks dangerous commands by default:
  • rm -rf, shutdown, reboot
  • docker rm, kubectl delete
  • dd, mkfs, format

  Set allow_dangerous: true to override.

EOF
sleep 1.5

# 7. Audit diff (vssh feature)
printf "  > "
type_slow "What changed this week?"
sleep 0.3
cat << 'EOF'

  📋 Audit Diff (Jun 21-27)

  | Date   | Host | Action                  | Key        |
  |--------|------|-------------------------|------------|
  | Jun 27 | g1   | docker restart flowise  | admin      |
  | Jun 26 | d2   | ollama pull gemma4      | deploy     |
  | Jun 25 | g1   | systemctl restart nginx | admin      |
  | Jun 24 | v1   | apt upgrade             | cron       |

  12 actions · 3 keys · 0 policy violations
  Hash chain: a3f2c1...verified ✅

EOF
sleep 1.5

# 8. Optimization
printf "  > "
type_slow "Suggest optimizations"
sleep 0.3
cat << 'EOF'

  📊 Analysis

  Issues:
  • g1 overloaded (8 services)
  • d1 underused (56 cores idle)
  • d1 disk 88% full

  Recommendations:
  | Move flowise  | g1 → d1 | Use idle CPUs    |
  | Move langflow | g1 → d1 | Balance load     |
  | Cleanup d1    | —       | Free 200GB       |

  Execute migration workflow? [y/N]

EOF
sleep 2

echo ""
echo "  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  vssh — The AI Execution Runtime"
echo ""
echo "  ✓ Fleet Memory    AI remembers your infra"
echo "  ✓ Intent          Natural language → commands"
echo "  ✓ Policy          Dangerous commands blocked"
echo "  ✓ Audit           Hash-chained, tamper-evident"
echo "  ✓ Evidence        Structured results, not text"
echo ""
echo "  github.com/zeus-kim/vssh"
echo "  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
sleep 2
