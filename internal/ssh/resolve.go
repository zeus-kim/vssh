package ssh

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/zeus-kim/vssh/internal/config"
)

type endpoint struct {
	host string
	user string
	desc string
}

func (c *Connector) getEndpoints(peer *config.Peer) []endpoint {
	var eps []endpoint

	// Get user from peer or users.json mapping
	users := config.LoadNodeUsers()
	user := lookupNodeUser(users, peer.NodeName)
	if user == "" {
		user = peer.User
	}

	// 1. Tailscale IP. In day-to-day AI operator workflows this is usually
	// the most reliable route because device identity, ACLs, and mobile/desktop
	// reachability are already handled by Tailscale. Keep Wire/VPN metadata for
	// status, but prefer Tailscale for SSH execution when available.
	if tsIP := tailscaleIPForHost(peer.NodeName); tsIP != "" {
		eps = append(eps, endpoint{
			host: tsIP,
			user: user,
			desc: fmt.Sprintf("Tailscale (%s)", tsIP),
		})
	}

	// 2. VPN IP (Wire/custom mesh)
	if peer.VpnIP != "" {
		eps = append(eps, endpoint{
			host: peer.VpnIP,
			user: user,
			desc: fmt.Sprintf("VPN (%s)", peer.VpnIP),
		})
	}

	// 3. LAN IP (same network)
	if peer.LanIP != "" && !strings.HasPrefix(peer.LanIP, "127.") {
		eps = append(eps, endpoint{
			host: peer.LanIP,
			user: user,
			desc: fmt.Sprintf("LAN (%s)", peer.LanIP),
		})
	}

	// 4. Public IP
	if peer.PublicIP != "" {
		eps = append(eps, endpoint{
			host: peer.PublicIP,
			user: user,
			desc: fmt.Sprintf("Public (%s)", peer.PublicIP),
		})
	}

	return eps
}

// ResolveHost maps a configured/discovered node name to the best private
// address for the native vssh daemon. It intentionally returns only the host;
// native daemon port selection stays with the vssh client.
func (c *Connector) ResolveHost(target string) (string, error) {
	hosts, err := c.CandidateHosts(target)
	if err != nil {
		return "", err
	}
	return hosts[0], nil
}

// localIPs returns this machine's own non-loopback interface IPs so the resolver
// never dials our OWN address for a REMOTE peer (which silently reaches the local
// daemon — the misroute-to-controller class). Self-targeting still works via the
// loopback fallback in CandidateHosts.
func localIPs() map[string]bool {
	out := map[string]bool{}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			out[ipnet.IP.String()] = true
		}
	}
	return out
}

var (
	selfTSOnce sync.Once
	selfTSHost string
	selfTSDNS  string
	selfTSIPs  []string
)

var (
	tsStatusMu       sync.Mutex
	tsStatusCache    []byte
	tsStatusCachedAt time.Time
)

const tsStatusTTL = 5 * time.Second

// tailscaleStatusJSON returns `tailscale status --json` output cached for a
// short TTL. A fleet-wide resolution loop touches Tailscale once per node;
// without the cache each touch shelled out, turning an N-node fan-out into N
// subprocess spawns. A TTL (rather than sync.Once) keeps long-lived daemons
// fresh when a peer's Tailscale IP changes. Safe for the concurrent fan-out.
func tailscaleStatusJSON() []byte {
	tsStatusMu.Lock()
	defer tsStatusMu.Unlock()
	if tsStatusCache != nil && time.Since(tsStatusCachedAt) < tsStatusTTL {
		return tsStatusCache
	}
	out, err := exec.Command("tailscale", "status", "--json").Output()
	if err != nil {
		return nil
	}
	tsStatusCache = out
	tsStatusCachedAt = time.Now()
	return out
}

// tailscaleSelfInfo returns this machine's own Tailscale identity (the "Self"
// node). Cached for the process lifetime because it never changes mid-run and
// shelling out per resolved host would be wasteful. On macOS network-extension
// Tailscale the Self IP often does NOT appear in net.InterfaceAddrs, so this is
// the only reliable way to recognize our own Tailscale address.
func tailscaleSelfInfo() (hostname, dnsname string, ips []string) {
	selfTSOnce.Do(func() {
		out := tailscaleStatusJSON()
		if out == nil {
			return
		}
		var data struct {
			Self struct {
				HostName     string   `json:"HostName"`
				DNSName      string   `json:"DNSName"`
				TailscaleIPs []string `json:"TailscaleIPs"`
			} `json:"Self"`
		}
		if json.Unmarshal(out, &data) != nil {
			return
		}
		selfTSHost = data.Self.HostName
		selfTSDNS = data.Self.DNSName
		selfTSIPs = data.Self.TailscaleIPs
	})
	return selfTSHost, selfTSDNS, selfTSIPs
}

// localSelfAddrs is localIPs plus our own Tailscale Self IPs — the full set of
// addresses that actually point back at this machine.
func localSelfAddrs() map[string]bool {
	out := localIPs()
	_, _, ips := tailscaleSelfInfo()
	for _, ip := range ips {
		out[ip] = true
	}
	return out
}

// selfNames returns this machine's own name forms (OS hostname + Tailscale Self
// host/DNS name) for matching a target against ourselves.
func selfNames() []string {
	var out []string
	if h, err := os.Hostname(); err == nil && h != "" {
		out = append(out, h)
	}
	host, dns, _ := tailscaleSelfInfo()
	if host != "" {
		out = append(out, host)
	}
	if dns != "" {
		out = append(out, dns)
	}
	return out
}

// isSelfTarget reports whether target names THIS machine, by IP (loopback or any
// of our own/Tailscale addresses) or by name (OS or Tailscale hostname, full or
// short). When true the daemon is reachable on loopback, which sidesteps the
// stale/un-dialable self-IP stall that breaks `vssh exec m1` on the controller.
func isSelfTarget(target string) bool {
	target = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(target)), ".")
	if target == "" {
		return false
	}
	if ip := net.ParseIP(target); ip != nil {
		return ip.IsLoopback() || localSelfAddrs()[target]
	}
	short := strings.Split(target, ".")[0]
	for _, n := range selfNames() {
		n = strings.TrimSuffix(strings.ToLower(n), ".")
		if n == target || strings.Split(n, ".")[0] == short {
			return true
		}
	}
	return false
}

// CandidateHosts returns every candidate address for a node in preference order
// (Tailscale, VPN, LAN, Public), de-duplicated. The caller can probe these to
// pick a live endpoint instead of blindly dialing the first — which hangs for the
// full timeout when a node's preferred IP has moved (the classic stale-IP stall).
func (c *Connector) CandidateHosts(target string) ([]string, error) {
	// Self-target: the daemon listens on loopback, and our own Tailscale/VPN IP
	// may be un-dialable from the host itself (or not even enumerable on macOS),
	// so dial loopback directly instead of stalling on a self-IP. Handled before
	// peer lookup so it works even when the controller isn't in its own peer list.
	if isSelfTarget(target) {
		return []string{"127.0.0.1"}, nil
	}
	peer := c.findPeer(target)
	if peer == nil {
		return nil, fmt.Errorf("peer not found: %s", target)
	}
	endpoints := c.getEndpoints(peer)
	local := localSelfAddrs()
	seen := map[string]bool{}
	hosts := make([]string, 0, len(endpoints))
	filteredLocal := false
	for _, e := range endpoints {
		if e.host == "" || seen[e.host] {
			continue
		}
		if local[e.host] {
			filteredLocal = true
			continue
		}
		seen[e.host] = true
		hosts = append(hosts, e.host)
	}
	if len(hosts) == 0 {
		if filteredLocal {
			return []string{"127.0.0.1"}, nil
		}
		return nil, fmt.Errorf("no candidate address for peer: %s", target)
	}
	return hosts, nil
}

func lookupNodeUser(users map[string]string, nodeName string) string {
	candidates := []string{
		nodeName,
		strings.ToLower(nodeName),
		strings.Split(strings.ToLower(nodeName), ".")[0],
	}
	for _, candidate := range candidates {
		if user := users[candidate]; user != "" {
			return user
		}
	}
	for key, user := range users {
		if strings.EqualFold(key, nodeName) || strings.EqualFold(key, strings.Split(nodeName, ".")[0]) {
			return user
		}
	}
	return ""
}

func tailscaleIPForHost(hostname string) string {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" {
		return ""
	}

	output := tailscaleStatusJSON()
	if output == nil {
		return ""
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
		} `json:"Peer"`
	}
	if err := json.Unmarshal(output, &data); err != nil {
		return ""
	}

	matches := func(candidate string) bool {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		candidate = strings.TrimSuffix(candidate, ".")
		return candidate == hostname || strings.Split(candidate, ".")[0] == hostname
	}
	if (matches(data.Self.HostName) || matches(data.Self.DNSName)) && len(data.Self.TailscaleIPs) > 0 {
		return data.Self.TailscaleIPs[0]
	}
	for _, peer := range data.Peer {
		if (matches(peer.HostName) || matches(peer.DNSName)) && len(peer.TailscaleIPs) > 0 {
			return peer.TailscaleIPs[0]
		}
	}
	return ""
}

func (c *Connector) trySSH(host, user string, extraArgs []string) bool {
	args := []string{
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
	}

	target := host
	if user != "" {
		target = user + "@" + host
	}
	args = append(args, target)
	args = append(args, extraArgs...)

	cmd := exec.Command("ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	return err == nil
}

func (c *Connector) findPeer(target string) *config.Peer {
	target = strings.ToLower(target)

	for i := range c.peers {
		p := &c.peers[i]
		// Match by name
		if strings.ToLower(p.NodeName) == target {
			return p
		}
		// Match by partial name
		if strings.Contains(strings.ToLower(p.NodeName), target) {
			return p
		}
		// Match by VPN IP
		if p.VpnIP == target {
			return p
		}
		// Match by node ID
		if strings.HasPrefix(strings.ToLower(p.NodeID), target) {
			return p
		}
	}

	return nil
}
