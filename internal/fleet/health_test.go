package fleet

import "testing"

func TestParseHealth(t *testing.T) {
	h := ParseHealth("load=4.28\ncores=32\ndisk_pct=94\nmem_pct=52\nfailed=1\n")
	if h.Load != 4.28 || h.Cores != 32 || h.DiskPct != 94 || h.MemPct != 52 || h.Failed != 1 {
		t.Fatalf("parsed wrong: %+v", h)
	}
}

func TestAssessThresholds(t *testing.T) {
	cases := []struct {
		name string
		h    HealthSignals
		want string
	}{
		{"healthy", HealthSignals{Load: 1.0, Cores: 8, DiskPct: 40, MemPct: 30}, HealthOK},
		{"disk-warn", HealthSignals{DiskPct: 92, Cores: 8, Load: 1}, HealthWarn},
		{"disk-critical", HealthSignals{DiskPct: 96, Cores: 8, Load: 1}, HealthCritical},
		{"load-warn", HealthSignals{Cores: 4, Load: 9, DiskPct: 10}, HealthWarn},
		{"load-critical", HealthSignals{Cores: 2, Load: 9, DiskPct: 10}, HealthCritical},
		{"failed-units", HealthSignals{Cores: 8, Load: 1, DiskPct: 10, Failed: 2}, HealthWarn},
		{"mem-warn", HealthSignals{Cores: 8, Load: 1, DiskPct: 10, MemPct: 95}, HealthWarn},
	}
	for _, c := range cases {
		got, issues := Assess(c.h)
		if got != c.want {
			t.Errorf("%s: severity %q, want %q (issues %v)", c.name, got, c.want, issues)
		}
		if c.want != HealthOK && len(issues) == 0 {
			t.Errorf("%s: severity %q but no issue reasons", c.name, got)
		}
	}
}

// A critical signal must not be masked by a later warn signal.
func TestAssessCriticalNotDowngraded(t *testing.T) {
	h := HealthSignals{DiskPct: 97, Cores: 8, Load: 1, MemPct: 95, Failed: 1}
	if sev, _ := Assess(h); sev != HealthCritical {
		t.Fatalf("severity = %q, want critical (disk 97%% + warns)", sev)
	}
}
