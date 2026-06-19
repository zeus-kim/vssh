package server

import (
	"strings"
	"testing"
)

// TestDaemonLogBestEffort confirms the daemon log resolves a path and never
// panics (writes are best-effort and must never affect serving).
func TestDaemonLogBestEffort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if p := resolveDaemonLogPath(); !strings.HasSuffix(p, "daemon.log") {
		t.Errorf("daemon log path %q should end with daemon.log", p)
	}
	daemonLog("test_event", map[string]interface{}{"k": "v", "n": 1})
}
