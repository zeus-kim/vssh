package server

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests lock in the VSSH_REQUIRE_TLS kill-switch behavior ahead of the
// fleet-wide flip (docs/SECURITY_TRANSPORT_MIGRATION.md §5.3): the daemon must
// refuse plaintext, the client must not silently fall back to plaintext, and a
// normal TLS session must still work. They also guard that enforcement honors
// the same truthy values node_info reports (1/true/yes), so the state a node
// REPORTS via node_info can never disagree with what it ENFORCES.

// seedIdentity creates a temp HOME with an authorized identity key and returns
// the public key. Mirrors the setup used by the policy e2e tests. The identity
// is process-cached (sync.Once), so authorized_keys is always seeded with the
// effective key regardless of test ordering.
func seedIdentity(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	vd := filepath.Join(tmp, ".vssh")
	if err := os.MkdirAll(vd, 0700); err != nil {
		t.Fatal(err)
	}
	_, pub := LoadOrCreateIdentity()
	if pub == "" {
		t.Fatal("no identity")
	}
	if err := os.WriteFile(filepath.Join(vd, "authorized_keys"), []byte(pub+" require-tls-test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return pub
}

// TestServerRequireTLSRefusesPlaintext drives handleConnection over an in-memory
// pipe (plaintext, no TLS record byte) with an AUTHORIZED VAUTH1 line. When
// REQUIRE_TLS is on (in any truthy form) the daemon must refuse at the transport
// layer with AUTH_FAILED and never issue a CHALLENGE; when off it must proceed
// to the challenge. The off-cases prove the gate is the thing making the
// difference (same authorized key, different env).
func TestServerRequireTLSRefusesPlaintext(t *testing.T) {
	cases := []struct {
		env        string
		wantRefuse bool
	}{
		{"1", true},
		{"true", true},
		{"yes", true},
		{"0", false},
		{"", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run("env="+tc.env, func(t *testing.T) {
			pub := seedIdentity(t)
			t.Setenv("VSSH_REQUIRE_TLS", tc.env)

			s := &Server{}
			cli, srv := net.Pipe()
			done := make(chan struct{})
			go func() { s.handleConnection(srv); close(done) }()

			_ = cli.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err := cli.Write([]byte("VAUTH1 " + pub + "\n")); err != nil {
				t.Fatalf("write auth line: %v", err)
			}
			resp, err := bufio.NewReader(cli).ReadString('\n')
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if tc.wantRefuse {
				if !strings.HasPrefix(resp, "AUTH_FAILED") {
					t.Fatalf("env=%q: expected AUTH_FAILED (plaintext refused), got %q", tc.env, resp)
				}
			} else {
				if !strings.HasPrefix(resp, "CHALLENGE") {
					t.Fatalf("env=%q: expected CHALLENGE (plaintext allowed), got %q", tc.env, resp)
				}
			}
			_ = cli.Close()
			<-done
		})
	}
}

// TestClientRequireTLSNoPlaintextFallback verifies dialAuth refuses to fall back
// to plaintext when REQUIRE_TLS is set: against a listener that does not speak
// TLS, the dial must fail with a REQUIRE_TLS error instead of silently
// downgrading to a plaintext VAUTH1 session.
func TestClientRequireTLSNoPlaintextFallback(t *testing.T) {
	seedIdentity(t)
	t.Setenv("VSSH_REQUIRE_TLS", "1")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			c.Close() // never complete a TLS handshake
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	conn, _, derr := dialAuth("127.0.0.1", port, "", 2*time.Second)
	if conn != nil {
		conn.Close()
	}
	if derr == nil {
		t.Fatal("expected dial error under REQUIRE_TLS, got nil (plaintext fallback?)")
	}
	if !strings.Contains(derr.Error(), "REQUIRE_TLS") {
		t.Fatalf("expected REQUIRE_TLS in error, got %v", derr)
	}
}

// TestRequireTLSRoundTrip is the de-risking test for the fleet flip: with
// REQUIRE_TLS=1 set, a normal exec must still succeed end to end (client dials
// TLS, the in-process daemon accepts the TLS session, the command runs). If
// this passes, flipping the kill-switch does not break healthy nodes.
func TestRequireTLSRoundTrip(t *testing.T) {
	seedIdentity(t)
	t.Setenv("VSSH_REQUIRE_TLS", "1")

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portStr, _ := net.SplitHostPort(l.Addr().String())
	port, _ := strconv.Atoi(portStr)
	l.Close()

	srv := NewServer(port, "x")
	go func() { _ = srv.Run() }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, derr := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", portStr), 100*time.Millisecond); derr == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	r, err := ExecCommandStructured("127.0.0.1", port, "x", "/bin/echo tlsok")
	if err != nil {
		t.Fatalf("exec transport err under REQUIRE_TLS: %v", err)
	}
	if !r.Success || !strings.Contains(r.Stdout, "tlsok") {
		t.Fatalf("expected success+tlsok, got success=%v code=%q stdout=%q err=%q", r.Success, r.ErrorCode, r.Stdout, r.Error)
	}
}
