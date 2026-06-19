package main

import "testing"

// TestGroupedDefaultSurface: the default tools/list is the grouped surface —
// the per-verb tools are folded into action-tools, cutting the advertised count.
func TestGroupedDefaultSurface(t *testing.T) {
	t.Setenv("VSSH_MCP_TOOLSET", "")
	got := toolNames(filteredMCPTools())
	for _, want := range []string{
		"vssh_exec", "vssh_query", "vssh_job", "vssh_fleet", "vssh_transport",
		"vssh_route", "vssh_config", "vssh_memory", "vssh_workflow",
		"vssh_fleet_state", "vssh_intent", "vssh_diff",
	} {
		if !got[want] {
			t.Errorf("grouped surface missing %q", want)
		}
	}
	for _, folded := range []string{
		"vssh_exec_many", "vssh_facts_many", "vssh_rpc_call", "vssh_memory_get",
		"vssh_workflow_run", "vssh_job_start", "vssh_config_list",
	} {
		if got[folded] {
			t.Errorf("flat tool %q should be folded into a group, not advertised", folded)
		}
	}
	if n := len(filteredMCPTools()); n >= len(getMCPTools()) {
		t.Errorf("grouped surface (%d) should be smaller than flat (%d)", n, len(getMCPTools()))
	}
}

// TestResolveGroupedTool: grouped+action maps to the flat handler name; an empty
// action falls to the group default where defined; unknown action errors; a
// non-grouped name passes through unchanged.
func TestResolveGroupedTool(t *testing.T) {
	cases := []struct {
		name, action, wantFlat string
		wantErr                bool
	}{
		{"vssh_exec", "safe", "vssh_exec_safe", false},
		{"vssh_exec", "many", "vssh_exec_many", false},
		{"vssh_exec", "", "vssh_exec", false}, // default action
		{"vssh_query", "facts_many", "vssh_facts_many", false},
		{"vssh_memory", "find", "vssh_memory_find", false},
		{"vssh_job", "cancel", "vssh_job_cancel", false},
		{"vssh_doctor", "", "vssh_doctor", false}, // non-grouped passthrough
		{"vssh_job", "bogus", "vssh_job", true},   // invalid action
	}
	for _, c := range cases {
		args := map[string]interface{}{}
		if c.action != "" {
			args["action"] = c.action
		}
		flat, gerr := resolveGroupedTool(c.name, args)
		if c.wantErr {
			if gerr == nil {
				t.Errorf("%s/%s: expected error, got none", c.name, c.action)
			}
			continue
		}
		if gerr != nil {
			t.Errorf("%s/%s: unexpected error %v", c.name, c.action, gerr)
		}
		if flat != c.wantFlat {
			t.Errorf("%s/%s -> %q, want %q", c.name, c.action, flat, c.wantFlat)
		}
	}
}

// TestGroupedExecSchemaUnion: a grouped tool requires "action" and carries a
// non-empty union of its members' properties.
func TestGroupedExecSchemaUnion(t *testing.T) {
	var exec *Tool
	for i, tl := range groupedMCPTools() {
		if tl.Name == "vssh_exec" {
			exec = &groupedMCPTools()[i]
			break
		}
	}
	if exec == nil {
		t.Fatal("vssh_exec grouped tool missing")
	}
	if _, ok := exec.InputSchema.Properties["action"]; !ok {
		t.Error("grouped vssh_exec must expose an action property")
	}
	if len(exec.InputSchema.Required) == 0 || exec.InputSchema.Required[0] != "action" {
		t.Error("grouped vssh_exec must require action")
	}
	if len(exec.InputSchema.Properties) < 2 {
		t.Error("grouped vssh_exec should union member properties (target/command/...)")
	}
}
