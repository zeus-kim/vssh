package intent

import (
	"strings"
	"testing"
)

func TestResolveDiskCheck(t *testing.T) {
	p, ok := Resolve("disk check")
	if !ok || p.Intent != "disk-check" {
		t.Fatalf("disk check → %q,%v", p.Intent, ok)
	}
	if len(p.Commands) == 0 || !strings.HasPrefix(p.Commands[0], "df -h") {
		t.Fatalf("disk-check commands = %v", p.Commands)
	}
}

func TestResolveServiceCheckFillsArg(t *testing.T) {
	p, ok := Resolve("service check nginx")
	if !ok || p.Intent != "service-check" {
		t.Fatalf("service check → %q,%v", p.Intent, ok)
	}
	if p.Arg != "nginx" {
		t.Fatalf("arg = %q, want nginx", p.Arg)
	}
	if p.Commands[0] != "systemctl status nginx --no-pager" {
		t.Fatalf("filled command = %q", p.Commands[0])
	}
}

func TestResolvePrefersMoreSpecific(t *testing.T) {
	// "service check" must beat the generic "service logs"/others, and "log
	// check" must not steal "service check".
	p, ok := Resolve("service check redis")
	if !ok || p.Intent != "service-check" {
		t.Fatalf("expected service-check, got %q", p.Intent)
	}
}

func TestResolveGPUAndProcess(t *testing.T) {
	if p, ok := Resolve("gpu status"); !ok || p.Commands[0] != "nvidia-smi" {
		t.Fatalf("gpu status → %v,%v", p.Commands, ok)
	}
	if p, ok := Resolve("process check"); !ok || !strings.Contains(p.Commands[0], "ps aux") {
		t.Fatalf("process check → %v,%v", p.Commands, ok)
	}
}

func TestResolveNoMatch(t *testing.T) {
	if p, ok := Resolve("make me a sandwich"); ok {
		t.Fatalf("unexpected match: %q", p.Intent)
	}
}

func TestBuiltinCountAtLeast20(t *testing.T) {
	if n := len(builtinIntents()); n < 20 {
		t.Fatalf("only %d built-in intents, want >= 20", n)
	}
}

func TestUserIntentOverridesBuiltin(t *testing.T) {
	intents := []Intent{
		{Name: "disk-check", Keywords: []string{"disk"}, Commands: []string{"echo custom"}},
	}
	p, ok := resolveWith(intents, "disk")
	if !ok || p.Commands[0] != "echo custom" {
		t.Fatalf("override not applied: %v", p.Commands)
	}
}

func TestArgNotLeakedWhenNotNeeded(t *testing.T) {
	p, _ := Resolve("disk check")
	if p.Arg != "" {
		t.Fatalf("disk-check should not extract an arg, got %q", p.Arg)
	}
	for _, c := range p.Commands {
		if strings.Contains(c, "{{") {
			t.Fatalf("unfilled placeholder in %q", c)
		}
	}
}
