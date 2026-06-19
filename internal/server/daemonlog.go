package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Daemon operational log (distinct from the hash-chained audit trail). Records
// accept/handshake failures, slow handshakes, plaintext refusals, and periodic
// health so a stall or wedge is diagnosable after the fact. Best-effort: a log
// write never affects serving.

var (
	daemonLogOnce sync.Once
	daemonLogPath string
	daemonLogMu   sync.Mutex
)

func resolveDaemonLogPath() string {
	daemonLogOnce.Do(func() {
		daemonLogPath = filepath.Join(daemonLogDir(), "daemon.log")
	})
	return daemonLogPath
}

// daemonLogDir mirrors the audit-log directory selection WITHOUT triggering the
// audit path's sync.Once (keeping the two logs independent): a system path as
// root, else the user's ~/.vssh, else /tmp/vssh.
func daemonLogDir() string {
	if os.Geteuid() == 0 {
		if os.MkdirAll("/var/log/vssh", 0700) == nil {
			return "/var/log/vssh"
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		d := filepath.Join(home, ".vssh")
		if os.MkdirAll(d, 0700) == nil {
			return d
		}
	}
	_ = os.MkdirAll("/tmp/vssh", 0700)
	return "/tmp/vssh"
}

// daemonLog appends one best-effort structured JSONL event.
func daemonLog(event string, fields map[string]interface{}) {
	path := resolveDaemonLogPath()
	if path == "" {
		return
	}
	rec := map[string]interface{}{
		"ts":    time.Now().UTC().Format(time.RFC3339Nano),
		"event": event,
	}
	for k, v := range fields {
		rec[k] = v
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	daemonLogMu.Lock()
	defer daemonLogMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}
