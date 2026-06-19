package server

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Fleet state: a consolidated, controller-authoritative snapshot of the whole
// fleet (inventory + node keys + caps/tags + liveness). The controller signs it
// with its Ed25519 identity and timestamps it; read-only replicas can live on
// every node for durability, and any consumer can verify authorship + freshness.

type FleetNode struct {
	Name     string   `json:"name"`
	IP       string   `json:"ip,omitempty"`
	OS       string   `json:"os,omitempty"`
	Arch     string   `json:"arch,omitempty"`
	Caps     []string `json:"caps,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Pubkey   string   `json:"pubkey,omitempty"`
	Online   bool     `json:"online"`
	LastSeen int64    `json:"last_seen,omitempty"`
}

type FleetState struct {
	Version     int         `json:"version"`
	GeneratedAt string      `json:"generated_at"`
	GeneratedBy string      `json:"generated_by"` // controller public key (base64)
	Nodes       []FleetNode `json:"nodes"`
	Signature   string      `json:"signature,omitempty"`
}

// FleetStatePath is the canonical location of the persisted fleet state.
func FleetStatePath() string { return filepath.Join(vsshConfigDir(), "fleet_state.json") }

func sortedNodesCopy(in []FleetNode) []FleetNode {
	nodes := make([]FleetNode, len(in))
	copy(nodes, in)
	for i := range nodes {
		c := append([]string(nil), nodes[i].Caps...)
		sort.Strings(c)
		nodes[i].Caps = c
		t := append([]string(nil), nodes[i].Tags...)
		sort.Strings(t)
		nodes[i].Tags = t
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes
}

// canonicalFleetBytes produces a deterministic byte representation for signing
// and verification (signature cleared, nodes/caps/tags sorted, no caller mutation).
func canonicalFleetBytes(fs FleetState) []byte {
	cp := fs
	cp.Signature = ""
	cp.Nodes = sortedNodesCopy(fs.Nodes)
	b, _ := json.Marshal(cp)
	return b
}

// BuildAndSignFleetState assembles a fleet state signed by this host's identity.
func BuildAndSignFleetState(nodes []FleetNode) FleetState {
	priv, pub := LoadOrCreateIdentity()
	fs := FleetState{
		Version:     1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		GeneratedBy: pub,
		Nodes:       sortedNodesCopy(nodes),
	}
	sig := ed25519.Sign(priv, canonicalFleetBytes(fs))
	fs.Signature = base64.StdEncoding.EncodeToString(sig)
	return fs
}

// VerifyFleetState reports whether the signature matches the embedded generated_by key.
func VerifyFleetState(fs FleetState) bool {
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(fs.GeneratedBy))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(fs.Signature))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), canonicalFleetBytes(fs), sig)
}

// FleetStateAgeSeconds returns the age of the snapshot in seconds, or -1 if the
// timestamp cannot be parsed.
func FleetStateAgeSeconds(fs FleetState) int64 {
	t, err := time.Parse(time.RFC3339, fs.GeneratedAt)
	if err != nil {
		return -1
	}
	return int64(time.Since(t).Seconds())
}

func WriteFleetState(fs FleetState) error {
	b, err := json.MarshalIndent(fs, "", "  ")
	if err != nil {
		return err
	}
	_ = os.MkdirAll(vsshConfigDir(), 0700)
	return os.WriteFile(FleetStatePath(), b, 0644)
}

func ReadFleetState() (FleetState, error) {
	var fs FleetState
	data, err := os.ReadFile(FleetStatePath())
	if err != nil {
		return fs, err
	}
	err = json.Unmarshal(data, &fs)
	return fs, err
}
