package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zeus-kim/vssh/internal/fleet"
	"github.com/zeus-kim/vssh/internal/server"
)

// nodeHealth is one node's assessed health (or why it couldn't be reached).
type nodeHealth struct {
	Node     string   `json:"node"`
	Severity string   `json:"severity"` // ok | warn | critical | unreachable
	Load     float64  `json:"load,omitempty"`
	Cores    int      `json:"cores,omitempty"`
	DiskPct  int      `json:"disk_pct,omitempty"`
	MemPct   int      `json:"mem_pct,omitempty"`
	Failed   int      `json:"failed_units,omitempty"`
	Issues   []string `json:"issues,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// probeFleetHealth probes every target in parallel and assesses each. An
// unreachable node is a first-class critical result, not a dropped row — a down
// node is exactly what a health view must surface.
func probeFleetHealth(targets []string) []nodeHealth {
	out := make([]nodeHealth, len(targets))
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
			res, err := server.ExecCommandStructuredTimeout(host, getPort(), getSecret(), fleet.HealthProbeCommand, 15*time.Second)
			if err != nil || !res.Success {
				msg := "unreachable"
				if err != nil {
					msg = err.Error()
				}
				out[i] = nodeHealth{Node: t, Severity: "unreachable", Error: msg}
				return
			}
			sig := fleet.ParseHealth(res.Stdout)
			sev, issues := fleet.Assess(sig)
			out[i] = nodeHealth{
				Node: t, Severity: sev, Load: sig.Load, Cores: sig.Cores,
				DiskPct: sig.DiskPct, MemPct: sig.MemPct, Failed: sig.Failed, Issues: issues,
			}
		}()
	}
	wg.Wait()
	return out
}

// severityRank orders unreachable/critical first for the report.
func healthRank(sev string) int {
	if sev == "unreachable" {
		return -1
	}
	return fleet.SeverityRank(sev)
}

// cmdFleetHealth probes the fleet and prints a worst-first health summary.
func cmdFleetHealth(args []string) {
	jsonOut := false
	var targets []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
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
		fmt.Fprintln(os.Stderr, "vssh: no nodes to check (no peers configured)")
		os.Exit(1)
	}

	reports := probeFleetHealth(targets)
	// worst-first, then by name
	for i := 0; i < len(reports); i++ {
		for j := i + 1; j < len(reports); j++ {
			ri, rj := healthRank(reports[i].Severity), healthRank(reports[j].Severity)
			if rj < ri || (rj == ri && reports[j].Node < reports[i].Node) {
				reports[i], reports[j] = reports[j], reports[i]
			}
		}
	}

	if jsonOut {
		printJSON(map[string]interface{}{"nodes": reports, "summary": healthCounts(reports)})
		return
	}

	mark := map[string]string{"ok": "  ok  ", "warn": " WARN ", "critical": " CRIT ", "unreachable": " DOWN "}
	for _, r := range reports {
		m := mark[r.Severity]
		if m == "" {
			m = " ???? "
		}
		detail := strings.Join(r.Issues, ", ")
		if r.Severity == "unreachable" {
			detail = r.Error
		} else if detail == "" {
			detail = fmt.Sprintf("disk %d%%, load %s/%dc, mem %d%%", r.DiskPct, fleetTrim(r.Load), r.Cores, r.MemPct)
		}
		fmt.Printf("[%s] %-8s %s\n", m, r.Node, detail)
	}
	c := healthCounts(reports)
	fmt.Printf("\n%d nodes: %d ok, %d warn, %d critical, %d down\n",
		len(reports), c["ok"], c["warn"], c["critical"], c["unreachable"])
}

func healthCounts(reports []nodeHealth) map[string]int {
	c := map[string]int{"ok": 0, "warn": 0, "critical": 0, "unreachable": 0}
	for _, r := range reports {
		c[r.Severity]++
	}
	return c
}

func fleetTrim(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" {
		s = "0"
	}
	return s
}

// toolFleetHealth (MCP) exposes the same worst-first fleet health to an agent.
func toolFleetHealth(args map[string]interface{}) map[string]interface{} {
	var targets []string
	if t := getString(args, "target"); strings.TrimSpace(t) != "" {
		targets = strings.Split(t, ",")
		for i := range targets {
			targets[i] = strings.TrimSpace(targets[i])
		}
	} else {
		targets = peerNames()
	}
	if len(targets) == 0 {
		return map[string]interface{}{"success": false, "tool": "vssh_fleet_health",
			"error": map[string]interface{}{"code": "no_nodes", "message": "no peers to check"}}
	}
	reports := probeFleetHealth(targets)
	return map[string]interface{}{
		"success": true, "tool": "vssh_fleet_health",
		"summary": healthCounts(reports), "nodes": reports,
	}
}
