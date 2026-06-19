package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTemplatesValid loads every shipped policy template and asserts it parses,
// every rule compiles, and every rule is fully anchored (^...$) — an unanchored
// template rule is a metachar-smuggling hole (docs §6.4), so templates must be clean.
func TestTemplatesValid(t *testing.T) {
	files, err := filepath.Glob("../../policies/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates found: %v", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var p Policy
		if err := json.Unmarshal(data, &p); err != nil {
			t.Errorf("%s: parse: %v", f, err)
			continue
		}
		for _, grp := range [][]string{p.ExecAllow, p.ExecDeny, p.DangerPreapproved} {
			for _, rule := range grp {
				if _, err := regexp.Compile(rule); err != nil {
					t.Errorf("%s: rule %q does not compile: %v", f, rule, err)
				}
				if !strings.HasPrefix(rule, "^") || !strings.HasSuffix(rule, "$") {
					t.Errorf("%s: rule %q is not fully anchored (^...$)", f, rule)
				}
			}
		}
		if p.Name != strings.TrimSuffix(filepath.Base(f), ".json") {
			t.Errorf("%s: name %q != filename", f, p.Name)
		}
	}
}
