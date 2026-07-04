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
	// 1. Try Tailscale first (real-time IPs and online status)
	tsPeers := c.peersFromTailscale()

	// 2. Get stats from daemon or coordinator
	var statsPeers []config.Peer
	if config.IsDaemonRunning() {
		statsPeers = c.peersFromDaemon()
	}
	if statsPeers == nil {
		statsPeers = c.peersFromCoordinators()
	}
	if statsPeers == nil {
		statsPeers = c.peersFromCache()
	}

	// 3. Merge: Tailscale base + stats overlay
	if tsPeers != nil {
		merged := c.mergeStats(tsPeers, statsPeers)
		c.peers = c.enrichPeersFromConfig(merged)
		return
	}

	// 4. No Tailscale: use daemon/coordinator/config/cache
	if statsPeers != nil {
		c.peers = c.enrichPeersFromConfig(statsPeers)
		return
	}

	basePeers := c.peersFromConfig()
	if basePeers != nil {
		c.peers = c.enrichPeersFromConfig(basePeers)
		return
	}

	c.peers = nil
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
			OS           string   `json:"OS"`
		} `json:"Peer"`
	}

	if err := json.Unmarshal(output, &data); err != nil {
		return nil
	}

	var peers []config.Peer
	selfName := normalizeHostname(extractTailscaleName(data.Self.DNSName, data.Self.HostName))

	// Add peers (skip self, phones, tablets)
	for _, p := range data.Peer {
		if len(p.TailscaleIPs) == 0 {
			continue
		}
		// Skip phones and tablets
		osLower := strings.ToLower(p.OS)
		if osLower == "ios" || osLower == "android" {
			continue
		}
		nodeName := extractTailscaleName(p.DNSName, p.HostName)
		if nodeName == "" {
			continue
		}
		// Skip current machine
		if normalizeHostname(nodeName) == selfName {
			continue
		}
		online := p.Online
		var lastSeen interface{}
		if online {
			lastSeen = time.Now().Unix()
		}
		peers = append(peers, config.Peer{
			NodeName: nodeName,
			VpnIP:    p.TailscaleIPs[0],
			Online:   &online,
			LastSeen: lastSeen,
		})
	}

	return peers
}

// overlayTailscaleStatus updates peer online status from Tailscale while keeping
// coordinator data (names, IPs, stats). This gives fresh liveness without losing
// the richer metadata from the coordinator.
func (c *Connector) overlayTailscaleStatus(basePeers []config.Peer) []config.Peer {
	output := tailscaleStatusJSON()
	if output == nil {
		return basePeers
	}

	var data struct {
		Peer map[string]struct {
			HostName     string   `json:"HostName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
			Online       bool     `json:"Online"`
		} `json:"Peer"`
	}

	if err := json.Unmarshal(output, &data); err != nil {
		return basePeers
	}

	// Build lookup: both exact match and normalized (alphanumeric lowercase only)
	type tsInfo struct {
		online     bool
		normalized string
	}
	tsStatus := make(map[string]tsInfo)
	for _, p := range data.Peer {
		if p.HostName != "" {
			lower := strings.ToLower(p.HostName)
			normalized := normalizeHostname(lower)
			tsStatus[lower] = tsInfo{online: p.Online, normalized: normalized}
		}
	}

	// Update base peers with Tailscale online status
	now := time.Now().Unix()
	for i := range basePeers {
		name := strings.ToLower(basePeers[i].NodeName)
		normName := normalizeHostname(name)

		// Try exact match first
		if info, found := tsStatus[name]; found {
			basePeers[i].Online = &info.online
			if info.online {
				basePeers[i].LastSeen = now
			}
			continue
		}

		// Try normalized match (e.g., "macmini" matches "odt의 Mac mini")
		for _, info := range tsStatus {
			if strings.Contains(info.normalized, normName) || strings.Contains(normName, info.normalized) {
				basePeers[i].Online = &info.online
				if info.online {
					basePeers[i].LastSeen = now
				}
				break
			}
		}
	}

	return basePeers
}

// extractTailscaleName prefers DNSName (user-set name) over HostName (machine hostname)
func extractTailscaleName(dnsName, hostName string) string {
	// DNSName format: "v1.tail2b6cb8.ts.net." -> extract "v1"
	if dnsName != "" {
		parts := strings.Split(strings.TrimSuffix(dnsName, "."), ".")
		if len(parts) > 0 && parts[0] != "" {
			return parts[0]
		}
	}
	return hostName
}

// normalizeHostname extracts only alphanumeric characters for fuzzy matching
func normalizeHostname(s string) string {
	var result strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// mergeStats overlays stats from daemon/coordinator onto Tailscale peers
func (c *Connector) mergeStats(tsPeers, statsPeers []config.Peer) []config.Peer {
	if statsPeers == nil {
		return tsPeers
	}

	// Build lookup by normalized hostname
	type statsInfo struct {
		peer       *config.Peer
		normalized string
	}
	statsMap := make(map[string]statsInfo)
	for i := range statsPeers {
		norm := normalizeHostname(statsPeers[i].NodeName)
		statsMap[norm] = statsInfo{peer: &statsPeers[i], normalized: norm}
	}

	// Merge stats into Tailscale peers
	for i := range tsPeers {
		tsNorm := normalizeHostname(tsPeers[i].NodeName)

		// Try exact match first
		if info, ok := statsMap[tsNorm]; ok {
			applyStats(&tsPeers[i], info.peer)
			continue
		}

		// Try fuzzy match only for longer names (e.g., "macmini" matches "odt의 Mac mini")
		// Skip fuzzy for short names (<=3 chars) to avoid false matches like s1/s2
		if len(tsNorm) > 3 {
			for _, info := range statsMap {
				if len(info.normalized) > 3 && (strings.Contains(tsNorm, info.normalized) || strings.Contains(info.normalized, tsNorm)) {
					applyStats(&tsPeers[i], info.peer)
					// Use the cleaner name from stats if available
					if len(info.peer.NodeName) < len(tsPeers[i].NodeName) {
						tsPeers[i].NodeName = info.peer.NodeName
					}
					break
				}
			}
		}
	}

	return tsPeers
}

func applyStats(dst, src *config.Peer) {
	dst.Stats = src.Stats
	if dst.Port == 0 {
		dst.Port = src.Port
	}
	if dst.User == "" {
		dst.User = src.User
	}
	if dst.PublicIP == "" {
		dst.PublicIP = src.PublicIP
	}
	if dst.LanIP == "" {
		dst.LanIP = src.LanIP
	}
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

	// Build IP lookup for renaming
	ipToName := make(map[string]string)
	for name, srv := range servers {
		if srv.IP != "" {
			ipToName[srv.IP] = name
		}
		if srv.VpnIP != "" {
			ipToName[srv.VpnIP] = name
		}
	}

	for i := range peers {
		name := peers[i].NodeName

		// Check if IP matches a config entry - use that name (alias)
		if newName, ok := ipToName[peers[i].VpnIP]; ok {
			peers[i].NodeName = newName
			name = newName
		}

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
