# vssh Examples

Sample files demonstrating vssh configuration and output formats.

## Files

| File | Description |
|------|-------------|
| `fleet.sample.json` | Node inventory (`~/.vssh/servers.json` format) |
| `memory.sample.json` | Fleet memory showing per-node context |
| `workflow.sample.yaml` | Multi-step deployment workflow definition |
| `nvidia-smi.result.json` | Structured execution evidence (what vssh returns) |

## Notes

- **IP addresses are examples** from RFC 5737 (192.0.2.x). Replace with your actual hosts.
- **No real credentials** are included. Actual keys go in `~/.vssh/`.
- These files are for documentation purposes. See [docs/MANUAL.md](../docs/MANUAL.md) for full reference.

## Quick Start

```bash
# Copy sample inventory
cp fleet.sample.json ~/.vssh/servers.json

# Edit with your actual hosts
$EDITOR ~/.vssh/servers.json

# Verify setup
vssh doctor
vssh list
```
