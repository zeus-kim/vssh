package fleet

import "testing"

func seedThree(t *testing.T) *FleetMemory {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	fm, _ := Load()
	fm.SetNode("d1", NodeMemory{Role: "storage", Services: []string{"nfs", "postgres"}, Tags: []string{"gpu", "linux"}})
	fm.AddNote("d1", "지난주 디스크 94% 위기")
	fm.AddNote("d1", "5월26일 전원 장애")
	fm.SetNode("g1", NodeMemory{Role: "gpu-worker", Tags: []string{"arm64", "apple-silicon"}})
	fm.SetNode("v1", NodeMemory{Role: "vps", Tags: []string{"relay"}})
	return fm
}

func names(hits []NodeMemory) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Name
	}
	return out
}

func TestFindByRole(t *testing.T) {
	fm := seedThree(t)
	got := names(fm.Find(FleetFilter{Role: "storage"}))
	if len(got) != 1 || got[0] != "d1" {
		t.Fatalf("role=storage → %v, want [d1]", got)
	}
}

func TestFindByTag(t *testing.T) {
	fm := seedThree(t)
	got := names(fm.Find(FleetFilter{Tag: "gpu"}))
	if len(got) != 1 || got[0] != "d1" {
		t.Fatalf("tag=gpu → %v, want [d1]", got)
	}
}

func TestFindByService(t *testing.T) {
	fm := seedThree(t)
	got := names(fm.Find(FleetFilter{Service: "postgres"}))
	if len(got) != 1 || got[0] != "d1" {
		t.Fatalf("service=postgres → %v, want [d1]", got)
	}
}

func TestFindByText(t *testing.T) {
	fm := seedThree(t)
	got := names(fm.Find(FleetFilter{Text: "디스크"}))
	if len(got) != 1 || got[0] != "d1" {
		t.Fatalf("text=디스크 → %v, want [d1]", got)
	}
}

func TestFindCombined(t *testing.T) {
	fm := seedThree(t)
	// tag=gpu AND text 장애 → only d1 satisfies both
	got := names(fm.Find(FleetFilter{Tag: "gpu", Text: "장애"}))
	if len(got) != 1 || got[0] != "d1" {
		t.Fatalf("tag=gpu+text=장애 → %v, want [d1]", got)
	}
	// tag=relay AND text 장애 → v1 has the tag but not the note → no match
	if got := fm.Find(FleetFilter{Tag: "relay", Text: "장애"}); len(got) != 0 {
		t.Fatalf("tag=relay+text=장애 → %v, want []", names(got))
	}
}

func TestFindEmptyFilterReturnsAll(t *testing.T) {
	fm := seedThree(t)
	if got := fm.Find(FleetFilter{}); len(got) != 3 {
		t.Fatalf("empty filter → %d nodes, want 3", len(got))
	}
}
