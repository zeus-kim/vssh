package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientConfigPathKnown(t *testing.T) {
	t.Setenv("HOME", "/home/x")
	for _, c := range []string{"claude", "cursor", "claude-code", "Cursor", "gemini", "ai-studio", "codex"} {
		if _, ok := clientConfigPath(c); !ok {
			t.Fatalf("client %q should be known", c)
		}
	}
	if _, ok := clientConfigPath("bogus"); ok {
		t.Fatal("bogus client should be unknown")
	}
}

func TestInstallMCPServerMergesAndPreserves(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p, []byte(`{"mcpServers":{"other":{"command":"x"}},"someKey":1}`), 0644)
	got, err := installMCPServer("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Fatalf("path=%s want %s", got, p)
	}
	var cfg map[string]interface{}
	data, _ := os.ReadFile(p)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	servers := cfg["mcpServers"].(map[string]interface{})
	if _, ok := servers["other"]; !ok {
		t.Fatal("existing 'other' server was dropped")
	}
	v, ok := servers["vssh"].(map[string]interface{})
	if !ok || v["command"] == "" {
		t.Fatalf("vssh entry missing/incomplete: %#v", servers["vssh"])
	}
	if _, ok := cfg["someKey"]; !ok {
		t.Fatal("top-level someKey was dropped")
	}
	if _, err := installMCPServer("cursor"); err != nil {
		t.Fatalf("second install (idempotent) failed: %v", err)
	}
}

func TestClientIsTOML(t *testing.T) {
	if !clientIsTOML("codex") {
		t.Error("codex should be TOML")
	}
	for _, c := range []string{"claude", "cursor", "gemini"} {
		if clientIsTOML(c) {
			t.Errorf("%q should be JSON, not TOML", c)
		}
	}
}

func TestInstallMCPServerCodexTOML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p, err := installMCPServer("codex")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "[mcp_servers.vssh]") {
		t.Fatalf("codex config missing server block: %s", data)
	}
	if _, err := installMCPServer("codex"); err != nil {
		t.Fatalf("second codex install (idempotent) failed: %v", err)
	}
	data2, _ := os.ReadFile(p)
	if strings.Count(string(data2), "[mcp_servers.vssh]") != 1 {
		t.Fatalf("codex block duplicated: %s", data2)
	}
}
