package server

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPolicyEnforcementE2E spins up a real daemon on loopback with a temp HOME,
// a policied key, and exercises allow / deny / danger_preapproved / fail-closed
// through the actual client exec paths (single EXEJ and mux), then checks the
// audit log carries the matched rule id and preapproved flag.
func TestPolicyEnforcementE2E(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	vd := filepath.Join(tmp, ".vssh")
	if err := os.MkdirAll(filepath.Join(vd, "policies"), 0700); err != nil {
		t.Fatal(err)
	}
	// Identity (shared by the in-process daemon + client for the test).
	_, pub := LoadOrCreateIdentity()
	if pub == "" {
		t.Fatal("no identity")
	}
	if err := os.WriteFile(filepath.Join(vd, "authorized_keys"),
		[]byte(pub+" caps=exec policy=demo testkey\n"), 0600); err != nil {
		t.Fatal(err)
	}
	policy := `{"name":"demo",
	  "exec_allow":["^/bin/echo hello$"],
	  "exec_deny":["^/bin/echo blocked$"],
	  "danger_preapproved":["^/bin/echo danger$"]}`
	polPath := filepath.Join(vd, "policies", "demo.json")
	if err := os.WriteFile(polPath, []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	srv := NewServer(port, "testsecret")
	go func() { _ = srv.Run() }()
	// wait for listen
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond); err == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	exec1 := func(cmd string) ExecCommandResult {
		r, err := ExecCommandStructured("127.0.0.1", port, "testsecret", cmd)
		if err != nil {
			t.Fatalf("exec %q transport err: %v", cmd, err)
		}
		return r
	}

	// allow
	if r := exec1("/bin/echo hello"); !r.Success || !strings.Contains(r.Stdout, "hello") {
		t.Errorf("allow: got success=%v code=%q stdout=%q err=%q", r.Success, r.ErrorCode, r.Stdout, r.Error)
	}
	// deny (explicit deny rule)
	if r := exec1("/bin/echo blocked"); r.Success || r.ErrorCode != ErrCodePolicyDenied {
		t.Errorf("deny: expected policy_denied, got success=%v code=%q", r.Success, r.ErrorCode)
	}
	// no match -> refuse
	if r := exec1("/bin/echo whatever"); r.Success || r.ErrorCode != ErrCodePolicyDenied {
		t.Errorf("no-match: expected policy_denied, got success=%v code=%q", r.Success, r.ErrorCode)
	}
	// metachar smuggle on an allowed prefix -> refuse (anchoring)
	if r := exec1("/bin/echo hello; rm -rf /tmp/x"); r.Success || r.ErrorCode != ErrCodePolicyDenied {
		t.Errorf("smuggle: expected policy_denied, got success=%v code=%q stdout=%q", r.Success, r.ErrorCode, r.Stdout)
	}
	// danger_preapproved -> runs
	if r := exec1("/bin/echo danger"); !r.Success || !strings.Contains(r.Stdout, "danger") {
		t.Errorf("danger: expected success, got success=%v code=%q", r.Success, r.ErrorCode)
	}
	// mux path also enforces
	if rs, err := RunMux("127.0.0.1", port, "testsecret", []string{"/bin/echo hello", "/bin/echo blocked"}); err != nil {
		t.Fatalf("mux err: %v", err)
	} else {
		if len(rs) != 2 {
			t.Fatalf("mux: want 2 results, got %d", len(rs))
		}
		if !rs[0].Success {
			t.Errorf("mux allow failed: %q", rs[0].Error)
		}
		if rs[1].Success || rs[1].ErrorCode != ErrCodePolicyDenied {
			t.Errorf("mux deny: expected policy_denied, got success=%v code=%q", rs[1].Success, rs[1].ErrorCode)
		}
	}

	// fail-closed: remove the policy file -> a policied key becomes unusable.
	if err := os.Remove(polPath); err != nil {
		t.Fatal(err)
	}
	if r := exec1("/bin/echo hello"); r.Success || r.ErrorCode != ErrCodePolicyDenied {
		t.Errorf("fail-closed: expected policy_denied after policy removed, got success=%v code=%q", r.Success, r.ErrorCode)
	}

	// audit: matched rule ids and preapproved flag recorded.
	auditCheck(t, filepath.Join(vd, "audit.log"))
}

func auditCheck(t *testing.T, path string) {
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("audit open: %v", err)
	}
	defer f.Close()
	var sawAllow, sawPreapproved, sawNoMatch, sawLoadErr bool
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var r map[string]interface{}
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		rule, _ := r["policy_rule"].(string)
		switch {
		case rule == "exec_allow[0]":
			sawAllow = true
		case strings.HasPrefix(rule, "danger_preapproved"):
			if pre, _ := r["preapproved"].(bool); pre {
				sawPreapproved = true
			}
		case rule == "no_match":
			sawNoMatch = true
		case rule == "load_error":
			sawLoadErr = true
		}
	}
	if !sawAllow || !sawPreapproved || !sawNoMatch || !sawLoadErr {
		t.Errorf("audit missing rule ids: allow=%v preapproved=%v no_match=%v load_error=%v",
			sawAllow, sawPreapproved, sawNoMatch, sawLoadErr)
	}
}

// TestHostIdentityVerification proves the opt-in host-identity check blocks a
// connection whose reached daemon key differs from the expected key (the
// name->wrong-host misroute class), and allows it when the expectation is
// cleared. Deterministic: a bogus expected key guarantees a mismatch.
func TestHostIdentityVerification(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	vd := filepath.Join(tmp, ".vssh")
	if err := os.MkdirAll(vd, 0700); err != nil {
		t.Fatal(err)
	}
	_, pub := LoadOrCreateIdentity()
	if err := os.WriteFile(filepath.Join(vd, "authorized_keys"), []byte(pub+" self\n"), 0600); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	go func() { _ = NewServer(port, "s").Run() }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond); err == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// host-identity verification is default-ON now; expectations drive the check

	// mismatch -> hard fail (refuse to run on the wrong host)
	SetExpectedHostKey("127.0.0.1", "AAAAbogus_wrong_daemon_key_AAAA=")
	if _, err := ExecCommandStructured("127.0.0.1", port, "s", "echo should_not_run"); err == nil {
		t.Error("expected host-identity mismatch to fail the exec, but it succeeded")
	}

	// expectation cleared -> proceeds normally
	SetExpectedHostKey("127.0.0.1", "")
	if r, err := ExecCommandStructured("127.0.0.1", port, "s", "echo ok"); err != nil || !r.Success {
		t.Errorf("expected success after clearing expectation, got err=%v success=%v", err, r.Success)
	}
}

// TestPolicyCheckRPC verifies the read-only policy_check decision used by the MCP
// danger_preapproved flow: deny / preapproved / allow / none, without executing.
func TestPolicyCheckRPC(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	vd := filepath.Join(tmp, ".vssh")
	os.MkdirAll(filepath.Join(vd, "policies"), 0700)
	_, pub := LoadOrCreateIdentity()
	os.WriteFile(filepath.Join(vd, "authorized_keys"), []byte(pub+" caps=exec,rpc policy=demo self\n"), 0600)
	os.WriteFile(filepath.Join(vd, "policies", "demo.json"), []byte(`{"name":"demo","exec_allow":["^/bin/echo ok$"],"danger_preapproved":["^/usr/bin/systemctl restart vsshd$"]}`), 0600)
	port := freePort(t)
	go func() { _ = NewServer(port, "s").Run() }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond); err == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	check := func(cmd string) string {
		resp, err := CallRPC("127.0.0.1", port, "s", "policy_check", map[string]interface{}{"command": cmd}, 5*time.Second)
		if err != nil || !resp.Success {
			t.Fatalf("policy_check rpc err=%v ok=%v", err, resp.Success)
		}
		d, _ := resp.Data.(map[string]interface{})
		s, _ := d["decision"].(string)
		return s
	}
	if got := check("/usr/bin/systemctl restart vsshd"); got != "preapproved" {
		t.Errorf("danger cmd: got decision %q, want preapproved", got)
	}
	if got := check("/bin/echo ok"); got != "allow" {
		t.Errorf("allow cmd: got %q, want allow", got)
	}
	if got := check("/bin/rm -rf /"); got != "deny" {
		t.Errorf("unlisted cmd: got %q, want deny", got)
	}
}

// freePort returns an OS-assigned free TCP port, avoiding hardcoded ports that
// can collide with ephemeral source ports (e.g. a Tailscale outbound connection).
func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return p
}
