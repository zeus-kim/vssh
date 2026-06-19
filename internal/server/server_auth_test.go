package server

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// TestServerRejectsLegacyHMACAuth locks in the P4 change: the legacy
// shared-secret HMAC auth path was removed, so the daemon must reject any
// non-VAUTH1 auth line (e.g. a legacy HMAC-style token). Only per-node
// Ed25519 VAUTH1 is accepted.
func TestServerRejectsLegacyHMACAuth(t *testing.T) {
	t.Setenv("VSSH_REQUIRE_TLS", "0") // isolate: test the auth line, not TLS refusal
	s := &Server{Secret: "test-secret"}

	cli, srv := net.Pipe()
	done := make(chan struct{})
	go func() { s.handleConnection(srv); close(done) }()

	token := "1700000000:deadbeefcafebabedeadbeefcafebabe" // legacy HMAC-style auth line
	_ = cli.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := cli.Write([]byte(token + "\n")); err != nil {
		t.Fatalf("write auth line: %v", err)
	}
	resp, err := bufio.NewReader(cli).ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.HasPrefix(resp, "AUTH_FAILED") {
		t.Fatalf("expected AUTH_FAILED for legacy HMAC token, got %q", resp)
	}
	_ = cli.Close()
	<-done
}
