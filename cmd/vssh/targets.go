package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zeus-kim/vssh/internal/fleet"
)

// resolveTargets expands a target spec into concrete node names. A spec is a
// comma-separated list of tokens; each token is either a literal host or a
// fleet-memory selector beginning with '@':
//
//	d1,g1            two literal hosts
//	@gpu             every node whose role OR tag OR service is "gpu"
//	@role:gpu        every node with role "gpu"
//	@tag:prod        every node tagged "prod"
//	@service:ollama  every node running service "ollama"
//	@all             every node known to fleet memory
//
// Literal tokens pass through unvalidated (they may be peers not yet in memory);
// a selector that matches no node is an error, so an agent gets a clear signal
// instead of silently running on an empty set. Order is preserved and names are
// de-duplicated, so `@gpu,d1` never runs d1 twice.
func resolveTargets(spec string) ([]string, error) {
	fm, err := fleet.Load()
	if err != nil {
		return nil, err
	}
	return resolveTargetsWith(fm, spec)
}

func resolveTargetsWith(fm *fleet.FleetMemory, spec string) ([]string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("empty target")
	}
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	for _, tok := range strings.Split(spec, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if !strings.HasPrefix(tok, "@") {
			add(tok) // literal host
			continue
		}
		names, err := expandSelector(fm, tok)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			add(n)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("target %q resolved to no nodes", spec)
	}
	return out, nil
}

// expandSelector resolves a single '@' selector against fleet memory.
func expandSelector(fm *fleet.FleetMemory, tok string) ([]string, error) {
	sel := strings.ToLower(strings.TrimPrefix(tok, "@"))
	if sel == "all" {
		names := make([]string, 0, len(fm.Nodes))
		for name := range fm.Nodes {
			names = append(names, name)
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("selector @all matched no nodes (fleet memory empty)")
		}
		sort.Strings(names)
		return names, nil
	}

	var matched []fleet.NodeMemory
	switch {
	case strings.HasPrefix(sel, "role:"):
		matched = fm.Find(fleet.FleetFilter{Role: strings.TrimPrefix(sel, "role:")})
	case strings.HasPrefix(sel, "tag:"):
		matched = fm.Find(fleet.FleetFilter{Tag: strings.TrimPrefix(sel, "tag:")})
	case strings.HasPrefix(sel, "service:"):
		matched = fm.Find(fleet.FleetFilter{Service: strings.TrimPrefix(sel, "service:")})
	default:
		// Bare "@foo" is a convenience union: role OR tag OR service == foo.
		nameSeen := map[string]bool{}
		for _, f := range []fleet.FleetFilter{{Role: sel}, {Tag: sel}, {Service: sel}} {
			for _, m := range fm.Find(f) {
				if !nameSeen[m.Name] {
					nameSeen[m.Name] = true
					matched = append(matched, m)
				}
			}
		}
	}
	if len(matched) == 0 {
		return nil, fmt.Errorf("selector %q matched no nodes", tok)
	}
	names := make([]string, 0, len(matched))
	for _, m := range matched {
		names = append(names, m.Name)
	}
	sort.Strings(names)
	return names, nil
}
