package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zeus-kim/vssh/internal/ssh"
)

// toolSetup is the MCP self-bootstrap: idempotent first-run configuration so any
// model on any client (Claude/Cursor/Codex/AI Studio) can get vssh ready by
// calling ONE tool. It auto-detects peers, (re)builds the trusted node-key
// registry (~/.vssh/node_keys) by a LOOPBACK handshake on each peer (its own
// daemon — unmisroutable), which is what host-identity verification checks, runs
// the doctor, and reports what remains a deliberate manual step. Safe to re-run.
func toolSetup() map[string]interface{} {
	connector, err := ssh.NewConnector("default")
	if err != nil {
		return map[string]interface{}{"success": false, "tool": "vssh_setup", "error": "no connector: " + err.Error()}
	}
	peers := connector.ListPeers()
	home, _ := os.UserHomeDir()
	regPath := filepath.Join(home, ".vssh", "node_keys")

	reg := map[string]string{}
	if data, e := os.ReadFile(regPath); e == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if f := strings.Fields(l); len(f) >= 2 {
				reg[f[0]] = f[1]
			}
		}
	}

	type res struct{ name, key string }
	ch := make(chan res, len(peers))
	var wg sync.WaitGroup
	for _, p := range peers {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			osOut, _ := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", name, "uname -s").Output()
			osn := strings.TrimSpace(string(osOut))
			if osn == "" {
				ch <- res{name, ""}
				return
			}
			vb, pfx := "/usr/local/bin/vssh", "sudo"
			if osn == "Darwin" {
				vb, pfx = "$HOME/.local/bin/vssh", ""
			}
			cmd := fmt.Sprintf("%s %s handshake-test --tls 127.0.0.1 2>/dev/null", pfx, vb)
			out, _ := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", name, cmd).Output()
			key := ""
			for _, l := range strings.Split(string(out), "\n") {
				if strings.Contains(l, "server_key") {
					if parts := strings.Split(l, "\""); len(parts) >= 4 {
						key = parts[3]
					}
				}
			}
			ch <- res{name, key}
		}(p.NodeName)
	}
	wg.Wait()
	close(ch)

	ok := 0
	var failed []string
	for r := range ch {
		if r.key != "" {
			reg[r.name] = r.key
			ok++
		} else {
			failed = append(failed, r.name)
		}
	}

	names := make([]string, 0, len(reg))
	for n := range reg {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + " " + reg[n] + "\n")
	}
	_ = os.MkdirAll(filepath.Dir(regPath), 0700)
	werr := os.WriteFile(regPath, []byte(b.String()), 0600)

	_, fleetErr := refreshFleetState(false)
	return map[string]interface{}{
		"fleet_state_written":        fleetErr == nil,
		"success":                    werr == nil,
		"tool":                       "vssh_setup",
		"registry_path":              regPath,
		"registry_entries":           len(reg),
		"refreshed":                  ok,
		"unreachable":                failed,
		"host_identity_verification": fmt.Sprintf("default-ON; now enforces %d known nodes (a misrouted/wrong host is refused)", len(reg)),
		"remaining_manual":           []string{"Flip VSSH_REQUIRE_TLS fleet-wide after the 7-day plaintext-free window: scripts/enable_require_tls.sh (gate-guarded)."},
		"next":                       "vssh is configured. Run vssh_doctor to confirm, then use vssh_exec normally.",
		"doctor":                     doctorJSON(runDoctor()),
	}
}

// autoSetupOnce runs vssh_setup at most once per controller — the first time an
// operational MCP tool (exec/facts/rpc/job/...) is used — so host-identity
// verification is provisioned with zero manual steps ("zero-touch onboarding").
// It is a no-op when already provisioned, when the marker exists, or when
// disabled via VSSH_NO_AUTOSETUP. Returns the setup result only if it ran.
func autoSetupOnce() map[string]interface{} {
	if v := strings.TrimSpace(os.Getenv("VSSH_NO_AUTOSETUP")); v != "" && v != "0" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	marker := filepath.Join(home, ".vssh", ".autosetup_done")
	if _, err := os.Stat(marker); err == nil {
		return nil // already attempted on this controller
	}
	// Already provisioned (registry populated)? Drop the marker and skip the cost.
	if nodeKeysCount(home) > 0 {
		writeAutoSetupMarker(marker)
		return nil
	}
	res := toolSetup()
	writeAutoSetupMarker(marker)
	if res != nil {
		res["auto_triggered"] = true
	}
	return res
}

// nodeKeysCount returns the number of pinned host keys in ~/.vssh/node_keys.
func nodeKeysCount(home string) int {
	data, err := os.ReadFile(filepath.Join(home, ".vssh", "node_keys"))
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(string(data), "\n") {
		if len(strings.Fields(l)) >= 2 {
			n++
		}
	}
	return n
}

func writeAutoSetupMarker(path string) {
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0600)
}
