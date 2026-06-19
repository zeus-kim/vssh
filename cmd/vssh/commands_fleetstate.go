package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeus-kim/vssh/internal/fleet"
	"github.com/zeus-kim/vssh/internal/server"
	"github.com/zeus-kim/vssh/internal/ssh"
)

func toInt64(v interface{}) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	}
	return 0
}

// dialAlive reports whether a TCP connection to addr succeeds within timeout.
func dialAlive(addr string, timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// probeReachable reports whether the node's vssh daemon is reachable on ANY of
// its candidate endpoints (relay/alternates included) — matching how exec routes,
// so liveness aligns with what `vssh run` would actually reach.
func probeReachable(conn *ssh.Connector, name, fallbackIP, port string) bool {
	cands, _ := conn.CandidateHosts(name)
	if len(cands) == 0 && fallbackIP != "" {
		cands = []string{fallbackIP}
	}
	for _, h := range cands {
		if dialAlive(net.JoinHostPort(h, port), 1500*time.Millisecond) {
			return true
		}
	}
	return false
}

// buildFleetNodes assembles the inventory from the local peer list + pinned node
// keys. When live is true, each node's Online is set by a parallel TCP probe of
// its vssh daemon port instead of the (possibly stale) cached liveness.
func buildFleetNodes(live bool) []server.FleetNode {
	conn, err := ssh.NewConnector("default")
	if err != nil {
		return nil
	}
	var nodes []server.FleetNode
	for _, p := range conn.ListPeers() {
		if strings.TrimSpace(p.NodeName) == "" {
			continue
		}
		online := false
		if p.Online != nil {
			online = *p.Online
		}
		nodes = append(nodes, server.FleetNode{
			Name:     p.NodeName,
			IP:       p.VpnIP,
			OS:       p.OS,
			Arch:     p.Arch,
			Caps:     p.Capabilities,
			Tags:     p.Tags,
			Pubkey:   server.NodeKey(p.NodeName),
			Online:   online,
			LastSeen: toInt64(p.LastSeen),
		})
	}
	if live {
		port := strconv.Itoa(getPort())
		var wg sync.WaitGroup
		sem := make(chan struct{}, 16)
		for i := range nodes {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int) {
				defer wg.Done()
				defer func() { <-sem }()
				nodes[i].Online = probeReachable(conn, nodes[i].Name, nodes[i].IP, port)
			}(i)
		}
		wg.Wait()
	}
	return nodes
}

// refreshFleetState rebuilds and persists the signed fleet state on this controller.
func refreshFleetState(live bool) (server.FleetState, error) {
	fs := server.BuildAndSignFleetState(buildFleetNodes(live))
	return fs, server.WriteFleetState(fs)
}

func fleetStateResult(fs server.FleetState, rebuilt bool) map[string]interface{} {
	res := map[string]interface{}{
		"success":      true,
		"tool":         "vssh_fleet_state",
		"verified":     server.VerifyFleetState(fs),
		"age_seconds":  server.FleetStateAgeSeconds(fs),
		"generated_at": fs.GeneratedAt,
		"generated_by": fs.GeneratedBy,
		"node_count":   len(fs.Nodes),
		"nodes":        fs.Nodes,
		"rebuilt":      rebuilt,
	}
	// Merge in controller-local fleet memory (role/services/tags/notes) so an AI
	// reading the fleet state gets historical context alongside live inventory.
	if fm, err := fleet.Load(); err == nil && len(fm.Nodes) > 0 {
		res["memory"] = fm.Nodes
	}
	return res
}

func cmdFleetState(args []string) {
	action := "show"
	if len(args) > 0 {
		action = args[0]
	}
	live := false
	for _, a := range args {
		if a == "--live" || a == "-live" {
			live = true
		}
	}
	switch action {
	case "build", "refresh":
		fs, err := refreshFleetState(live)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vssh: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("fleet state written: %s (%d nodes, live=%v)\n", server.FleetStatePath(), len(fs.Nodes), live)
	case "show":
		fs, err := server.ReadFleetState()
		if err != nil {
			fmt.Fprintf(os.Stderr, "vssh: no fleet state (%v); run 'vssh fleet-state build'\n", err)
			os.Exit(1)
		}
		b, _ := json.MarshalIndent(fs, "", "  ")
		fmt.Println(string(b))
		fmt.Fprintf(os.Stderr, "# verified=%v age=%ds\n", server.VerifyFleetState(fs), server.FleetStateAgeSeconds(fs))
	case "verify":
		fs, err := server.ReadFleetState()
		if err != nil {
			fmt.Fprintf(os.Stderr, "vssh: %v\n", err)
			os.Exit(1)
		}
		if !server.VerifyFleetState(fs) {
			fmt.Println("FAIL: signature invalid")
			os.Exit(1)
		}
		fmt.Printf("OK: signature valid, %d nodes, age %ds\n", len(fs.Nodes), server.FleetStateAgeSeconds(fs))
	default:
		fmt.Fprintln(os.Stderr, "usage: vssh fleet-state [build [--live]|show|verify]")
		os.Exit(1)
	}
}

// toolFleetState (MCP) reads (or with action=build, refreshes) the signed fleet
// state; action=build with live=true probes node reachability in real time.
func toolFleetState(args map[string]interface{}) map[string]interface{} {
	if a := getString(args, "action"); a == "build" || a == "refresh" {
		fs, err := refreshFleetState(getBool(args, "live", false))
		if err != nil {
			return map[string]interface{}{"success": false, "tool": "vssh_fleet_state",
				"error": map[string]interface{}{"code": "build_failed", "message": err.Error()}}
		}
		return fleetStateResult(fs, true)
	}
	fs, err := server.ReadFleetState()
	if err != nil {
		return map[string]interface{}{"success": false, "tool": "vssh_fleet_state",
			"error": map[string]interface{}{"code": "not_found", "message": "no fleet state yet; call with action=build"}}
	}
	return fleetStateResult(fs, false)
}
