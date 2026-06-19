package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zeus-kim/vssh/internal/server"
	"github.com/zeus-kim/vssh/internal/workflow"
)

// nativeExecFunc returns an ExecFunc bound to a resolved target host, so the
// workflow engine stays transport-agnostic.
func nativeExecFunc(target string) workflow.ExecFunc {
	host := resolveReachableHost(target, getPort())
	return func(cmd string) (string, string, int, bool, error) {
		res, err := server.ExecCommandStructuredTimeout(host, getPort(), getSecret(), cmd, 120*time.Second)
		return res.Stdout, res.Stderr, res.ExitCode, err == nil && res.Success, err
	}
}

func newRunID(name string) string {
	return fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
}

func cmdWorkflow(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: vssh workflow {list | run <name> --target X [--param k=v] [--dry-run] | status <run_id>}")
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		cmdWorkflowList(args[1:])
	case "run":
		cmdWorkflowRun(args[1:])
	case "status":
		cmdWorkflowStatus(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "usage: vssh workflow {list | run | status}")
		os.Exit(1)
	}
}

func cmdWorkflowList(_ []string) {
	for _, w := range workflow.List() {
		params := ""
		if len(w.Params) > 0 {
			params = " (params: " + strings.Join(w.Params, ", ") + ")"
		}
		fmt.Printf("%-18s %s%s\n", w.Name, w.Description, params)
	}
}

func cmdWorkflowRun(args []string) {
	target := ""
	dryRun := false
	jsonOut := false
	params := map[string]string{}
	var name string
	addParam := func(kv string) bool {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return false
		}
		params[parts[0]] = parts[1]
		return true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--target="):
			target = strings.TrimPrefix(a, "--target=")
		case a == "--target" && i+1 < len(args):
			i++
			target = args[i]
		case strings.HasPrefix(a, "--param="):
			if !addParam(strings.TrimPrefix(a, "--param=")) {
				fmt.Fprintf(os.Stderr, "vssh: --param expects key=value\n")
				os.Exit(1)
			}
		case a == "--param" && i+1 < len(args):
			i++
			if !addParam(args[i]) {
				fmt.Fprintf(os.Stderr, "vssh: --param expects key=value\n")
				os.Exit(1)
			}
		case a == "--dry-run":
			dryRun = true
		case a == "--json":
			jsonOut = true
		case strings.HasPrefix(a, "--"):
			fmt.Fprintf(os.Stderr, "vssh: unknown flag %q\n", a)
			os.Exit(1)
		default:
			if name == "" {
				name = a
			}
		}
	}

	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: vssh workflow run <name> --target X [--param k=v] [--dry-run]")
		os.Exit(1)
	}
	w, ok := workflow.Get(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "vssh: no workflow %q (try 'vssh workflow list')\n", name)
		os.Exit(1)
	}
	if err := w.Validate(params); err != nil {
		fmt.Fprintf(os.Stderr, "vssh: %v\n", err)
		os.Exit(1)
	}
	if target == "" {
		fmt.Fprintln(os.Stderr, "vssh: --target is required")
		os.Exit(1)
	}

	runID := newRunID(name)
	var execFn workflow.ExecFunc
	if !dryRun {
		execFn = nativeExecFunc(target)
	}
	res := w.Run(runID, target, params, dryRun, execFn)
	if err := workflow.SaveRun(res); err != nil {
		fmt.Fprintf(os.Stderr, "vssh: warning: could not persist run: %v\n", err)
	}

	if jsonOut {
		printJSON(res)
		return
	}
	printWorkflowRun(res)
}

func printWorkflowRun(res workflow.RunResult) {
	fmt.Printf("run %s  (%s)\n", res.RunID, res.Status)
	for _, s := range res.Steps {
		switch {
		case s.Type == "summary":
			continue
		case s.Skipped:
			fmt.Printf("  [plan] %s: %s\n", s.ID, s.Cmd)
		default:
			status := "ok"
			if !s.Success {
				status = fmt.Sprintf("FAIL exit=%d", s.ExitCode)
			}
			line := fmt.Sprintf("  [%s] %s", status, s.ID)
			if s.Action != "" {
				line += " → " + s.Action
			}
			fmt.Println(line)
			if out := strings.TrimRight(s.Stdout, "\n"); out != "" {
				for _, l := range strings.Split(out, "\n") {
					fmt.Printf("      %s\n", l)
				}
			}
		}
	}
	fmt.Printf("summary: %s\n", res.Summary)
}

func cmdWorkflowStatus(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: vssh workflow status <run_id>")
		os.Exit(1)
	}
	res, err := workflow.LoadRun(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "vssh: no such run %q (%v)\n", args[0], err)
		os.Exit(1)
	}
	printWorkflowRun(res)
}
