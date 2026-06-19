// Package fleet stores AI-facing, controller-local memory about each node: its
// role, the services it runs, free-form tags, and a rolling log of notable
// events. It lets an agent manage the fleet with context ("d1 had a disk scare
// last week") instead of treating every node as a blank slate. The store is a
// single JSON file under ~/.vssh and is independent of the signed fleet_state
// snapshot (which is inventory + liveness, not narrative).
package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxNotes bounds the per-node event log; AddNote keeps only the most recent N.
const maxNotes = 20

// Note is a single timestamped observation about a node.
type Note struct {
	Text string `json:"text"`
	TS   string `json:"ts"`
}

// NodeMemory is everything we remember about one node.
type NodeMemory struct {
	Name      string   `json:"name"`
	Role      string   `json:"role,omitempty"`
	Services  []string `json:"services,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Notes     []Note   `json:"notes,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

// FleetMemory is the whole store: a versioned map of node name -> memory.
type FleetMemory struct {
	Version   int                   `json:"version"`
	UpdatedAt string                `json:"updated_at"`
	Nodes     map[string]NodeMemory `json:"nodes"`
}

// configDir mirrors the daemon's ~/.vssh resolution without importing the
// server package (avoids a dependency cycle and keeps this package standalone).
func configDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".vssh")
	}
	return "/etc/vssh"
}

// MemoryPath is the canonical location of the persisted fleet memory.
func MemoryPath() string { return filepath.Join(configDir(), "fleet_memory.json") }

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// Load reads the fleet memory from disk. A missing file is not an error: it
// returns an empty, ready-to-use store so callers can mutate and Save.
func Load() (*FleetMemory, error) {
	fm := &FleetMemory{Version: 1, Nodes: map[string]NodeMemory{}}
	data, err := os.ReadFile(MemoryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return fm, nil
		}
		return fm, err
	}
	if err := json.Unmarshal(data, fm); err != nil {
		return fm, err
	}
	if fm.Nodes == nil {
		fm.Nodes = map[string]NodeMemory{}
	}
	if fm.Version == 0 {
		fm.Version = 1
	}
	return fm, nil
}

// Save persists the store, stamping the top-level updated_at.
func (fm *FleetMemory) Save() error {
	if fm.Version == 0 {
		fm.Version = 1
	}
	if fm.Nodes == nil {
		fm.Nodes = map[string]NodeMemory{}
	}
	fm.UpdatedAt = nowRFC3339()
	b, err := json.MarshalIndent(fm, "", "  ")
	if err != nil {
		return err
	}
	_ = os.MkdirAll(configDir(), 0700)
	return os.WriteFile(MemoryPath(), b, 0600)
}

// GetNode returns the memory for a node and whether it exists.
func (fm *FleetMemory) GetNode(name string) (NodeMemory, bool) {
	m, ok := fm.Nodes[name]
	return m, ok
}

// SetNode replaces a node's role/services/tags. Existing notes are preserved
// unless the caller supplies a non-nil Notes slice. Services and tags are
// normalized (trimmed, de-duped, sorted) for stable, diffable output.
func (fm *FleetMemory) SetNode(name string, mem NodeMemory) {
	mem.Name = name
	mem.Services = normalize(mem.Services)
	mem.Tags = normalize(mem.Tags)
	if existing, ok := fm.Nodes[name]; ok && mem.Notes == nil {
		mem.Notes = existing.Notes
	}
	mem.UpdatedAt = nowRFC3339()
	fm.Nodes[name] = mem
}

// AddNote appends a timestamped note to a node, creating the node if needed,
// and trims the log to the most recent maxNotes entries.
func (fm *FleetMemory) AddNote(node, text string) {
	mem := fm.Nodes[node]
	mem.Name = node
	mem.Notes = append(mem.Notes, Note{Text: text, TS: nowRFC3339()})
	if len(mem.Notes) > maxNotes {
		mem.Notes = mem.Notes[len(mem.Notes)-maxNotes:]
	}
	mem.UpdatedAt = nowRFC3339()
	fm.Nodes[node] = mem
}

// FleetFilter selects nodes for Find. Empty fields are ignored; supplied fields
// are ANDed together. Role/Tag/Service match case-insensitively (role by exact
// value, tag/service by membership); Text is a substring search over the node's
// name, role, services, tags, and note text.
type FleetFilter struct {
	Role    string
	Tag     string
	Service string
	Text    string
}

// fieldVal is one searchable (field-name, value) pair of a node, used by both
// Find's text search and Ask's keyword matching.
type fieldVal struct {
	field string
	value string
}

func (m NodeMemory) fields() []fieldVal {
	out := []fieldVal{{"name", m.Name}, {"role", m.Role}}
	for _, s := range m.Services {
		out = append(out, fieldVal{"service", s})
	}
	for _, t := range m.Tags {
		out = append(out, fieldVal{"tag", t})
	}
	for _, n := range m.Notes {
		out = append(out, fieldVal{"note", n.Text})
	}
	return out
}

// Find returns the nodes matching every supplied filter field, sorted by name.
func (fm *FleetMemory) Find(filter FleetFilter) []NodeMemory {
	role := strings.ToLower(strings.TrimSpace(filter.Role))
	tag := strings.ToLower(strings.TrimSpace(filter.Tag))
	svc := strings.ToLower(strings.TrimSpace(filter.Service))
	text := strings.ToLower(strings.TrimSpace(filter.Text))

	out := []NodeMemory{}
	for _, m := range fm.Nodes {
		if role != "" && strings.ToLower(m.Role) != role {
			continue
		}
		if tag != "" && !containsFold(m.Tags, tag) {
			continue
		}
		if svc != "" && !containsFold(m.Services, svc) {
			continue
		}
		if text != "" && !textMatch(m, text) {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func containsFold(list []string, want string) bool {
	for _, v := range list {
		if strings.ToLower(v) == want {
			return true
		}
	}
	return false
}

func textMatch(m NodeMemory, lowerText string) bool {
	for _, f := range m.fields() {
		if strings.Contains(strings.ToLower(f.value), lowerText) {
			return true
		}
	}
	return false
}

// normalize trims, drops empties, de-dupes, and sorts a string slice. A result
// with no elements is returned as nil so it omits cleanly from JSON.
func normalize(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}
