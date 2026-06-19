package server

import "testing"

func TestConfigMutations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const pub = "QUJDZGVmMTIzNDU2Nzg5MEFCQ2RlZjEyMzQ1Njc4OTA="

	// authorize (idempotent) + capabilities read back
	if err := AuthorizeKey(pub, "exec,rpc", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeKey(pub, "exec,rpc", "operator-1"); err != nil {
		t.Fatal(err)
	}
	caps, ok := KeyCapabilities(pub)
	if !ok || !caps["exec"] || !caps["rpc"] {
		t.Fatalf("authorize/readback failed: %v %v", caps, ok)
	}
	// revoke
	removed, err := RevokeKey(pub)
	if err != nil || !removed {
		t.Fatalf("revoke failed: removed=%v err=%v", removed, err)
	}
	if _, ok := KeyCapabilities(pub); ok {
		t.Fatal("key still authorized after revoke")
	}

	// node config upsert
	if err := SetNodeConfig("D1", "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if ip := ConfigNodeIP("d1"); ip != "10.0.0.1" {
		t.Fatalf("ConfigNodeIP=%q", ip)
	}
	if err := SetNodeConfig("d1", "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	if ip := ConfigNodeIP("d1"); ip != "10.0.0.2" {
		t.Fatalf("upsert did not replace: %q", ip)
	}

	// node_keys pin upsert
	if err := PinNode("d1", pub); err != nil {
		t.Fatal(err)
	}
	if k := NodeKey("d1"); k != pub {
		t.Fatalf("NodeKey=%q want %q", k, pub)
	}
}
