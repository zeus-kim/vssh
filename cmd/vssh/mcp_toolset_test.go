package main

import "testing"

func toolNames(ts []Tool) map[string]bool {
	m := map[string]bool{}
	for _, t := range ts {
		m[t.Name] = true
	}
	return m
}

// TestFilteredMCPToolsHidesGatedWrites: by default (no VSSH_ALLOW_CONFIG_WRITE)
// the four mutating config tools are NOT advertised (they can't run anyway), but
// read/core tools are — trimming tools/list token cost with no loss of function.
func TestFilteredMCPToolsHidesGatedWrites(t *testing.T) {
	t.Setenv("VSSH_MCP_TOOLSET", "flat")
	t.Setenv("VSSH_ALLOW_CONFIG_WRITE", "")
	got := toolNames(filteredMCPTools())
	for _, w := range []string{"vssh_config_authorize_key", "vssh_config_revoke_key", "vssh_config_set_node", "vssh_config_pin_node"} {
		if got[w] {
			t.Errorf("gated write tool %q should be hidden when VSSH_ALLOW_CONFIG_WRITE is off", w)
		}
	}
	for _, keep := range []string{"vssh_exec", "vssh_config_list", "vssh_doctor"} {
		if !got[keep] {
			t.Errorf("expected %q to remain advertised", keep)
		}
	}
}

// TestFilteredMCPToolsAllowGatedWrites: with the gate on, the write tools appear.
func TestFilteredMCPToolsAllowGatedWrites(t *testing.T) {
	t.Setenv("VSSH_MCP_TOOLSET", "flat")
	t.Setenv("VSSH_ALLOW_CONFIG_WRITE", "1")
	got := toolNames(filteredMCPTools())
	if !got["vssh_config_authorize_key"] {
		t.Error("expected vssh_config_authorize_key advertised when VSSH_ALLOW_CONFIG_WRITE=1")
	}
}

// TestFilteredMCPToolsCoreSubset: VSSH_MCP_TOOLSET=core advertises only the
// curated essential subset (opt-in token minimization).
func TestFilteredMCPToolsCoreSubset(t *testing.T) {
	t.Setenv("VSSH_MCP_TOOLSET", "core")
	ts := filteredMCPTools()
	got := toolNames(ts)
	if !got["vssh_doctor"] || !got["vssh_exec"] || !got["vssh_facts"] {
		t.Error("core toolset missing an expected essential tool")
	}
	for _, absent := range []string{"vssh_tunnel", "vssh_job_start", "vssh_config_list"} {
		if got[absent] {
			t.Errorf("core toolset should not advertise %q", absent)
		}
	}
	if len(ts) >= len(getMCPTools()) {
		t.Errorf("core toolset (%d) should be smaller than full (%d)", len(ts), len(getMCPTools()))
	}
}
