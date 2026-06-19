// Package intent maps a short natural-language request ("disk check",
// "service check nginx") to a concrete command plan using rule-based keyword
// matching — no LLM, no network. Built-in intents cover the common ops surface;
// users extend or override them via ~/.vssh/intents.json. Planning never
// executes anything; the caller decides whether to run the plan.
package intent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Intent is a named plan template: when any of Keywords matches the query, its
// Commands become the plan. {{arg}} in a command is filled from the trailing
// words of the query (e.g. "service check nginx" → arg="nginx").
type Intent struct {
	Name      string   `json:"name"`
	Keywords  []string `json:"keywords"`
	Commands  []string `json:"commands"`
	Rationale string   `json:"rationale,omitempty"`
	NeedsArg  bool     `json:"needs_arg,omitempty"`
}

// Plan is the result of resolving a query.
type Plan struct {
	Intent          string   `json:"intent"`
	Commands        []string `json:"commands"`
	Rationale       string   `json:"rationale"`
	MatchedKeywords []string `json:"matched_keywords"`
	Arg             string   `json:"arg,omitempty"`
}

// IntentsPath is where user-defined/override intents live.
func IntentsPath() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".vssh", "intents.json")
	}
	return "/etc/vssh/intents.json"
}

// Load returns the merged intent set: built-ins overlaid by any user intents
// from ~/.vssh/intents.json (user entries with the same name win).
func Load() []Intent {
	merged := map[string]Intent{}
	order := []string{}
	add := func(in Intent) {
		if _, seen := merged[in.Name]; !seen {
			order = append(order, in.Name)
		}
		merged[in.Name] = in
	}
	for _, in := range builtinIntents() {
		add(in)
	}
	for _, in := range loadUserIntents() {
		if in.Name == "" || len(in.Commands) == 0 {
			continue
		}
		add(in)
	}
	out := make([]Intent, 0, len(order))
	for _, name := range order {
		out = append(out, merged[name])
	}
	return out
}

func loadUserIntents() []Intent {
	data, err := os.ReadFile(IntentsPath())
	if err != nil {
		return nil
	}
	var out []Intent
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return out
}

// Resolve picks the best intent for a query and returns a filled plan. ok=false
// means nothing matched. The best match is the intent with the most keyword
// hits (longer keywords break ties, so "service check" beats "check").
func Resolve(query string) (Plan, bool) {
	return resolveWith(Load(), query)
}

func resolveWith(intents []Intent, query string) (Plan, bool) {
	low := strings.ToLower(strings.TrimSpace(query))
	if low == "" {
		return Plan{}, false
	}

	type scored struct {
		in       Intent
		matched  []string
		score    int
		keyChars int
	}
	var best *scored
	for _, in := range intents {
		var matched []string
		keyChars := 0
		for _, kw := range in.Keywords {
			k := strings.ToLower(strings.TrimSpace(kw))
			if k != "" && strings.Contains(low, k) {
				matched = append(matched, kw)
				keyChars += len(k)
			}
		}
		if len(matched) == 0 {
			continue
		}
		cand := scored{in: in, matched: matched, score: len(matched), keyChars: keyChars}
		if best == nil || cand.score > best.score ||
			(cand.score == best.score && cand.keyChars > best.keyChars) {
			b := cand
			best = &b
		}
	}
	if best == nil {
		return Plan{}, false
	}

	arg := extractArg(low, best.in)
	sort.Strings(best.matched)
	plan := Plan{
		Intent:          best.in.Name,
		Rationale:       best.in.Rationale,
		MatchedKeywords: best.matched,
		Arg:             arg,
	}
	for _, c := range best.in.Commands {
		plan.Commands = append(plan.Commands, fillArg(c, arg))
	}
	return plan, true
}

// extractArg pulls the trailing token of the query that is not part of any
// matched keyword — the target of intents like "service check nginx".
func extractArg(low string, in Intent) string {
	if !in.NeedsArg {
		return ""
	}
	words := strings.Fields(low)
	kw := map[string]bool{}
	for _, k := range in.Keywords {
		for _, w := range strings.Fields(strings.ToLower(k)) {
			kw[w] = true
		}
	}
	for i := len(words) - 1; i >= 0; i-- {
		if !kw[words[i]] {
			return words[i]
		}
	}
	return ""
}

func fillArg(cmd, arg string) string {
	cmd = strings.ReplaceAll(cmd, "{{arg}}", arg)
	cmd = strings.ReplaceAll(cmd, "{{service}}", arg)
	return strings.TrimSpace(cmd)
}
