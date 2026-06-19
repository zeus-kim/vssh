package server

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRPCCapabilityEnforcement locks in the fix for the privilege gap where the
// "rpc" capability alone let file_write/job_start bypass the file/exec caps and
// the per-key policy. A caps=rpc key may run read-only typed RPCs but NOT
// file_write/job_start; granting file+exec re-enables them.
func TestRPCCapabilityEnforcement(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("VSSH_REQUIRE_TLS", "0")
	vd := filepath.Join(tmp, ".vssh")
	if err := os.MkdirAll(vd, 0700); err != nil {
		t.Fatal(err)
	}
	_, pub := LoadOrCreateIdentity()
	if pub == "" {
		t.Fatal("no identity")
	}
	akpath := filepath.Join(vd, "authorized_keys")
	writeAK := func(caps string) {
		if err := os.WriteFile(akpath, []byte(pub+" caps="+caps+" rpckey\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeAK("rpc")

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
	call := func(method string, params map[string]interface{}) RPCResponse {
		r, cerr := CallRPC("127.0.0.1", port, "x", method, params, 5*time.Second)
		if cerr != nil {
			t.Fatalf("%s transport err: %v", method, cerr)
		}
		return r
	}

	// caps=rpc: read-only typed RPC allowed
	if r := call("node_info", nil); !r.Success {
		t.Errorf("node_info should be allowed for caps=rpc: %+v", r)
	}
	// caps=rpc: file_write must be cap-denied (and must NOT write)
	wp := filepath.Join(tmp, "w.txt")
	if r := call("file_write", map[string]interface{}{"path": wp, "content": "x"}); r.Success || !strings.Contains(r.Error, "cap") {
		t.Errorf("file_write should be cap-denied for caps=rpc: %+v", r)
	}
	if _, serr := os.Stat(wp); serr == nil {
		t.Error("file_write wrote the file despite cap denial (bypass!)")
	}
	// caps=rpc: job_start must be cap-denied
	if r := call("job_start", map[string]interface{}{"command": "echo hi"}); r.Success || !strings.Contains(r.Error, "cap") {
		t.Errorf("job_start should be cap-denied for caps=rpc: %+v", r)
	}

	// grant file+exec -> now allowed
	writeAK("rpc,file,exec")
	if r := call("file_write", map[string]interface{}{"path": wp, "content": "x"}); !r.Success {
		t.Errorf("file_write should be allowed with 'file' cap: %+v", r)
	}
	if r := call("job_start", map[string]interface{}{"command": "echo hi"}); !r.Success {
		t.Errorf("job_start should be allowed with 'exec' cap: %+v", r)
	}
}
