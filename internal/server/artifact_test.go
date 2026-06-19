package server

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestRPCArtifactCollectFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.txt")
	if err := os.WriteFile(path, []byte("abcdef"), 0644); err != nil {
		t.Fatal(err)
	}

	artifact, err := rpcArtifactCollect(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Type != "file" {
		t.Fatalf("type=%s", artifact.Type)
	}
	if !artifact.Truncated {
		t.Fatal("expected truncated artifact")
	}
	decoded, err := base64.StdEncoding.DecodeString(artifact.Content)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "abc" {
		t.Fatalf("content=%q", decoded)
	}
}

func TestRPCArtifactCollectDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}

	artifact, err := rpcArtifactCollect(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Type != "directory" {
		t.Fatalf("type=%s", artifact.Type)
	}
	if len(artifact.Entries) != 1 {
		t.Fatalf("entries=%d", len(artifact.Entries))
	}
	if artifact.Entries[0].Name != "a.txt" {
		t.Fatalf("name=%q", artifact.Entries[0].Name)
	}
}
