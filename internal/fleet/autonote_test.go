package fleet

import (
	"strings"
	"testing"
)

func hasNote(notes []string, substr string) bool {
	for _, n := range notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

func TestExtractDisk(t *testing.T) {
	out := `Filesystem      Size  Used Avail Use% Mounted on
/dev/sda1       100G   91G   9G   91% /
tmpfs           4.0G  0.5G  3.5G  13% /run`
	notes := ExtractNotes("df -h", out)
	if !hasNote(notes, "disk /dev/sda1 at 91% (auto)") {
		t.Fatalf("disk note missing: %v", notes)
	}
	// the 13% partition must not be flagged
	if hasNote(notes, "13%") {
		t.Fatalf("low-usage partition flagged: %v", notes)
	}
}

func TestExtractServiceFromCommandHint(t *testing.T) {
	out := `● nginx.service - A high performance web server
   Loaded: loaded (/lib/systemd/system/nginx.service; enabled)
   Active: failed (Result: exit-code) since Mon`
	notes := ExtractNotes("systemctl status nginx", out)
	if !hasNote(notes, "service nginx failed (auto)") {
		t.Fatalf("service note missing: %v", notes)
	}
}

func TestExtractServiceDead(t *testing.T) {
	out := `● redis.service
   Active: inactive (dead)`
	notes := ExtractNotes("", out)
	if !hasNote(notes, "service redis failed (auto)") {
		t.Fatalf("dead service note missing: %v", notes)
	}
}

func TestExtractLoad(t *testing.T) {
	out := ` 14:02:01 up 10 days,  3:22,  2 users,  load average: 12.50, 8.30, 5.10`
	notes := ExtractNotes("uptime", out)
	if !hasNote(notes, "high load avg 12.50 (auto)") {
		t.Fatalf("load note missing: %v", notes)
	}
}

func TestExtractLoadLowIgnored(t *testing.T) {
	out := ` load average: 0.10, 0.20, 0.30`
	notes := ExtractNotes("uptime", out)
	if hasNote(notes, "high load") {
		t.Fatalf("low load should not be flagged: %v", notes)
	}
}

func TestExtractNvidia(t *testing.T) {
	out := `Failed to initialize NVML: Driver/library version mismatch`
	notes := ExtractNotes("nvidia-smi", out)
	if !hasNote(notes, "nvidia driver issue (auto)") {
		t.Fatalf("nvidia note missing: %v", notes)
	}
}

func TestExtractFallback(t *testing.T) {
	out := "everything is fine here\nsecond line"
	notes := ExtractNotes("echo", out)
	if len(notes) != 1 || !strings.Contains(notes[0], "everything is fine here") {
		t.Fatalf("fallback note wrong: %v", notes)
	}
}

func TestAutoNotePersists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fm, _ := Load()
	extracted := fm.AutoNote("d1", "df -h", "/dev/sda1 100G 91G 9G 91% /")
	if len(extracted) == 0 {
		t.Fatal("expected at least one extracted note")
	}
	mem, _ := fm.GetNode("d1")
	if len(mem.Notes) != len(extracted) {
		t.Fatalf("notes on node = %d, want %d", len(mem.Notes), len(extracted))
	}
}
