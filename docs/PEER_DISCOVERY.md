# Peer Discovery

vssh discovers nodes from multiple sources.

## Tailscale (Recommended)

If [Tailscale](https://tailscale.com) is installed, vssh automatically discovers peers from your Tailscale network.

**Benefits:**
- Zero configuration - all nodes in your Tailscale network are discovered automatically
- Real-time online status
- Automatic NAT traversal

## Manual Configuration

To use vssh without Tailscale, define nodes in `~/.vssh/servers.json`:

```json
{
  "my-server": {
    "ip": "192.168.1.100",
    "port": 48291,
    "tags": ["linux", "docker"]
  },
  "another-server": {
    "ip": "192.168.1.101",
    "port": 48291
  }
}
```

---

## Quick Start

### With Tailscale (Recommended)

```bash
# 1. Install Tailscale on each node and log in
curl -fsSL https://tailscale.com/install.sh | sh
tailscale up

# 2. Install vssh on each node
curl -sSL https://raw.githubusercontent.com/zeus-kim/vssh/main/install.sh | bash

# 3. Start vssh server on each node
vssh server &

# 4. Check status
vssh status
```

### Without Tailscale

```bash
# 1. Install vssh
curl -sSL https://raw.githubusercontent.com/zeus-kim/vssh/main/install.sh | bash

# 2. Configure server list
cat > ~/.vssh/servers.json << 'EOF'
{
  "server1": {"ip": "192.168.1.100", "port": 48291},
  "server2": {"ip": "192.168.1.101", "port": 48291}
}
EOF

# 3. Start vssh server on each node
vssh server &

# 4. Check status
vssh status
```

---

## Getting Node Information

### Basic Status

```bash
vssh status
```

### Detailed System Info

```bash
vssh facts <node>
```

Returns hostname, OS, CPU count, memory, load average, disk usage, etc.

---

## Troubleshooting

### Node shows as offline

1. Verify `vssh server` is running on the target node
2. Check network connectivity: `ping <node-ip>`
3. Check port: `nc -zv <node-ip> 48291`

### Tailscale nodes not appearing

```bash
# Check Tailscale status
tailscale status

# Verify Tailscale is in PATH
which tailscale
```
