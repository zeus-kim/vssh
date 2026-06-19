package server

import "testing"

func TestExecLocalStructuredCapturesStdoutStderrAndExit(t *testing.T) {
	result := ExecLocalStructured("printf out; printf err >&2; exit 7")

	if result.Success {
		t.Fatal("Success = true, want false")
	}
	if result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", result.ExitCode)
	}
	if result.Stdout != "out" {
		t.Fatalf("Stdout = %q, want out", result.Stdout)
	}
	if result.Stderr != "err" {
		t.Fatalf("Stderr = %q, want err", result.Stderr)
	}
	if result.DurationMs < 0 {
		t.Fatalf("DurationMs = %d, want non-negative", result.DurationMs)
	}
}
