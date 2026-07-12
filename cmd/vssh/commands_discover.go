package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zeus-kim/vssh/internal/fleet"
	"github.com/zeus-kim/vssh/internal/server"
	"github.com/zeus-kim/vssh/internal/ssh"
)

// discoveryResult is one node's inferred memory (or why it couldn't be probed).
type discoveryResult struct {
	Node     string   `json:"node"`
	Role     string   `json:"role,omitempty"`
	Services []string `json:"services,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// discoverNodes probes every target in parallel and infers what each one IS
// from what it actually runs. Unreachable nodes are reported, not fatal — a
// partial fleet still yields a useful memory refresh.
func discoverNodes(targets []string) []discoveryResult {
	out := make([]discoveryResult, len(targets))
	sem := make(chan struct{}, normalizedMaxParallelism(defaultMaxParallelism(), len(targets)))
	var wg sync.WaitGroup
	for i, t := range targets {
		i, t := i, t
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			host := resolveReachableHost(t, getPort())
			res, err := server.ExecCommandStructuredTimeout(host, getPort(), getSecret(), fleet.ProbeCommand, 30*time.Second)
			if err != nil || !res.Success {
				msg := "unreachable"
				if err != nil {
					msg = err.Error()
				} else if strings.TrimSpace(res.Error) != "" {
					msg = res.Error
				}
				out[i] = discoveryResult{Node: t, Error: msg}
				return
			}
			m := fleet.Infer(fleet.ParseSignals(res.Stdout))
			out[i] = discoveryResult{Node: t, Role: m.Role, Services: m.Services, Tags: m.Tags}
		}()
	}
	wg.Wait()
	return out
}

// peerNames lists every node vssh knows about, so discovery covers the fleet
// without the operator naming each box.
func peerNames() []string {
	c, err := ssh.NewConnector("default")
	if err != nil {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	for _, p := range c.ListPeers() {
		if p.NodeName == "" || seen[p.NodeName] {
			continue
		}
		seen[p.NodeName] = true
		names = append(names, p.NodeName)
	}
	sort.Strings(names)
	return names
}

// cmdMemoryDiscover probes the fleet and refreshes fleet memory from observed
// usage patterns. It PLANS by default and only writes with --apply, so a bad
// probe can't silently rewrite the operator's memory. Notes are preserved
// (SetNode keeps them); only role/services/tags are derived.
func cmdMemoryDiscover(args []string) {
	apply, jsonOut := false, false
	var targets []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--apply":
			apply = true
		case a == "--json":
			jsonOut = true
		case a == "--target":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "vssh: --target needs a value")
				os.Exit(1)
			}
			i++
			targets = append(targets, strings.Split(args[i], ",")...)
		case strings.HasPrefix(a, "--target="):
			targets = append(targets, strings.Split(strings.TrimPrefix(a, "--target="), ",")...)
		case strings.HasPrefix(a, "--"):
			fmt.Fprintf(os.Stderr, "vssh: unknown flag %q\n", a)
			os.Exit(1)
		default:
			targets = append(targets, a)
		}
	}
	if len(targets) == 0 {
		targets = peerNames()
	}
	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "vssh: no nodes to discover (no peers configured)")
		os.Exit(1)
	}

	results := discoverNodes(targets)

	if apply {
		fm := loadMemoryOrExit()
		applied := 0
		for _, r := range results {
			if r.Error != "" {
				continue
			}
			fm.SetNode(r.Node, fleet.NodeMemory{Role: r.Role, Services: r.Services, Tags: r.Tags})
			applied++
		}
		if err := fm.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "vssh: save: %v\n", err)
			os.Exit(1)
		}
		if !jsonOut {
			fmt.Printf("applied to %d/%d nodes (notes preserved)\n\n", applied, len(results))
		}
	}

	if jsonOut {
		printJSON(map[string]interface{}{"applied": apply, "nodes": results})
		return
	}
	if !apply {
		fmt.Println("(plan only — re-run with --apply to write fleet memory)")
		fmt.Println()
	}
	for _, r := range results {
		if r.Error != "" {
			fmt.Printf("%-6s  !! %s\n", r.Node, r.Error)
			continue
		}
		fmt.Printf("%-6s  role=%-8s services=%-30s tags=%s\n",
			r.Node, r.Role, strings.Join(r.Services, ","), strings.Join(r.Tags, ","))
	}
}

// toolMemoryDiscover (MCP) exposes the same fleet-wide discovery to an agent.
func toolMemoryDiscover(args map[string]interface{}) map[string]interface{} {
	var targets []string
	if t := getString(args, "target"); strings.TrimSpace(t) != "" {
		targets = strings.Split(t, ",")
	} else {
		targets = peerNames()
	}
	for i := range targets {
		targets[i] = strings.TrimSpace(targets[i])
	}
	if len(targets) == 0 {
		return map[string]interface{}{"success": false, "tool": "vssh_memory",
			"error": map[string]interface{}{"code": "no_nodes", "message": "no peers configured to discover"}}
	}
	results := discoverNodes(targets)
	apply := getBool(args, "apply", false)
	applied := 0
	if apply {
		fm, err := fleet.Load()
		if err != nil {
			return map[string]interface{}{"success": false, "tool": "vssh_memory",
				"error": map[string]interface{}{"code": "load_failed", "message": err.Error()}}
		}
		for _, r := range results {
			if r.Error != "" {
				continue
			}
			fm.SetNode(r.Node, fleet.NodeMemory{Role: r.Role, Services: r.Services, Tags: r.Tags})
			applied++
		}
		if err := fm.Save(); err != nil {
			return map[string]interface{}{"success": false, "tool": "vssh_memory",
				"error": map[string]interface{}{"code": "save_failed", "message": err.Error()}}
		}
	}
	return map[string]interface{}{
		"success": true, "tool": "vssh_memory", "action": "discover",
		"applied": apply, "applied_count": applied, "nodes": results,
	}
}
