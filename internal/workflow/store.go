package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func configDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".vssh")
	}
	return "/etc/vssh"
}

// WorkflowsDir holds user-defined workflow JSON files (one workflow per file).
func WorkflowsDir() string { return filepath.Join(configDir(), "workflows") }

// runsDir holds persisted run results, keyed by run id.
func runsDir() string { return filepath.Join(configDir(), "workflow_runs") }

// List returns built-in workflows overlaid by any user workflows in
// ~/.vssh/workflows/*.json (user entries with the same name win), sorted by name.
func List() []Workflow {
	merged := map[string]Workflow{}
	for _, w := range builtinWorkflows() {
		merged[w.Name] = w
	}
	for _, w := range loadUserWorkflows() {
		if w.Name == "" || len(w.Steps) == 0 {
			continue
		}
		merged[w.Name] = w
	}
	out := make([]Workflow, 0, len(merged))
	for _, w := range merged {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns a workflow by name.
func Get(name string) (Workflow, bool) {
	for _, w := range List() {
		if w.Name == name {
			return w, true
		}
	}
	return Workflow{}, false
}

func loadUserWorkflows() []Workflow {
	entries, err := os.ReadDir(WorkflowsDir())
	if err != nil {
		return nil
	}
	var out []Workflow
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(WorkflowsDir(), e.Name()))
		if err != nil {
			continue
		}
		var w Workflow
		if json.Unmarshal(data, &w) == nil && w.Name != "" {
			out = append(out, w)
		}
	}
	return out
}

// SaveRun persists a run result so `workflow status <run_id>` can retrieve it.
func SaveRun(r RunResult) error {
	dir := runsDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, sanitizeRunID(r.RunID)+".json"), b, 0600)
}

// LoadRun retrieves a persisted run result by id.
func LoadRun(runID string) (RunResult, error) {
	var r RunResult
	data, err := os.ReadFile(filepath.Join(runsDir(), sanitizeRunID(runID)+".json"))
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(data, &r)
	return r, err
}

// sanitizeRunID keeps run ids safe as filenames.
func sanitizeRunID(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, id)
}
