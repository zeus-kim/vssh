package diff

import (
	"strings"
	"testing"
	"time"
)

const sample = `
{"ts":"2026-06-17T02:00:00Z","remote":"10.0.0.5:5001","command":"sed -i 's/listen 80/listen 443/' /etc/nginx/nginx.conf","success":true,"exit_code":0,"duration_ms":12,"key_name":"ops"}
{"ts":"2026-06-17T02:00:05Z","remote":"10.0.0.5:5001","command":"systemctl restart nginx","success":true,"exit_code":0,"duration_ms":300,"key_name":"ops"}
{"ts":"2026-06-17T02:00:08Z","remote":"10.0.0.5:5001","command":"systemctl is-active nginx","success":true,"exit_code":0,"duration_ms":20,"key_name":"ops"}
{"ts":"2026-06-17T05:00:00Z","remote":"10.0.0.9:6001","command":"df -h","success":true,"exit_code":0,"duration_ms":15,"key_name":"audit"}
{"ts":"2026-06-17T05:00:02Z","remote":"10.0.0.9:6001","command":"systemctl restart redis","success":false,"exit_code":1,"duration_ms":500,"key_name":"audit"}
`

func TestAnalyzeGroupsSessions(t *testing.T) {
	sessions, err := AnalyzeReader(strings.NewReader(sample), Options{})
	if err != nil {
		t.Fatalf("AnalyzeReader: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}
	// first session: nginx edit + restart by ops
	s0 := sessions[0]
	if s0.KeyName != "ops" || len(s0.Outcomes) != 3 {
		t.Fatalf("session0 = key %q, %d outcomes; want ops,3", s0.KeyName, len(s0.Outcomes))
	}
	wantSummary := "nginx.conf changed (listen 80 → 443), service nginx restarted, result: OK"
	if s0.Summary != wantSummary {
		t.Fatalf("session0 summary = %q\n want %q", s0.Summary, wantSummary)
	}
}

func TestAnalyzeReportsFailure(t *testing.T) {
	sessions, _ := AnalyzeReader(strings.NewReader(sample), Options{})
	s1 := sessions[1]
	if s1.KeyName != "audit" {
		t.Fatalf("session1 key = %q, want audit", s1.KeyName)
	}
	if !strings.Contains(s1.Summary, "service redis restarted") {
		t.Fatalf("session1 summary missing restart: %q", s1.Summary)
	}
	if !strings.Contains(s1.Summary, "1 failed") {
		t.Fatalf("session1 summary should report failure: %q", s1.Summary)
	}
}

func TestSessionSplitByGap(t *testing.T) {
	// same key/endpoint but 3h apart → two sessions (default gap 5m)
	sessions, _ := AnalyzeReader(strings.NewReader(`
{"ts":"2026-06-17T01:00:00Z","remote":"r","command":"uptime","success":true,"key_name":"k"}
{"ts":"2026-06-17T04:00:00Z","remote":"r","command":"uptime","success":true,"key_name":"k"}
`), Options{})
	if len(sessions) != 2 {
		t.Fatalf("gap split: got %d sessions, want 2", len(sessions))
	}
}

func TestLastNKeepsNewest(t *testing.T) {
	sessions, _ := AnalyzeReader(strings.NewReader(sample), Options{LastN: 1})
	if len(sessions) != 1 || sessions[0].KeyName != "audit" {
		t.Fatalf("LastN=1 should keep newest (audit); got %d / %q", len(sessions), func() string {
			if len(sessions) > 0 {
				return sessions[0].KeyName
			}
			return ""
		}())
	}
}

func TestSinceFilter(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-17T05:30:00Z")
	sessions, _ := AnalyzeReader(strings.NewReader(sample), Options{Since: time.Hour, Now: now})
	if len(sessions) != 1 || sessions[0].KeyName != "audit" {
		t.Fatalf("Since=1h@05:30 should keep only the 05:00 session; got %d", len(sessions))
	}
}

func TestHeadlineForReadOnly(t *testing.T) {
	if h := headlineFor("df -h | tail"); h != "" {
		t.Fatalf("read-only command produced headline %q", h)
	}
	if h := headlineFor("echo hi > /etc/motd"); !strings.Contains(h, "motd written") {
		t.Fatalf("redirect headline = %q", h)
	}
}

// A path like /Users/alice/Projects contains the substring "s/alice/Projects/"
// — it must NOT be mistaken for a sed substitution.
func TestHeadlineIgnoresSedLikePaths(t *testing.T) {
	cmd := `cat /Users/alice/Projects/app/readonly.json`
	if h := headlineFor(cmd); h != "" {
		t.Fatalf("path mistaken for edit: %q", h)
	}
}

// `2>/dev/null` is a discard, not a file write — it must not produce a headline.
func TestHeadlineIgnoresDevNullRedirect(t *testing.T) {
	cmd := `echo "=== DISK ==="; df -h / 2>/dev/null | tail -n +1`
	if h := headlineFor(cmd); h != "" {
		t.Fatalf("/dev/null redirect mistaken for write: %q", h)
	}
}

// A real write after a /dev/null discard should still be reported.
func TestHeadlineReportsRealWritePastSink(t *testing.T) {
	cmd := `command 2>/dev/null > /etc/motd`
	if h := headlineFor(cmd); h != "motd written" {
		t.Fatalf("real write past sink = %q, want \"motd written\"", h)
	}
}

func TestHeadlineRealSedEdit(t *testing.T) {
	h := headlineFor(`sed -i 's/listen 80/listen 443/' /etc/nginx/nginx.conf`)
	if h != "nginx.conf changed (listen 80 → 443)" {
		t.Fatalf("sed headline = %q", h)
	}
}
