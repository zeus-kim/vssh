#!/bin/bash
# Demo recording script for README

type_slow() {
    text="$1"
    for ((i=0; i<${#text}; i++)); do
        printf "%s" "${text:$i:1}"
        sleep 0.05
    done
    echo
}

clear
echo ""
echo "  Claude + vssh"
echo ""
sleep 1

# 1. Show me my fleet
printf "  > "
type_slow "Show me my fleet"
sleep 0.5
cat << 'EOF'

  | Host | Status | Tags              | Capabilities        |
  |------|--------|-------------------|---------------------|
  | d1   | 🟢     | ai-workload, gpu  | cuda, docker, ollama|
  | d2   | 🟢     | docker-host, gpu  | cuda, docker, ollama|
  | g1   | 🟢     | docker-host, gpu  | cuda, docker, ollama|
  | g2   | 🟢     | docker-host, gpu  | cuda, docker, ollama|
  | g3   | 🟢     | docker-host, gpu  | cuda, docker, ollama|
  | g4   | 🟢     | docker-host, gpu  | cuda, docker, ollama|
  | v1   | 🟢     | vps               | linux, server       |
  | ...  |        |                   | +7 more hosts       |

  14 online, 2 stale

EOF
sleep 2

# 2. What services are running?
printf "  > "
type_slow "What services are running?"
sleep 0.5
cat << 'EOF'

  | Service      | Hosts              |
  |--------------|--------------------|
  | ollama       | d1, d2, g1-g4      |
  | open-webui   | g1, g3, g4         |
  | flowise      | g1                 |
  | langflow     | g1                 |
  | meilisearch  | g1                 |
  | bookstack    | c2                 |

  17 service instances across 14 servers

EOF
sleep 2

# 3. Check GPU status
printf "  > "
type_slow "Check GPU status"
sleep 0.5
cat << 'EOF'

  | Host | GPU               | VRAM        | Util |
  |------|-------------------|-------------|------|
  | d1   | 2x RTX A6000      | 62/98 GB    | idle |
  | d2   | RTX 4090          | 17/24 GB    | 88%  |
  | g1   | NVIDIA GB10       | unified     | idle |
  | g2   | NVIDIA GB10       | unified     | idle |
  | g3   | NVIDIA GB10       | unified     | idle |
  | g4   | NVIDIA GB10       | unified     | idle |

  7 GPUs total, 610 GB VRAM

EOF
sleep 2

# 4. Find all services on all servers
printf "  > "
type_slow "Find all services on all servers"
sleep 0.5
echo ""
echo "  Querying 14 servers in parallel..."
sleep 0.3
cat << 'EOF'

  | Host | Services                                    |
  |------|---------------------------------------------|
  | d1   | ollama                                      |
  | d2   | ollama                                      |
  | g1   | ollama, open-webui, flowise, langflow, ... |
  | g2   | ollama                                      |
  | g3   | ollama, open-webui, rsshub                  |
  | g4   | ollama, open-webui                          |
  | c2   | bookstack, signald                          |
  | v1-5 | vssh-daemon (relay)                         |

  14 servers scanned in 2.3s

EOF
sleep 2

# 5. Suggest reallocation
printf "  > "
type_slow "Suggest reallocation"
sleep 0.5
cat << 'EOF'

  g1 is overloaded (8 services). Recommend:

  | Move        | From | To | Reason                |
  |-------------|------|----|-----------------------|
  | flowise     | g1   | d1 | d1 has 56 idle cores  |
  | langflow    | g1   | d1 | CPU-heavy, not GPU    |
  | meilisearch | g1   | g3 | Balance I/O           |
  | anythingllm | g1   | g2 | g2 is empty           |

  Execute migration? [y/N]

EOF
sleep 3

echo ""
echo "  vssh — The AI Execution Runtime"
echo "  github.com/zeus-kim/vssh"
echo ""
sleep 2
