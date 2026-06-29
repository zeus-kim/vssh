package ssh

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/zeus-kim/vssh/internal/config"
)

type localServerConfig struct {
	IP           string                 `json:"ip"`
	VpnIP        string                 `json:"vpn_ip,omitempty"`
	PublicIP     string                 `json:"public_ip,omitempty"`
	LanIP        string                 `json:"lan_ip,omitempty"`
	Port         int                    `json:"port,omitempty"`
	User         string                 `json:"user,omitempty"`
	MonitorURL   string                 `json:"monitor_url,omitempty"`
	MonitorPort  int                    `json:"monitor_port,omitempty"`
	Tags         []string               `json:"tags,omitempty"`
	Capabilities []string               `json:"capabilities,omitempty"`
	Roles        []string               `json:"roles,omitempty"`
	OS           string                 `json:"os,omitempty"`
	Arch         string                 `json:"arch,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

func (c *Connector) loadPeers() {
	// 1. Try wire daemon first
	if config.IsDaemonRunning() {
		if peers := c.peersFromDaemon(); peers != nil {
			c.peers = c.enrichPeersFromConfig(peers)
			return
		}
	}

	// 2. Try Tailscale (preferred for fresh online status)
	if peers := c.peersFromTailscale(); peers != nil {
		c.peers = c.enrichPeersFromConfig(peers)
		return
	}

	// 3. Try coordinator (fallback)
	if peers := c.peersFromCoordinators(); peers != nil {
		c.peers = c.enrichPeersFromConfig(peers)
		return
	}

	// 4. Try config-based servers
	if peers := c.peersFromConfig(); peers != nil {
		c.peers = peers
		return
	}

	// 5. Fallback to cache
	c.peers = c.enrichPeersFromConfig(c.peersFromCache())
}

func (c *Connector) peersFromCoordinators() []config.Peer {
	urls := c.getCoordinatorURLs()

	for _, url := range urls {
		baseURL := strings.TrimSuffix(url, "/")
		client := &http.Client{Timeout: 5 * time.Second}

		resp, err := client.Get(baseURL + "/peers")
		if err != nil {
			continue
		}

		var result struct {
			Peers []config.Peer `json:"peers"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		if len(result.Peers) > 0 {
			c.savePeersCache(result.Peers)
			return result.Peers
		}
	}

	return nil
}

func (c *Connector) getCoordinatorURLs() []string {
	var urls []string

	// From wire config
	if c.config.ServerURL != "" {
		urls = append(urls, c.config.ServerURL)
	}

	// Default fallback
	if len(urls) == 0 {
		urls = nil
	}

	return urls
}

func (c *Connector) peersFromTailscale() []config.Peer {
	output := tailscaleStatusJSON()
	if output == nil {
		return nil
	}

	var data struct {
		Self struct {
			HostName     string   `json:"HostName"`
			DNSName      string   `json:"DNSName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
		Peer map[string]struct {
			HostName     string   `json:"HostName"`
			DNSName      string   `json:"DNSName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
			Online       bool     `json:"Online"`
		} `json:"Peer"`
	}

	if err := json.Unmarshal(output, &data); err != nil {
		return nil
	}

	var peers []config.Peer

	// Add self
	if len(data.Self.TailscaleIPs) > 0 && data.Self.HostName != "" {
		peers = append(peers, config.Peer{
			NodeName: data.Self.HostName,
			VpnIP:    data.Self.TailscaleIPs[0],
		})
	}

	// Add peers
	for _, p := range data.Peer {
		if len(p.TailscaleIPs) > 0 && p.HostName != "" {
			online := p.Online
			var lastSeen interface{}
			if online {
				lastSeen = time.Now().Unix()
			}
			peers = append(peers, config.Peer{
				NodeName: p.HostName,
				VpnIP:    p.TailscaleIPs[0],
				Online:   &online,
				LastSeen: lastSeen,
			})
		}
	}

	return peers
}

func (c *Connector) peersFromConfig() []config.Peer {
	servers := loadLocalServers()
	if len(servers) == 0 {
		return nil
	}

	var peers []config.Peer
	for name, srv := range servers {
		vpnIP := srv.VpnIP
		if vpnIP == "" {
			vpnIP = srv.IP
		}
		peers = append(peers, config.Peer{
			NodeName:     name,
			VpnIP:        vpnIP,
			PublicIP:     srv.PublicIP,
			LanIP:        srv.LanIP,
			Port:         srv.Port,
			User:         srv.User,
			MonitorURL:   srv.MonitorURL,
			MonitorPort:  srv.MonitorPort,
			Tags:         srv.Tags,
			Capabilities: srv.Capabilities,
			Roles:        srv.Roles,
			OS:           srv.OS,
			Arch:         srv.Arch,
			Metadata:     srv.Metadata,
		})
	}

	return peers
}

func loadLocalServers() map[string]localServerConfig {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	configPath := home + "/.vssh/servers.json"

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil
	}

	var servers map[string]localServerConfig
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil
	}
	return servers
}

func (c *Connector) enrichPeersFromConfig(peers []config.Peer) []config.Peer {
	if len(peers) == 0 {
		return peers
	}
	servers := loadLocalServers()
	if len(servers) == 0 {
		return peers
	}

	for i := range peers {
		name := peers[i].NodeName
		srv, ok := servers[name]
		if !ok {
			for candidate, value := range servers {
				if strings.EqualFold(candidate, name) {
					srv = value
					ok = true
					break
				}
			}
		}
		if !ok {
			continue
		}
		applyLocalServerConfig(&peers[i], srv)
	}
	return peers
}

func applyLocalServerConfig(peer *config.Peer, srv localServerConfig) {
	if peer.VpnIP == "" {
		if srv.VpnIP != "" {
			peer.VpnIP = srv.VpnIP
		} else {
			peer.VpnIP = srv.IP
		}
	}
	if peer.PublicIP == "" {
		peer.PublicIP = srv.PublicIP
	}
	if peer.LanIP == "" {
		peer.LanIP = srv.LanIP
	}
	if peer.Port == 0 {
		peer.Port = srv.Port
	}
	if peer.User == "" {
		peer.User = srv.User
	}
	if peer.MonitorURL == "" {
		peer.MonitorURL = srv.MonitorURL
	}
	if peer.MonitorPort == 0 {
		peer.MonitorPort = srv.MonitorPort
	}
	if peer.OS == "" {
		peer.OS = srv.OS
	}
	if peer.Arch == "" {
		peer.Arch = srv.Arch
	}
	peer.Tags = mergeStringLists(peer.Tags, srv.Tags)
	peer.Capabilities = mergeStringLists(peer.Capabilities, srv.Capabilities)
	peer.Roles = mergeStringLists(peer.Roles, srv.Roles)
	if peer.Metadata == nil {
		peer.Metadata = srv.Metadata
	} else {
		for key, value := range srv.Metadata {
			if _, exists := peer.Metadata[key]; !exists {
				peer.Metadata[key] = value
			}
		}
	}
}

func mergeStringLists(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range append(append([]string{}, a...), b...) {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func (c *Connector) peersFromDaemon() []config.Peer {
	conn, err := net.Dial("unix", config.SocketPath())
	if err != nil {
		return nil
	}
	defer conn.Close()

	// Send peers request
	req := map[string]interface{}{
		"cmd":  "peers",
		"args": c.network,
	}
	data, _ := json.Marshal(req)
	conn.Write(append(data, '\n'))

	// Read response
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil
	}

	var resp struct {
		Success bool          `json:"success"`
		Data    []config.Peer `json:"data"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil
	}
	if !resp.Success {
		return nil
	}

	return resp.Data
}

func (c *Connector) peersFromCache() []config.Peer {
	cachePath := config.WireDir() + "/peers_cache.json"
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil
	}

	var peers []config.Peer
	json.Unmarshal(data, &peers)
	return peers
}

func (c *Connector) savePeersCache(peers []config.Peer) {
	cachePath := config.WireDir() + "/peers_cache.json"
	data, _ := json.MarshalIndent(peers, "", "  ")
	os.WriteFile(cachePath, data, 0600)
}
