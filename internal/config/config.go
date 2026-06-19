package config

import (
	"encoding/json"
	"net"
	"os"
	"os/user"
	"path/filepath"
)

// WireConfig represents wire configuration (read-only)
type WireConfig struct {
	ServerURL string                    `json:"server_url"`
	NodeName  string                    `json:"node_name"`
	NodeID    string                    `json:"node_id"`
	Networks  map[string]*NetworkConfig `json:"networks"`
}

// NetworkConfig per-network settings
type NetworkConfig struct {
	Network   string `json:"network"`
	VpnIP     string `json:"vpn_ip"`
	VpnSubnet string `json:"vpn_subnet"`
	Interface string `json:"interface"`
}

// PeerStats contains system stats
type PeerStats struct {
	Load      string  `json:"load,omitempty"`
	LoadValue float64 `json:"load_value,omitempty"`
	MemPct    int     `json:"mem_pct,omitempty"`
	DiskPct   int     `json:"disk_pct,omitempty"`
	Uptime    string  `json:"uptime,omitempty"`
	UpdatedAt int64   `json:"updated_at,omitempty"`
}

// Peer represents a peer node
type Peer struct {
	NodeID       string                 `json:"node_id"`
	NodeName     string                 `json:"node_name"`
	VpnIP        string                 `json:"vpn_ip"`
	PublicIP     string                 `json:"public_ip"`
	LanIP        string                 `json:"lan_ip"`
	Port         int                    `json:"port"`
	User         string                 `json:"user,omitempty"`
	MonitorURL   string                 `json:"monitor_url,omitempty"`
	MonitorPort  int                    `json:"monitor_port,omitempty"`
	Tags         []string               `json:"tags,omitempty"`
	Capabilities []string               `json:"capabilities,omitempty"`
	Roles        []string               `json:"roles,omitempty"`
	OS           string                 `json:"os,omitempty"`
	Arch         string                 `json:"arch,omitempty"`
	Online       *bool                  `json:"online,omitempty"`
	LastSeen     interface{}            `json:"last_seen,omitempty"`
	Stats        *PeerStats             `json:"stats,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// LoadNodeUsers loads user mappings from ~/.wire/users.json
func LoadNodeUsers() map[string]string {
	users := make(map[string]string)

	path := filepath.Join(WireDir(), "users.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return users
	}

	json.Unmarshal(data, &users)
	return users
}

// WireDir returns wire config directory
func WireDir() string {
	if os.Geteuid() == 0 {
		return "/etc/wire"
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		if u, err := user.Current(); err == nil {
			home = u.HomeDir
		}
	}
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".wire")
}

// LoadWireConfig loads wire configuration
func LoadWireConfig() (*WireConfig, error) {
	paths := []string{
		filepath.Join(WireDir(), "config.json"),
		"/etc/wire/config.json",
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg WireConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		return &cfg, nil
	}

	return nil, os.ErrNotExist
}

// SocketPath returns wire daemon socket path
func SocketPath() string {
	if os.Geteuid() == 0 {
		return "/var/run/wire.sock"
	}
	return filepath.Join(WireDir(), "wire.sock")
}

// IsDaemonRunning checks if wire daemon is running
func IsDaemonRunning() bool {
	conn, err := net.Dial("unix", SocketPath())
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
