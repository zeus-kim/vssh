package ssh

import (
	"testing"

	"github.com/zeus-kim/vssh/internal/config"
)

func TestApplyLocalServerConfigMergesRoutingMetadata(t *testing.T) {
	peer := config.Peer{
		NodeName:     "gpu1",
		VpnIP:        "192.0.2.20",
		Tags:         []string{"linux"},
		Capabilities: []string{"docker"},
		Metadata:     map[string]interface{}{"source": "discovery"},
	}
	applyLocalServerConfig(&peer, localServerConfig{
		User:         "ubuntu",
		Tags:         []string{"gpu", "linux"},
		Capabilities: []string{"cuda", "ollama"},
		OS:           "linux",
		Arch:         "amd64",
		Metadata:     map[string]interface{}{"owner": "runtime", "source": "local"},
	})

	if peer.User != "ubuntu" {
		t.Fatalf("user = %q, want ubuntu", peer.User)
	}
	if peer.OS != "linux" || peer.Arch != "amd64" {
		t.Fatalf("os/arch = %q/%q, want linux/amd64", peer.OS, peer.Arch)
	}
	for _, want := range []string{"linux", "gpu"} {
		if !contains(peer.Tags, want) {
			t.Fatalf("tags = %#v, missing %q", peer.Tags, want)
		}
	}
	for _, want := range []string{"docker", "cuda", "ollama"} {
		if !contains(peer.Capabilities, want) {
			t.Fatalf("capabilities = %#v, missing %q", peer.Capabilities, want)
		}
	}
	if peer.Metadata["source"] != "discovery" {
		t.Fatalf("metadata source overwritten: %#v", peer.Metadata)
	}
	if peer.Metadata["owner"] != "runtime" {
		t.Fatalf("metadata owner missing: %#v", peer.Metadata)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
