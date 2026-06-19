package adapter

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// ExecResult represents command execution result
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// ProbeResult represents a connectivity probe result
type ProbeResult struct {
	Target    string
	Path      string
	Success   bool
	LatencyMs int64
	Error     string
}

// defaultAdapterConfigPath returns the default config path (~/.vssh/vssh.yaml)
func defaultAdapterConfigPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".vssh", "vssh.yaml")
}

// NodeConfig holds per-node connection info
type NodeConfig struct {
	Name      string `yaml:"name"`
	WireIP    string `yaml:"wire_ip"`
	LanIP     string `yaml:"lan_ip"`
	PublicIP  string `yaml:"public_ip"`
	Tailscale string `yaml:"tailscale"`
}

// VSSHAdapter implements TransportAdapter
type VSSHAdapter struct {
	nodes       []NodeConfig
	dialTimeout time.Duration
}

// NewVSSHAdapter creates a new vssh adapter
func NewVSSHAdapter(configPath string) *VSSHAdapter {
	adapter := &VSSHAdapter{
		dialTimeout: 2 * time.Second,
	}

	if configPath == "" {
		configPath = defaultAdapterConfigPath()
	}

	data, err := os.ReadFile(configPath)
	if err == nil {
		var cfg struct {
			Nodes []NodeConfig `yaml:"nodes"`
		}
		if yaml.Unmarshal(data, &cfg) == nil {
			adapter.nodes = cfg.Nodes
		}
	}

	return adapter
}

// Exec executes a command on a remote node.
// Not implemented: product execution belongs to the native vssh server protocol.
func (v *VSSHAdapter) Exec(node string, cmd string) (ExecResult, error) {
	return ExecResult{}, fmt.Errorf("not implemented")
}

// Probe checks connectivity to a node via TCP
func (v *VSSHAdapter) Probe(nodeName string) (ProbeResult, error) {
	var node *NodeConfig
	for i := range v.nodes {
		if v.nodes[i].Name == nodeName {
			node = &v.nodes[i]
			break
		}
	}

	if node == nil {
		return ProbeResult{
			Target:  nodeName,
			Success: false,
			Error:   "node not found",
		}, nil
	}

	start := time.Now()

	// Try paths in order: wire -> lan -> public -> tailscale
	paths := []struct {
		name string
		ip   string
	}{
		{"wire", node.WireIP},
		{"lan", node.LanIP},
		{"public", node.PublicIP},
		{"tailscale", node.Tailscale},
	}

	for _, p := range paths {
		if p.ip == "" {
			continue
		}
		if v.canReach(p.ip, 22) {
			return ProbeResult{
				Target:    nodeName,
				Path:      p.name,
				Success:   true,
				LatencyMs: time.Since(start).Milliseconds(),
			}, nil
		}
	}

	return ProbeResult{
		Target:    nodeName,
		Success:   false,
		LatencyMs: time.Since(start).Milliseconds(),
		Error:     "all paths failed",
	}, nil
}

// ProbeAll probes all configured nodes
func (v *VSSHAdapter) ProbeAll() ([]ProbeResult, error) {
	var results []ProbeResult
	for _, node := range v.nodes {
		result, _ := v.Probe(node.Name)
		results = append(results, result)
	}
	return results, nil
}

func (v *VSSHAdapter) canReach(ip string, port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), v.dialTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// GetNodes returns configured nodes
func (v *VSSHAdapter) GetNodes() []NodeConfig {
	return v.nodes
}
