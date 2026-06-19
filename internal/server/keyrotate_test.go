package server

import (
	"os"
	"testing"
)

func TestRotateIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if CurrentIdentityPub() != "" {
		t.Fatal("expected no identity before first generate")
	}
	pub1, backup1, err := RotateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if pub1 == "" {
		t.Fatal("empty new pubkey")
	}
	if backup1 != "" {
		t.Fatalf("no backup expected on first generate, got %q", backup1)
	}
	if got := CurrentIdentityPub(); got != pub1 {
		t.Fatalf("CurrentIdentityPub=%q want %q", got, pub1)
	}
	pub2, backup2, err := RotateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if pub2 == pub1 {
		t.Fatal("rotation produced an identical key")
	}
	if backup2 == "" {
		t.Fatal("expected a backup on second rotation")
	}
	if _, e := os.Stat(backup2); e != nil {
		t.Fatalf("backup file missing: %v", e)
	}
	if got := CurrentIdentityPub(); got != pub2 {
		t.Fatalf("CurrentIdentityPub=%q want latest %q", got, pub2)
	}
}
