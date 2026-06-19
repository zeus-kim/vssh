package fleet

import (
	"testing"
)

func TestSetNodeAndPersist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fm, err := Load()
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if len(fm.Nodes) != 0 {
		t.Fatalf("expected empty store, got %d nodes", len(fm.Nodes))
	}

	fm.SetNode("d1", NodeMemory{Role: "storage", Services: []string{"postgres", "nfs", "nfs"}, Tags: []string{"gpu", " linux "}})
	if err := fm.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	mem, ok := got.GetNode("d1")
	if !ok {
		t.Fatal("d1 not found after reload")
	}
	if mem.Role != "storage" {
		t.Errorf("role = %q, want storage", mem.Role)
	}
	// services normalized: trimmed, de-duped, sorted.
	if len(mem.Services) != 2 || mem.Services[0] != "nfs" || mem.Services[1] != "postgres" {
		t.Errorf("services = %v, want [nfs postgres]", mem.Services)
	}
	if len(mem.Tags) != 2 || mem.Tags[0] != "gpu" || mem.Tags[1] != "linux" {
		t.Errorf("tags = %v, want [gpu linux]", mem.Tags)
	}
	if mem.UpdatedAt == "" {
		t.Error("updated_at not stamped")
	}
}

func TestSetNodePreservesNotes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fm, _ := Load()
	fm.AddNote("d1", "disk hit 94%")
	fm.SetNode("d1", NodeMemory{Role: "storage"})
	mem, _ := fm.GetNode("d1")
	if len(mem.Notes) != 1 {
		t.Fatalf("notes dropped by SetNode: %d", len(mem.Notes))
	}
	if mem.Role != "storage" {
		t.Errorf("role not applied: %q", mem.Role)
	}
}

func TestAddNoteRollingCap(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fm, _ := Load()
	for i := 0; i < maxNotes+5; i++ {
		fm.AddNote("d1", "event")
	}
	mem, _ := fm.GetNode("d1")
	if len(mem.Notes) != maxNotes {
		t.Errorf("note log = %d, want capped at %d", len(mem.Notes), maxNotes)
	}
}

func TestGetMissingNode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fm, _ := Load()
	if _, ok := fm.GetNode("nope"); ok {
		t.Error("expected missing node to report ok=false")
	}
}
