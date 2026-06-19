package main

import (
	"os"
	"testing"

	"github.com/zeus-kim/vssh/internal/server"
)

func TestConfigToolsGatedOffByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	os.Unsetenv("VSSH_ALLOW_CONFIG_WRITE")
	r := toolConfigAuthorizeKey(map[string]interface{}{"pubkey": "abc"})
	if r["success"] != false {
		t.Fatalf("should be gated off: %#v", r)
	}
	if r["error"].(map[string]interface{})["code"] != "config_write_disabled" {
		t.Fatalf("wrong code: %#v", r["error"])
	}
	// list is read-only and ungated
	if toolConfigList(nil)["success"] != true {
		t.Fatal("list should work ungated")
	}
}

func TestConfigToolsEnabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("VSSH_ALLOW_CONFIG_WRITE", "1")
	if toolConfigAuthorizeKey(map[string]interface{}{"pubkey": "PUBxyz", "caps": "exec"})["success"] != true {
		t.Fatal("authorize failed")
	}
	if !server.IsAuthorizedKey("PUBxyz") {
		t.Fatal("key not authorized after tool")
	}
	if toolConfigSetNode(map[string]interface{}{"name": "d1", "ip": "10.0.0.5"})["success"] != true {
		t.Fatal("set-node failed")
	}
	if server.ConfigNodeIP("d1") != "10.0.0.5" {
		t.Fatal("set-node not applied")
	}
	if toolConfigRevokeKey(map[string]interface{}{"pubkey": "PUBxyz"})["success"] != true {
		t.Fatal("revoke failed")
	}
}
