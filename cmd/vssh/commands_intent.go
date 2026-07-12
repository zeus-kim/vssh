package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zeus-kim/vssh/internal/intent"
	"github.com/zeus-kim/vssh/internal/server"
)

func cmdIntent(args []string) {
	target := ""
	run := false
	jsonOut := false
	var queryParts []string
	// Index-based so --target accepts both "--target host" and "--target=host",
	// matching `vssh workflow` (and the documented usage).
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--target":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "vssh: --target needs a value")
				os.Exit(1)
			}
			i++
			target = args[i]
		case strings.HasPrefix(a, "--target="):
			target = strings.TrimPrefix(a, "--target=")
		case a == "--run":
			run = true
		case a == "--json":
			jsonOut = true
		case strings.HasPrefix(a, "--"):
			fmt.Fprintf(os.Stderr, "vssh: unknown flag %q\n", a)
			os.Exit(1)
		default:
			queryParts = append(queryParts, a)
		}
	}
	query := strings.Join(queryParts, " ")
	if strings.TrimSpace(query) == "" {
		fmt.Fprintln(os.Stderr, `usage: vssh intent "disk check" [--target <host>] [--run] [--json]`)
		os.Exit(1)
	}
	plan, ok := intent.Resolve(query)
	if !ok {
		fmt.Fprintf(os.Stderr, "vssh: no intent matched %q (try 'disk check', 'service check nginx', 'gpu status')\n", query)
		os.Exit(1)
	}
	if run && target == "" {
		fmt.Fprintln(os.Stderr, "vssh: --run requires --target")
		os.Exit(1)
	}

	if !run {
		if jsonOut {
			printJSON(plan)
			return
		}
		fmt.Printf("intent: %s\n", plan.Intent)
		fmt.Printf("why:    %s\n", plan.Rationale)
		fmt.Printf("matched: %s\n", strings.Join(plan.MatchedKeywords, ", "))
		fmt.Println("plan:")
		for _, c := range plan.Commands {
			fmt.Printf("  $ %s\n", c)
		}
		return
	}

	targets, err := resolveTargets(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vssh: %v\n", err)
		os.Exit(1)
	}
	nodes := runPlanMulti(targets, plan.Commands)
	if jsonOut {
		printJSON(map[string]interface{}{"plan": plan, "targets": targets, "nodes": nodes})
		return
	}
	fmt.Printf("intent: %s → %s\n", plan.Intent, strings.Join(targets, ", "))
	for _, nr := range nodes {
		if len(nodes) > 1 {
			fmt.Printf("\n=== %s ===\n", nr.Node)
		}
		for _, r := range nr.Results {
			fmt.Printf("$ %s\n", r.Command)
			if r.Stdout != "" {
				fmt.Print(r.Stdout)
				if !strings.HasSuffix(r.Stdout, "\n") {
					fmt.Println()
				}
			}
			if r.Stderr != "" {
				fmt.Fprint(os.Stderr, r.Stderr)
			}
			if r.Error != "" {
				fmt.Fprintf(os.Stderr, "[error] %s\n", r.Error)
			}
		}
	}
}

// nodePlanResult is one node's outcome when a plan runs across a target set.
type nodePlanResult struct {
	Node    string           `json:"node"`
	Results []planStepResult `json:"results"`
}

// runPlanMulti runs the same plan on every target in parallel, returning one
// result block per node (order preserved). This is the fleet-scale path: an
// operator or agent asks once ("gpu status" on @gpu) and gets structured
// per-node outcomes instead of looping ssh across hosts and scraping text.
func runPlanMulti(targets []string, commands []string) []nodePlanResult {
	out := make([]nodePlanResult, len(targets))
	sem := make(chan struct{}, normalizedMaxParallelism(defaultMaxParallelism(), len(targets)))
	var wg sync.WaitGroup
	for i, t := range targets {
		i, t := i, t
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = nodePlanResult{Node: t, Results: runPlan(t, commands)}
		}()
	}
	wg.Wait()
	return out
}

type planStepResult struct {
	Command  string `json:"command"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

func runPlan(target string, commands []string) []planStepResult {
	host := resolveReachableHost(target, getPort())
	out := make([]planStepResult, 0, len(commands))
	for _, c := range commands {
		res, err := server.ExecCommandStructuredTimeout(host, getPort(), getSecret(), c, 60*time.Second)
		step := planStepResult{
			Command:  c,
			Stdout:   res.Stdout,
			Stderr:   res.Stderr,
			ExitCode: res.ExitCode,
			Success:  err == nil && res.Success,
		}
		if err != nil {
			step.Error = err.Error()
		}
		out = append(out, step)
	}
	return out
}

// toolIntent (MCP) resolves a natural-language query into a command plan, and
// optionally executes it on a target.
func toolIntent(args map[string]interface{}) map[string]interface{} {
	query := getString(args, "query")
	if strings.TrimSpace(query) == "" {
		return map[string]interface{}{"success": false, "tool": "vssh_intent",
			"error": map[string]interface{}{"code": "missing_argument", "message": "query is required"}}
	}
	plan, ok := intent.Resolve(query)
	if !ok {
		return map[string]interface{}{"success": false, "tool": "vssh_intent", "query": query,
			"error": map[string]interface{}{"code": "no_match", "message": "no intent matched the query"}}
	}
	payload := map[string]interface{}{
		"success":          true,
		"tool":             "vssh_intent",
		"query":            query,
		"intent":           plan.Intent,
		"commands":         plan.Commands,
		"rationale":        plan.Rationale,
		"matched_keywords": plan.MatchedKeywords,
		"executed":         false,
	}
	target := getString(args, "target")
	if getBool(args, "execute", false) {
		if target == "" {
			payload["success"] = false
			payload["error"] = map[string]interface{}{"code": "missing_argument", "message": "execute=true requires target"}
			return payload
		}
		targets, err := resolveTargets(target)
		if err != nil {
			payload["success"] = false
			payload["error"] = map[string]interface{}{"code": "bad_target", "message": err.Error()}
			return payload
		}
		payload["executed"] = true
		payload["target"] = target
		payload["targets"] = targets
		payload["nodes"] = runPlanMulti(targets, plan.Commands)
	}
	return payload
}
