package main

import (
	"os"
	"testing"
)

func TestToolTunnelValidation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := toolTunnel(map[string]interface{}{"action": "start", "type": "local"}); got["success"] != false {
		t.Fatalf("missing target/spec should fail: %#v", got)
	}
	if got := toolTunnel(map[string]interface{}{"action": "start", "target": "d1", "spec": "8080:localhost:80", "type": "bogus"}); got["success"] != false {
		t.Fatalf("bad type should fail: %#v", got)
	}
	if got := toolTunnel(map[string]interface{}{"action": "frobnicate"}); got["success"] != false {
		t.Fatalf("bad action should fail: %#v", got)
	}
}

func TestToolTunnelListEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := toolTunnel(map[string]interface{}{"action": "list"})
	if got["success"] != true {
		t.Fatalf("list should succeed: %#v", got)
	}
	if got["count"].(int) != 0 {
		t.Fatalf("expected 0 tunnels, got %#v", got["count"])
	}
}

func TestTunnelRecordRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(tunnelDir(), 0700); err != nil {
		t.Fatal(err)
	}
	rec := tunnelRecord{ID: "abc-d1-L", PID: 4242, Type: "local", Target: "d1", Spec: "8080:localhost:80", StartedAt: "2026-06-14T00:00:00Z"}
	writeTunnelRecord(rec)
	got, ok := readTunnelRecord("abc-d1-L")
	if !ok || got.PID != 4242 || got.Target != "d1" {
		t.Fatalf("round-trip mismatch: %#v ok=%v", got, ok)
	}
	if all := readTunnelRecords(); len(all) != 1 {
		t.Fatalf("expected 1 record, got %d", len(all))
	}
	removeTunnelRecord("abc-d1-L")
	if _, ok := readTunnelRecord("abc-d1-L"); ok {
		t.Fatal("record should be gone after remove")
	}
}

func TestTunnelStopNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := toolTunnel(map[string]interface{}{"action": "stop", "id": "nope"}); got["success"] != false {
		t.Fatalf("stop of missing id should fail: %#v", got)
	}
}

func TestTunnelIsOperational(t *testing.T) {
	if !isOperationalMCPTool("vssh_tunnel") {
		t.Fatal("vssh_tunnel should be operational")
	}
}
