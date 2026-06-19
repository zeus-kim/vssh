package server

import (
	"os/user"
	"testing"
)

func TestExpandTransferPathUsesEffectiveUserHome(t *testing.T) {
	u := &user.User{Username: "runtime", HomeDir: "/home/runtime"}

	got := expandTransferPath("~/artifact.bin", u)
	if got != "/home/runtime/artifact.bin" {
		t.Fatalf("expandTransferPath = %q, want /home/runtime/artifact.bin", got)
	}
}

func TestExpandTransferPathLeavesAbsolutePath(t *testing.T) {
	u := &user.User{Username: "runtime", HomeDir: "/home/runtime"}

	got := expandTransferPath("/tmp/artifact.bin", u)
	if got != "/tmp/artifact.bin" {
		t.Fatalf("expandTransferPath = %q, want /tmp/artifact.bin", got)
	}
}
