package server

import (
	"runtime"
	"testing"
)

func TestRPCNodeInfo(t *testing.T) {
	DaemonVersion = "9.9.9-test"
	t.Setenv("VSSH_REQUIRE_VAUTH", "1")
	t.Setenv("VSSH_REQUIRE_TLS", "")
	info, err := rpcNodeInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "9.9.9-test" {
		t.Fatalf("version=%q", info.Version)
	}
	if info.OS != runtime.GOOS || info.Arch != runtime.GOARCH {
		t.Fatalf("os/arch mismatch: %s/%s", info.OS, info.Arch)
	}
	if !info.RequireVAUTH {
		t.Fatal("expected RequireVAUTH true")
	}
	if info.RequireTLS {
		t.Fatal("expected RequireTLS false from empty env")
	}
	if info.UptimeSeconds < 0 {
		t.Fatalf("uptime negative: %d", info.UptimeSeconds)
	}
	if info.AuthModel == "" || info.Hostname == "" {
		t.Fatalf("missing fields: %#v", info)
	}
}

func TestEnvEnabled(t *testing.T) {
	cases := map[string]bool{"1": true, "true": true, "TRUE": true, "yes": true, "": false, "0": false, "off": false}
	for v, want := range cases {
		t.Setenv("VSSH_TEST_FLAG", v)
		if got := envEnabled("VSSH_TEST_FLAG"); got != want {
			t.Fatalf("envEnabled(%q)=%v want %v", v, got, want)
		}
	}
}
