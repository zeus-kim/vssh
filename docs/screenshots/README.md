# Screenshots

These screenshots show real Claude + vssh conversations.

## Required Screenshots

| File | Question | What to capture |
|------|----------|-----------------|
| `01-fleet.png` | "Show me my fleet" | Fleet table with hosts, status, tags, capabilities |
| `02-services.png` | "What services are running?" | Service list by category |
| `03-gpu.png` | "Check GPU status" | GPU/CPU hardware table |
| `04-parallel.png` | "Find all services on all servers" | Parallel query across 14 servers |
| `05-reallocation.png` | "Suggest reallocation" | AI analysis + migration recommendations |

## How to capture

1. Run the demo conversation with Claude + vssh
2. Take screenshots of each response
3. Crop to show the question + answer
4. Save as PNG, ~1200px width recommended

## Demo script

```
User: hello
User: show me my fleet
User: what services are running?
User: check GPU status
User: find all services on all servers
User: suggest reallocation
```
