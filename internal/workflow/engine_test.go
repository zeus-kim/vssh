package workflow

import (
	"strings"
	"testing"
)

// fakeExec returns an ExecFunc that fails for any command containing a substring
// in failOn, succeeds otherwise, and records the commands it ran.
func fakeExec(ran *[]string, failOn ...string) ExecFunc {
	return func(cmd string) (string, string, int, bool, error) {
		*ran = append(*ran, cmd)
		for _, f := range failOn {
			if strings.Contains(cmd, f) {
				return "", "boom", 1, false, nil
			}
		}
		return "ok", "", 0, true, nil
	}
}

func TestRunParamTemplating(t *testing.T) {
	w, _ := Get("service-restart")
	var ran []string
	res := w.Run("r1", "v1", map[string]string{"service": "nginx"}, false, fakeExec(&ran))
	if res.Status != "success" {
		t.Fatalf("status = %s, want success; steps=%+v", res.Status, res.Steps)
	}
	if ran[0] != "systemctl status nginx --no-pager" {
		t.Fatalf("templating failed: %q", ran[0])
	}
	// verify is the last exec step; report is a summary
	last := res.Steps[len(res.Steps)-1]
	if last.Type != "summary" {
		t.Fatalf("last step should be summary, got %+v", last)
	}
}

func TestRunAbortOnPrecheck(t *testing.T) {
	w, _ := Get("service-restart")
	var ran []string
	// "status" check fails → abort before stop/start
	res := w.Run("r2", "v1", map[string]string{"service": "ghost"}, false, fakeExec(&ran, "status ghost"))
	if res.Status != "aborted" {
		t.Fatalf("status = %s, want aborted", res.Status)
	}
	if len(ran) != 1 {
		t.Fatalf("expected to stop after failed precheck, ran=%v", ran)
	}
}

func TestRunJumpToReportOnVerifyFail(t *testing.T) {
	w, _ := Get("service-restart")
	var ran []string
	// verify (is-active) fails → jump to report summary; run marked failed
	res := w.Run("r3", "v1", map[string]string{"service": "nginx"}, false, fakeExec(&ran, "is-active"))
	if res.Status != "failed" {
		t.Fatalf("status = %s, want failed", res.Status)
	}
	var verify *StepResult
	for i := range res.Steps {
		if res.Steps[i].ID == "verify" {
			verify = &res.Steps[i]
		}
	}
	if verify == nil || verify.Action != "jumped:report" {
		t.Fatalf("verify action = %+v, want jumped:report", verify)
	}
}

func TestRunTolerantContinue(t *testing.T) {
	w, _ := Get("health-check")
	var ran []string
	// a middle step fails but on_fail=continue → run still success, all steps run
	res := w.Run("r4", "d1", nil, false, fakeExec(&ran, "df -h"))
	if res.Status != "success" {
		t.Fatalf("status = %s, want success (tolerant)", res.Status)
	}
	if len(ran) != 4 { // uptime, disk, memory, top
		t.Fatalf("expected all 4 commands to run, got %v", ran)
	}
}

func TestRunDryRun(t *testing.T) {
	w, _ := Get("service-restart")
	var ran []string
	res := w.Run("r5", "v1", map[string]string{"service": "nginx"}, true, fakeExec(&ran))
	if len(ran) != 0 {
		t.Fatalf("dry-run executed commands: %v", ran)
	}
	if !res.DryRun || !strings.Contains(res.Summary, "dry-run") {
		t.Fatalf("dry-run summary = %q", res.Summary)
	}
	for _, s := range res.Steps {
		if s.Type == "exec" && !s.Skipped {
			t.Fatalf("dry-run step not skipped: %+v", s)
		}
	}
}

func TestValidateMissingParam(t *testing.T) {
	w, _ := Get("service-restart")
	if err := w.Validate(map[string]string{}); err == nil {
		t.Fatal("expected missing-param error")
	}
	if err := w.Validate(map[string]string{"service": "nginx"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListIncludesBuiltins(t *testing.T) {
	names := map[string]bool{}
	for _, w := range List() {
		names[w.Name] = true
	}
	for _, want := range []string{"service-restart", "health-check", "disk-cleanup", "log-collect"} {
		if !names[want] {
			t.Fatalf("built-in %q missing from List()", want)
		}
	}
}

func TestSaveAndLoadRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	w, _ := Get("health-check")
	var ran []string
	res := w.Run("save-1", "d1", nil, false, fakeExec(&ran))
	if err := SaveRun(res); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	got, err := LoadRun("save-1")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if got.RunID != "save-1" || got.Workflow != "health-check" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestRunCycleBounded(t *testing.T) {
	// a workflow that jumps to itself on failure must terminate, not hang
	w := Workflow{
		Name:  "loopy",
		Steps: []Step{{ID: "a", Cmd: "false", OnFail: "a"}},
	}
	var ran []string
	res := w.Run("r6", "x", nil, false, fakeExec(&ran, "false"))
	if len(ran) > 64 {
		t.Fatalf("cycle not bounded: ran %d times", len(ran))
	}
	if res.Status == "success" {
		t.Fatalf("looping failure should not be success")
	}
}
