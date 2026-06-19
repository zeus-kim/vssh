package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// vssh_tunnel (P5): manage long-lived port forwards as DETACHED background
// processes. A forward (`vssh fwd`) blocks for its whole lifetime, which does not
// fit an MCP request/response call, so this tool spawns `vssh fwd` in its own
// session (Setsid) — surviving the MCP session — and tracks it in a small
// on-disk registry (~/.vssh/tunnels/<id>.json) so list/stop work across calls.

type tunnelRecord struct {
	ID        string   `json:"id"`
	PID       int      `json:"pid"`
	Type      string   `json:"type"` // local|reverse|socks
	Target    string   `json:"target"`
	Spec      string   `json:"spec"`
	Argv      []string `json:"argv"`
	Log       string   `json:"log"`
	StartedAt string   `json:"started_at"`
}

func tunnelDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vssh", "tunnels")
}

func tunnelErr(code, msg string) map[string]interface{} {
	return map[string]interface{}{
		"success": false, "tool": "vssh_tunnel",
		"error": map[string]interface{}{"code": code, "message": msg},
	}
}

func toolTunnel(args map[string]interface{}) map[string]interface{} {
	action := strings.ToLower(strings.TrimSpace(getString(args, "action")))
	if action == "" {
		action = "list"
	}
	switch action {
	case "start":
		return tunnelStart(args)
	case "list", "status":
		return tunnelList()
	case "stop":
		return tunnelStop(args)
	default:
		return tunnelErr("invalid_argument", "action must be start|list|stop")
	}
}

func tunnelStart(args map[string]interface{}) map[string]interface{} {
	target := strings.TrimSpace(getString(args, "target"))
	spec := strings.TrimSpace(getString(args, "spec"))
	ttype := strings.ToLower(strings.TrimSpace(getString(args, "type")))
	if target == "" || spec == "" {
		return tunnelErr("missing_argument", "target and spec are required for start")
	}
	var mode string
	switch ttype {
	case "local", "l", "-l":
		mode = "-L"
	case "reverse", "remote", "r", "-r":
		mode = "-R"
	case "socks", "dynamic", "d", "-d":
		mode = "-D"
	default:
		return tunnelErr("invalid_argument", "type must be local|reverse|socks")
	}
	self, err := os.Executable()
	if err != nil {
		return tunnelErr("internal_error", "cannot resolve vssh binary: "+err.Error())
	}
	if err := os.MkdirAll(tunnelDir(), 0700); err != nil {
		return tunnelErr("internal_error", err.Error())
	}
	id := fmt.Sprintf("%d-%s-%s", time.Now().UnixNano(), sanitizeID(target), strings.TrimPrefix(mode, "-"))
	logPath := filepath.Join(tunnelDir(), id+".log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return tunnelErr("internal_error", err.Error())
	}
	defer lf.Close()
	argv := []string{"fwd", target, mode, spec}
	cmd := exec.Command(self, argv...)
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach into its own session
	if err := cmd.Start(); err != nil {
		return tunnelErr("start_failed", err.Error())
	}
	rec := tunnelRecord{
		ID: id, PID: cmd.Process.Pid, Type: ttype, Target: target, Spec: spec,
		Argv:      append([]string{filepath.Base(self)}, argv...),
		Log:       logPath,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	writeTunnelRecord(rec)
	// Reap on exit so a process that dies in-session does not linger as a zombie.
	go func() { _ = cmd.Wait() }()
	// Brief liveness check: a bad spec makes `vssh fwd` exit immediately.
	time.Sleep(350 * time.Millisecond)
	if !aliveAsTunnel(rec) {
		out := map[string]interface{}{
			"success": false, "tool": "vssh_tunnel", "action": "start",
			"error":    map[string]interface{}{"code": "tunnel_exited", "message": "tunnel process exited immediately; see log_tail"},
			"log_tail": tailFile(logPath, 20),
		}
		removeTunnelRecord(id)
		return out
	}
	return map[string]interface{}{
		"success": true, "tool": "vssh_tunnel", "action": "start",
		"tunnel": tunnelView(rec), "alive": true,
	}
}

func tunnelList() map[string]interface{} {
	recs := readTunnelRecords()
	out := []map[string]interface{}{}
	for _, r := range recs {
		if !aliveAsTunnel(r) {
			removeTunnelRecord(r.ID) // prune dead/stale entries
			continue
		}
		out = append(out, tunnelView(r))
	}
	return map[string]interface{}{
		"success": true, "tool": "vssh_tunnel", "action": "list",
		"count": len(out), "tunnels": out,
	}
}

func tunnelStop(args map[string]interface{}) map[string]interface{} {
	id := strings.TrimSpace(getString(args, "id"))
	if id == "" {
		return tunnelErr("missing_argument", "id is required (get it from vssh_tunnel list)")
	}
	rec, ok := readTunnelRecord(id)
	if !ok {
		return tunnelErr("not_found", "no tunnel with id "+id)
	}
	killed := false
	if aliveAsTunnel(rec) { // only signal a process we positively identified as ours
		_ = syscall.Kill(-rec.PID, syscall.SIGTERM)
		_ = syscall.Kill(rec.PID, syscall.SIGTERM)
		time.Sleep(250 * time.Millisecond)
		if aliveAsTunnel(rec) {
			_ = syscall.Kill(-rec.PID, syscall.SIGKILL)
			_ = syscall.Kill(rec.PID, syscall.SIGKILL)
		}
		killed = true
	}
	removeTunnelRecord(id)
	return map[string]interface{}{
		"success": true, "tool": "vssh_tunnel", "action": "stop",
		"id": id, "killed": killed,
	}
}

func tunnelView(r tunnelRecord) map[string]interface{} {
	return map[string]interface{}{
		"id": r.ID, "pid": r.PID, "type": r.Type, "target": r.Target,
		"spec": r.Spec, "started_at": r.StartedAt, "log": r.Log,
	}
}

func writeTunnelRecord(r tunnelRecord) {
	data, _ := json.MarshalIndent(r, "", "  ")
	_ = os.WriteFile(filepath.Join(tunnelDir(), r.ID+".json"), data, 0600)
}

func removeTunnelRecord(id string) {
	_ = os.Remove(filepath.Join(tunnelDir(), id+".json"))
	_ = os.Remove(filepath.Join(tunnelDir(), id+".log"))
}

func readTunnelRecord(id string) (tunnelRecord, bool) {
	var r tunnelRecord
	data, err := os.ReadFile(filepath.Join(tunnelDir(), id+".json"))
	if err != nil {
		return r, false
	}
	if json.Unmarshal(data, &r) != nil {
		return r, false
	}
	return r, true
}

func readTunnelRecords() []tunnelRecord {
	var out []tunnelRecord
	entries, _ := os.ReadDir(tunnelDir())
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			if r, ok := readTunnelRecord(strings.TrimSuffix(e.Name(), ".json")); ok {
				out = append(out, r)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt < out[j].StartedAt })
	return out
}

// aliveAsTunnel confirms the recorded PID is still OUR forward process (guards
// against PID reuse and zombies) by inspecting `ps` args for the fwd verb and
// target.
func aliveAsTunnel(r tunnelRecord) bool {
	if r.PID <= 0 {
		return false
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(r.PID), "-o", "args=").Output()
	if err != nil {
		return false
	}
	a := strings.TrimSpace(string(out))
	return a != "" && strings.Contains(a, "fwd") && strings.Contains(a, r.Target)
}

func tailFile(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func sanitizeID(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			b.WriteRune(c)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
