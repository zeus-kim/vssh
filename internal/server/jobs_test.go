package server

import (
	"testing"
	"time"
)

func TestRPCJobLifecycle(t *testing.T) {
	info, err := rpcJobStart("printf out; printf err >&2")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID == "" {
		t.Fatal("missing job id")
	}
	if info.Status != JobRunning {
		t.Fatalf("initial status=%s", info.Status)
	}

	waitForJob(t, info.ID)

	status, err := rpcJobStatus(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != JobSucceeded {
		t.Fatalf("status=%s", status.Status)
	}
	if status.ExitCode != 0 {
		t.Fatalf("exit=%d", status.ExitCode)
	}

	logs, err := rpcJobLogs(info.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if logs.Stdout != "out" {
		t.Fatalf("stdout=%q", logs.Stdout)
	}
	if logs.Stderr != "err" {
		t.Fatalf("stderr=%q", logs.Stderr)
	}
}

func TestRPCJobCancel(t *testing.T) {
	info, err := rpcJobStart("sleep 10")
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := rpcJobCancel(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.ID != info.ID {
		t.Fatalf("id=%s", canceled.ID)
	}

	waitForJob(t, info.ID)

	status, err := rpcJobStatus(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != JobCanceled {
		t.Fatalf("status=%s", status.Status)
	}
}

func TestTailString(t *testing.T) {
	if got := tailString("abcdef", 3); got != "def" {
		t.Fatalf("got=%q", got)
	}
	if got := tailString("abc", 0); got != "abc" {
		t.Fatalf("got=%q", got)
	}
}

func waitForJob(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, err := rpcJobStatus(id)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status != JobRunning {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for job")
}
