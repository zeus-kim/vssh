package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zeus-kim/vssh/internal/config"
	"github.com/zeus-kim/vssh/internal/ssh"
)

type DoctorReport struct {
	Kind        string        `json:"kind"`
	Status      string        `json:"status"`
	Checks      []DoctorCheck `json:"checks"`
	NextActions []string      `json:"next_actions"`
}

type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Next   string `json:"next,omitempty"`
}

func cmdDoctor(args []string) {
	jsonOut := false
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--json" {
			jsonOut = true
			continue
		}
		filtered = append(filtered, arg)
	}
	if len(filtered) != 0 {
		fmt.Fprintln(os.Stderr, "Usage: vssh doctor [--json]")
		os.Exit(1)
	}
	report := runDoctor()
	if jsonOut {
		writeJSON(report)
		return
	}
	fmt.Print(formatDoctor(report))
	if report.Status == "fail" {
		os.Exit(1)
	}
}

func runDoctor() DoctorReport {
	report := DoctorReport{Kind: "vssh_doctor", Status: "ok"}
	add := func(name, status, detail, next string) {
		report.Checks = append(report.Checks, DoctorCheck{Name: name, Status: status, Detail: detail, Next: next})
		if status == "fail" {
			report.Status = "fail"
		} else if status == "warn" && report.Status == "ok" {
			report.Status = "warn"
		}
	}

	if exe, err := os.Executable(); err == nil {
		add("vssh_binary", "ok", exe, "")
	} else {
		add("vssh_binary", "fail", err.Error(), "reinstall vssh or run the expected binary explicitly")
	}
	add("vssh_version", "ok", fmt.Sprintf("vssh %s", version), "")
	checkBinaryConflicts(add)
	checkAuthModel(add)
	checkWireConfig(add)
	checkPeers(add)
	// host-identity registry (node_keys) — verification is default-ON but only
	// enforces nodes present here; an empty registry means it is dormant.
	if home, err := os.UserHomeDir(); err == nil {
		n := 0
		if data, e := os.ReadFile(filepath.Join(home, ".vssh", "node_keys")); e == nil {
			for _, l := range strings.Split(string(data), "\n") {
				if len(strings.Fields(l)) >= 2 {
					n++
				}
			}
		}
		if n == 0 {
			add("host_identity_registry", "warn", "node_keys empty/missing — host-identity verification is dormant", "run the vssh_setup tool (or scripts/build_node_registry.sh) to populate it")
		} else {
			add("host_identity_registry", "ok", fmt.Sprintf("%d nodes pinned", n), "")
		}
	}

	switch report.Status {
	case "ok":
		report.NextActions = []string{
			"Run vssh status.",
			"Run vssh facts-many <hosts> before connecting AI operators.",
			"Connect MCP clients to vssh mcp only when direct transport tools are needed.",
		}
	case "warn":
		report.NextActions = []string{
			"Review warning checks before relying on native execution.",
			"Pin MCP clients to the intended vssh binary.",
		}
	default:
		report.NextActions = []string{
			"Fix failed checks before starting vsshd or exposing vssh MCP.",
		}
	}
	return report
}

func checkBinaryConflicts(add func(name, status, detail, next string)) {
	current, _ := os.Executable()
	candidates := vsshBinaryCandidates(current)
	versions := map[string]string{}
	for _, candidate := range candidates {
		versions[candidate] = vsshBinaryVersion(candidate)
	}
	if len(versions) == 0 {
		add("vssh_binary_conflict", "warn", "no executable vssh candidates found", "install vssh or add it to PATH")
		return
	}
	if hasVersionConflict(versions) {
		add("vssh_binary_conflict", "warn", formatVersions(versions), "remove stale vssh binaries or pin MCP clients to the intended binary")
		return
	}
	add("vssh_binary_conflict", "ok", formatVersions(versions), "")
}

func checkAuthModel(add func(name, status, detail, next string)) {
	// vssh authenticates with per-node Ed25519 keys (VAUTH1); there is no shared
	// secret. Authorization is governed by ~/.vssh/authorized_keys (or /etc/vssh).
	add("auth_model", "ok", "key-only (Ed25519 VAUTH1); no shared secret", "")
}

func checkWireConfig(add func(name, status, detail, next string)) {
	cfg, err := config.LoadWireConfig()
	if err != nil {
		add("wire_config", "warn", "wire config not found", "standalone vssh can still work; configure peers or use explicit host:port")
		return
	}
	detail := "node=" + cfg.NodeName
	if cfg.ServerURL != "" {
		detail += " server_url=set"
	}
	add("wire_config", "ok", detail, "")
}

func checkPeers(add func(name, status, detail, next string)) {
	connector, err := ssh.NewConnector("default")
	if err != nil {
		add("peers", "warn", err.Error(), "verify wire/tailscale peer configuration or use explicit host:port targets")
		return
	}
	peers := connector.ListPeers()
	online := 0
	now := time.Now().Unix()
	for _, peer := range peers {
		if peer.NodeName == "" {
			continue
		}
		if peerOnline(peer, now) {
			online++
		}
	}
	if len(peers) == 0 {
		add("peers", "warn", "peers=0", "configure peers before using routing, facts-many, or MCP host selection")
		return
	}
	add("peers", "ok", fmt.Sprintf("peers=%d online=%d", len(peers), online), "")
}

func peerOnline(peer config.Peer, now int64) bool {
	if peer.Online != nil {
		return *peer.Online
	}
	if peer.LastSeen == nil {
		return false
	}
	switch v := peer.LastSeen.(type) {
	case float64:
		return now-int64(v) < 60
	case int64:
		return now-v < 60
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return time.Since(t) < 60*time.Second
		}
	}
	return false
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func vsshBinaryCandidates(current string) []string {
	seen := map[string]bool{}
	add := func(path string) {
		if path == "" || seen[path] {
			return
		}
		if st, err := os.Stat(path); err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
			seen[path] = true
		}
	}
	add(current)
	if path, err := exec.LookPath("vssh"); err == nil {
		add(path)
	}
	add(filepath.Join(homeDir(), "bin", "vssh"))
	add("/usr/local/bin/vssh")
	add("/opt/homebrew/bin/vssh")
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		add(filepath.Join(dir, "vssh"))
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func vsshBinaryVersion(path string) string {
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func hasVersionConflict(versions map[string]string) bool {
	seen := map[string]bool{}
	for _, version := range versions {
		if version == "" || version == "unknown" {
			continue
		}
		seen[version] = true
	}
	return len(seen) > 1
}

func formatVersions(versions map[string]string) string {
	paths := make([]string, 0, len(versions))
	for path := range versions {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		parts = append(parts, path+"="+versions[path])
	}
	return strings.Join(parts, "; ")
}

func formatDoctor(report DoctorReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "vssh doctor status=%s\n", report.Status)
	for _, check := range report.Checks {
		fmt.Fprintf(&b, "- %s: %s", check.Name, check.Status)
		if check.Detail != "" {
			fmt.Fprintf(&b, " | %s", check.Detail)
		}
		if check.Next != "" {
			fmt.Fprintf(&b, " | next: %s", check.Next)
		}
		b.WriteByte('\n')
	}
	if len(report.NextActions) > 0 {
		b.WriteString("next_actions:\n")
		for _, action := range report.NextActions {
			fmt.Fprintf(&b, "- %s\n", action)
		}
	}
	return b.String()
}

func doctorJSON(report DoctorReport) map[string]interface{} {
	payload, _ := json.Marshal(report)
	out := map[string]interface{}{}
	json.Unmarshal(payload, &out)
	return out
}
