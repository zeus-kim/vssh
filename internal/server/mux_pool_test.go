package server

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startAuthorizedServer brings up a real loopback daemon with an isolated HOME
// whose authorized_keys grants this process's identity exec/file/rpc caps, then
// waits for it to listen. Returns the port.
func startAuthorizedServer(t *testing.T) int {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	vd := filepath.Join(tmp, ".vssh")
	t.Setenv("VSSH_AUDIT_DIR", vd) // isolate audit trail (root CI writes to /var/log/vssh otherwise)
	if err := os.MkdirAll(vd, 0700); err != nil {
		t.Fatal(err)
	}
	_, pub := LoadOrCreateIdentity()
	if pub == "" {
		t.Fatal("no identity")
	}
	if err := os.WriteFile(filepath.Join(vd, "authorized_keys"),
		[]byte(pub+" caps=exec,file,rpc testkey\n"), 0600); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	srv := NewServer(port, "s")
	go func() { _ = srv.Run() }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond); err == nil {
			c.Close()
			return port
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("daemon did not start listening")
	return 0
}

// TestPersistentExecReusesSession proves the pool: the first exec establishes a
// MUX session, the second reuses the SAME connection (no re-handshake), and an
// exec after a stale close transparently re-establishes.
func TestPersistentExecReusesSession(t *testing.T) {
	port := startAuthorizedServer(t)

	EnablePersistentExec()
	defer persistentExec.Store(false)
	// Start from a clean pool entry for this port.
	s := muxSessionFor("127.0.0.1", port)
	s.mu.Lock()
	s.closeLocked()
	s.mu.Unlock()

	r1, err := ExecCommandStructured("127.0.0.1", port, "s", "/bin/echo one")
	if err != nil || !r1.Success || !strings.Contains(r1.Stdout, "one") {
		t.Fatalf("first exec: success=%v err=%v out=%q", r1.Success, err, r1.Stdout)
	}
	if s.conn == nil {
		t.Fatal("no pooled session after first exec — pool not used")
	}
	firstConn := s.conn

	r2, err := ExecCommandStructured("127.0.0.1", port, "s", "/bin/echo two")
	if err != nil || !strings.Contains(r2.Stdout, "two") {
		t.Fatalf("second exec: err=%v out=%q", err, r2.Stdout)
	}
	if s.conn != firstConn {
		t.Fatal("second exec did not reuse the pooled connection (re-handshaked)")
	}

	// Simulate the daemon idle-closing the session; the next exec must recover.
	s.mu.Lock()
	s.closeLocked()
	s.mu.Unlock()
	r3, err := ExecCommandStructured("127.0.0.1", port, "s", "/bin/echo three")
	if err != nil || !strings.Contains(r3.Stdout, "three") {
		t.Fatalf("third exec after stale close: err=%v out=%q", err, r3.Stdout)
	}
	if s.conn == nil {
		t.Fatal("session not re-established after a stale close")
	}
}

// TestPersistentExecStillEnforcesPolicy makes sure the fast path is not a policy
// bypass — a command denied on the one-shot path is equally denied over a
// reused MUX session.
func TestPersistentExecPreservesResults(t *testing.T) {
	port := startAuthorizedServer(t)

	EnablePersistentExec()
	defer persistentExec.Store(false)

	// A non-zero exit is reported faithfully over the pooled session.
	r := mustExec(t, port, "sh -c 'exit 7'")
	if r.Success || r.ExitCode != 7 {
		t.Fatalf("exit code not preserved over mux: success=%v code=%d", r.Success, r.ExitCode)
	}
	// And a normal command still returns stdout.
	r = mustExec(t, port, "/bin/echo ok")
	if !r.Success || !strings.Contains(r.Stdout, "ok") {
		t.Fatalf("stdout not preserved over mux: %q", r.Stdout)
	}
}

// safeToRetry must never green-light re-running after a read TIMEOUT (the
// command may have executed and only the reply was lost — double execution),
// but should allow retry after an idle-close (EOF/reset, command never ran).
func TestSafeToRetryNeverRetriesTimeout(t *testing.T) {
	timeouts := []error{
		errors.New("read tcp 1.2.3.4:5->6.7.8.9:10: i/o timeout"),
		errors.New("context deadline exceeded"),
	}
	for _, e := range timeouts {
		if safeToRetry(e) {
			t.Fatalf("safeToRetry(%v) = true; a timed-out command must not be re-run", e)
		}
	}
	idle := []error{
		io.EOF,
		errors.New("read tcp: connection reset by peer"),
		errors.New("write: broken pipe"),
		errors.New("use of closed network connection"),
	}
	for _, e := range idle {
		if !safeToRetry(e) {
			t.Fatalf("safeToRetry(%v) = false; an idle-closed session is safe to retry", e)
		}
	}
	if safeToRetry(nil) {
		t.Fatal("safeToRetry(nil) must be false")
	}
}

func mustExec(t *testing.T, port int, cmd string) ExecCommandResult {
	t.Helper()
	r, err := ExecCommandStructured("127.0.0.1", port, "s", cmd)
	if err != nil {
		t.Fatalf("exec %q: %v", cmd, err)
	}
	return r
}
