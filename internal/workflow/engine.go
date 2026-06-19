// Package workflow runs predefined multi-step plans with conditional branching
// and failure handling — no LLM, no external orchestrator. A workflow is a list
// of steps; each step runs a command (with {{param}} templating) and an on_fail
// policy decides what happens when it fails: abort, continue, or jump to another
// step (e.g. a summary). Built-ins ship in builtin.go; users add JSON files
// under ~/.vssh/workflows/. The engine takes an injected command runner so it is
// fully testable without a network.
package workflow

import (
	"fmt"
	"strings"
	"time"
)

// Step is one node in a workflow.
//
// Type "exec" (default) runs Cmd; type "summary" emits a roll-up of prior steps.
// OnFail (only meaningful for exec) is one of:
//
//	""/"continue" — tolerate the failure and proceed to the next step
//	"abort"       — stop the run and mark it failed
//	"<step-id>"   — jump to that step (the run is marked failed)
type Step struct {
	ID     string `json:"id"`
	Cmd    string `json:"cmd,omitempty"`
	Type   string `json:"type,omitempty"`
	OnFail string `json:"on_fail,omitempty"`
}

// Workflow is a named, parameterized plan.
type Workflow struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Params      []string `json:"params,omitempty"`
	Steps       []Step   `json:"steps"`
}

// StepResult records one step's execution.
type StepResult struct {
	ID       string `json:"id"`
	Cmd      string `json:"cmd,omitempty"`
	Type     string `json:"type"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
	Action   string `json:"action,omitempty"`
	Skipped  bool   `json:"skipped,omitempty"`
}

// RunResult is the full record of one workflow execution (persisted for status).
type RunResult struct {
	RunID     string            `json:"run_id"`
	Workflow  string            `json:"workflow"`
	Target    string            `json:"target"`
	Params    map[string]string `json:"params,omitempty"`
	Status    string            `json:"status"` // success | failed | aborted
	Steps     []StepResult      `json:"steps"`
	Summary   string            `json:"summary"`
	StartedAt string            `json:"started_at"`
	EndedAt   string            `json:"ended_at"`
	DryRun    bool              `json:"dry_run,omitempty"`
}

// ExecFunc runs a command and reports its outcome. Injected so the engine has
// no transport dependency (the CLI/MCP wire in the native daemon exec).
type ExecFunc func(cmd string) (stdout, stderr string, exitCode int, success bool, err error)

// maxStepHops bounds total step executions so an on_fail jump cycle can't loop
// forever.
const maxStepHopsFactor = 4

// Validate checks that every declared param has a value.
func (w Workflow) Validate(params map[string]string) error {
	for _, p := range w.Params {
		if strings.TrimSpace(params[p]) == "" {
			return fmt.Errorf("missing required param %q", p)
		}
	}
	return nil
}

// Run executes the workflow against an injected ExecFunc. With dryRun=true no
// command runs; each exec step is recorded as skipped with its filled command.
func (w Workflow) Run(runID, target string, params map[string]string, dryRun bool, exec ExecFunc) RunResult {
	res := RunResult{
		RunID:     runID,
		Workflow:  w.Name,
		Target:    target,
		Params:    params,
		Status:    "success",
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		DryRun:    dryRun,
	}

	index := map[string]int{}
	for i, s := range w.Steps {
		index[s.ID] = i
	}

	i, hops, maxHops := 0, 0, len(w.Steps)*maxStepHopsFactor+8
	for i < len(w.Steps) && hops < maxHops {
		hops++
		step := w.Steps[i]

		if step.Type == "summary" {
			sr := StepResult{ID: step.ID, Type: "summary", Success: true}
			res.Steps = append(res.Steps, sr)
			i++
			continue
		}

		cmd := fillParams(step.Cmd, params)
		if dryRun {
			res.Steps = append(res.Steps, StepResult{ID: step.ID, Cmd: cmd, Type: "exec", Skipped: true, Action: "dry-run"})
			i++
			continue
		}

		stdout, stderr, exit, ok, err := exec(cmd)
		sr := StepResult{ID: step.ID, Cmd: cmd, Type: "exec", Stdout: stdout, Stderr: stderr, ExitCode: exit, Success: ok}
		if err != nil {
			sr.Error = err.Error()
		}
		if ok {
			res.Steps = append(res.Steps, sr)
			i++
			continue
		}

		// failure handling
		switch policy := strings.TrimSpace(step.OnFail); policy {
		case "", "continue":
			sr.Action = "continued"
			res.Steps = append(res.Steps, sr)
			i++
		case "abort":
			sr.Action = "aborted"
			res.Steps = append(res.Steps, sr)
			res.Status = "aborted"
			i = len(w.Steps) // stop
		default:
			if dst, found := index[policy]; found {
				sr.Action = "jumped:" + policy
				res.Steps = append(res.Steps, sr)
				if res.Status == "success" {
					res.Status = "failed"
				}
				i = dst
			} else {
				sr.Action = "aborted (unknown on_fail target)"
				res.Steps = append(res.Steps, sr)
				res.Status = "aborted"
				i = len(w.Steps)
			}
		}
	}

	res.EndedAt = time.Now().UTC().Format(time.RFC3339Nano)
	res.Summary = summarize(&res)
	return res
}

func fillParams(cmd string, params map[string]string) string {
	for k, v := range params {
		cmd = strings.ReplaceAll(cmd, "{{"+k+"}}", v)
	}
	return strings.TrimSpace(cmd)
}

func summarize(r *RunResult) string {
	total, ok, failed := 0, 0, 0
	var failedIDs []string
	for _, s := range r.Steps {
		if s.Type == "summary" || s.Skipped {
			continue
		}
		total++
		if s.Success {
			ok++
		} else {
			failed++
			failedIDs = append(failedIDs, s.ID)
		}
	}
	base := fmt.Sprintf("workflow %s on %s: %d/%d steps ok, status=%s", r.Workflow, r.Target, ok, total, r.Status)
	if r.DryRun {
		return fmt.Sprintf("workflow %s on %s: dry-run, %d steps planned", r.Workflow, r.Target, len(r.Steps))
	}
	if len(failedIDs) > 0 {
		base += " (failed: " + strings.Join(failedIDs, ", ") + ")"
	}
	return base
}
