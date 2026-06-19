package ssh

import (
	"os"
	"strings"
	"testing"

	"github.com/zeus-kim/vssh/internal/config"
)

func TestIsSelfTargetLoopback(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "127.0.0.53"} {
		if !isSelfTarget(ip) {
			t.Errorf("isSelfTarget(%q) = false, want true", ip)
		}
	}
}

func TestIsSelfTargetHostname(t *testing.T) {
	host, err := os.Hostname()
	if err != nil || host == "" {
		t.Skip("no hostname")
	}
	if !isSelfTarget(host) {
		t.Errorf("isSelfTarget(%q) = false, want true (full hostname)", host)
	}
	short := strings.Split(host, ".")[0]
	if !isSelfTarget(short) {
		t.Errorf("isSelfTarget(%q) = false, want true (short hostname)", short)
	}
	if !isSelfTarget(strings.ToUpper(short)) {
		t.Errorf("isSelfTarget(%q) = false, want true (case-insensitive)", strings.ToUpper(short))
	}
}

func TestIsSelfTargetForeign(t *testing.T) {
	for _, v := range []string{"", "no-such-host-xyz-9991", "203.0.113.7", "10.255.255.254"} {
		if isSelfTarget(v) {
			t.Errorf("isSelfTarget(%q) = true, want false", v)
		}
	}
}

// A self-named peer whose only address is an un-dialable Tailscale-style IP must
// still resolve to loopback rather than that IP — the bug this fixes.
func TestCandidateHostsSelfPrefersLoopback(t *testing.T) {
	host, err := os.Hostname()
	if err != nil || host == "" {
		t.Skip("no hostname")
	}
	short := strings.Split(host, ".")[0]
	c := &Connector{peers: []config.Peer{
		{NodeName: short, VpnIP: "100.64.0.99"},
	}}
	hosts, err := c.CandidateHosts(short)
	if err != nil {
		t.Fatalf("CandidateHosts(%q): %v", short, err)
	}
	if len(hosts) != 1 || hosts[0] != "127.0.0.1" {
		t.Fatalf("CandidateHosts(%q) = %v, want [127.0.0.1]", short, hosts)
	}
}

// A genuinely remote peer must NOT be rewritten to loopback.
func TestCandidateHostsRemoteUnaffected(t *testing.T) {
	c := &Connector{peers: []config.Peer{
		{NodeName: "remote-xyz-node", VpnIP: "203.0.113.42"},
	}}
	hosts, err := c.CandidateHosts("remote-xyz-node")
	if err != nil {
		t.Fatalf("CandidateHosts remote: %v", err)
	}
	if len(hosts) != 1 || hosts[0] != "203.0.113.42" {
		t.Fatalf("remote hosts = %v, want [203.0.113.42]", hosts)
	}
}
