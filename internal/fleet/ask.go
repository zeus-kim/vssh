package fleet

import (
	"fmt"
	"sort"
	"strings"
)

// Ask: answer a natural-language question about the fleet with pure keyword +
// structure matching — no LLM, no network. The query is tokenized, stripped of
// structural stopwords, expanded with a small bilingual synonym table, and each
// token is matched (case-insensitive substring) against every node's
// role/tags/services/notes. Matched nodes are ranked by how many query tokens
// they satisfy, each with human-readable reasons.

// AskHit is one matching node and why it matched.
type AskHit struct {
	Node    string     `json:"node"`
	Memory  NodeMemory `json:"memory"`
	Reasons []string   `json:"reasons"`
	Score   int        `json:"score"`
}

// askStopwords are structural words that carry no selection meaning. Korean and
// English are mixed since queries arrive in either.
var askStopwords = map[string]bool{
	"노드": true, "서버": true, "목록": true, "있는": true, "있다": true,
	"있나": true, "이력": true, "하는": true, "돌리는": true, "돌리": true,
	"보여줘": true, "알려줘": true, "어떤": true, "무슨": true, "가진": true,
	"좀": true, "뭐": true, "the": true, "a": true, "an": true,
	"node": true, "nodes": true, "server": true, "servers": true,
	"list": true, "show": true, "which": true, "with": true, "that": true,
	"have": true, "has": true, "running": true, "run": true, "of": true,
}

// askSynonyms expands a token to related terms so a Korean query can match
// English-stored data and vice-versa.
var askSynonyms = map[string][]string{
	"디스크":        {"disk", "용량"},
	"disk":       {"디스크"},
	"gpu":        {"그래픽", "cuda"},
	"그래픽":        {"gpu"},
	"장애":         {"fail", "down", "error", "오류", "에러", "issue"},
	"fault":      {"장애", "fail"},
	"failure":    {"장애", "fail"},
	"postgres":   {"postgresql", "pg"},
	"postgresql": {"postgres", "pg"},
	"스토리지":       {"storage"},
	"저장":         {"storage"},
	"storage":    {"스토리지"},
	"relay":      {"릴레이"},
	"릴레이":        {"relay"},
}

// Ask returns nodes matching the query, ranked by match count then name.
func (fm *FleetMemory) Ask(query string) []AskHit {
	tokens := askTokens(query)
	hits := []AskHit{}
	for _, m := range fm.Nodes {
		reasons := []string{}
		matched := 0
		for _, tok := range tokens {
			if reason, ok := matchToken(m, expandToken(tok)); ok {
				matched++
				reasons = append(reasons, fmt.Sprintf("%q → %s", tok, reason))
			}
		}
		if matched > 0 {
			hits = append(hits, AskHit{Node: m.Name, Memory: m, Reasons: reasons, Score: matched})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Node < hits[j].Node
	})
	return hits
}

func askTokens(query string) []string {
	raw := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', ',', '?', '!', '.', '"', '\'':
			return true
		}
		return false
	})
	var toks []string
	seen := map[string]bool{}
	for _, t := range raw {
		if t == "" || askStopwords[t] || seen[t] {
			continue
		}
		seen[t] = true
		toks = append(toks, t)
	}
	return toks
}

func expandToken(tok string) []string {
	return append([]string{tok}, askSynonyms[tok]...)
}

func matchToken(m NodeMemory, variants []string) (string, bool) {
	for _, f := range m.fields() {
		lv := strings.ToLower(f.value)
		for _, v := range variants {
			if v != "" && strings.Contains(lv, strings.ToLower(v)) {
				return fmt.Sprintf("%s %q", f.field, f.value), true
			}
		}
	}
	return "", false
}
