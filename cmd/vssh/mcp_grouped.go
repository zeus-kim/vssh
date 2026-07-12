package main

import (
	"sort"
	"strconv"
	"strings"
)

// Grouped MCP tools collapse the per-verb tools into a few dispatcher tools that
// take an "action" argument ("enter in big chunks"), cutting the token cost of
// advertising the full flat surface over tools/list. The flat tool names remain
// callable for back-compat (VSSH_MCP_TOOLSET=flat advertises them); grouped is
// the default. Each grouped tool's input schema is the UNION of its members'
// real schemas (built from getMCPTools), so property names always match the
// handlers — dispatch just maps (groupedTool, action) -> flat name.

// groupActions maps a grouped tool name to its action -> flat-tool mapping.
// An empty-string action key, when present, is the default for that group.
func groupActions() map[string]map[string]string {
	return map[string]map[string]string{
		"vssh_exec": {
			"": "vssh_exec", "plain": "vssh_exec", "safe": "vssh_exec_safe",
			"routed": "vssh_exec_routed", "many": "vssh_exec_many",
		},
		"vssh_query": {
			"facts": "vssh_facts", "facts_many": "vssh_facts_many",
			"rpc": "vssh_rpc_call", "rpc_many": "vssh_rpc_many",
		},
		"vssh_job": {
			"start": "vssh_job_start", "status": "vssh_job_status",
			"logs": "vssh_job_logs", "cancel": "vssh_job_cancel",
		},
		"vssh_fleet": {
			"doctor": "vssh_doctor", "status": "vssh_status", "list": "vssh_list",
			"hosts": "vssh_hosts_list", "setup": "vssh_setup",
		},
		"vssh_transport": {
			"artifact_collect": "vssh_artifact_collect", "tunnel": "vssh_tunnel",
		},
		"vssh_route": {
			"select": "vssh_route_select", "policy_check": "vssh_policy_check",
		},
		"vssh_config": {
			"list": "vssh_config_list", "authorize_key": "vssh_config_authorize_key",
			"revoke_key": "vssh_config_revoke_key", "set_node": "vssh_config_set_node",
			"pin_node": "vssh_config_pin_node",
		},
		"vssh_memory": {
			"get": "vssh_memory_get", "set": "vssh_memory_set", "note": "vssh_memory_note",
			"auto_note": "vssh_memory_auto_note", "find": "vssh_memory_find", "ask": "vssh_memory_ask",
			"discover": "vssh_memory_discover",
		},
		"vssh_workflow": {
			"list": "vssh_workflow_list", "run": "vssh_workflow_run", "status": "vssh_workflow_status",
		},
	}
}

// resolveGroupedTool translates a grouped tool call into its flat tool name. For
// a non-grouped name it returns the name unchanged. For a grouped name with an
// unknown action it returns an error result (second value non-nil).
func resolveGroupedTool(name string, args map[string]interface{}) (string, interface{}) {
	m, ok := groupActions()[name]
	if !ok {
		return name, nil
	}
	action := strings.TrimSpace(getString(args, "action"))
	if flat, ok := m[action]; ok {
		return flat, nil
	}
	valid := make([]string, 0, len(m))
	for k := range m {
		if k != "" {
			valid = append(valid, k)
		}
	}
	sort.Strings(valid)
	return name, map[string]interface{}{
		"success": false, "tool": name,
		"error": map[string]interface{}{
			"code":    "invalid_action",
			"message": "unknown action " + strconv.Quote(action) + " for " + name + "; valid: " + strings.Join(valid, ", "),
		},
	}
}

// groupedMCPTools is the default tools/list surface: one tool per group (action
// selector + the union of member schemas) plus the standalone tools.
func groupedMCPTools() []Tool {
	idx := map[string]Tool{}
	for _, t := range getMCPTools() {
		idx[t.Name] = t
	}
	group := func(name, desc, actionDesc string, members ...string) Tool {
		props := map[string]Property{
			"action": {Type: "string", Description: actionDesc},
		}
		for _, mem := range members {
			for k, v := range idx[mem].InputSchema.Properties {
				if k == "action" {
					continue
				}
				if _, exists := props[k]; !exists {
					props[k] = v
				}
			}
		}
		return Tool{Name: name, Description: desc, InputSchema: InputSchema{Type: "object", Properties: props, Required: []string{"action"}}}
	}
	tools := []Tool{
		group("vssh_exec", "Run a command on the fleet (structured stdout/stderr/exit evidence). Set action.", "plain (default; raw exec) | safe (policy-checked) | routed | many (parallel over hosts)", "vssh_exec", "vssh_exec_safe", "vssh_exec_routed", "vssh_exec_many"),
		group("vssh_query", "Typed node facts and native RPCs. Set action.", "facts | facts_many | rpc | rpc_many", "vssh_facts", "vssh_facts_many", "vssh_rpc_call", "vssh_rpc_many"),
		group("vssh_job", "Long-running daemon jobs. Set action.", "start | status | logs | cancel", "vssh_job_start", "vssh_job_status", "vssh_job_logs", "vssh_job_cancel"),
		group("vssh_fleet", "Fleet discovery & health. Set action.", "doctor | status | list | hosts | setup", "vssh_doctor", "vssh_status", "vssh_list", "vssh_hosts_list", "vssh_setup"),
		group("vssh_transport", "Collect file/dir evidence or port-forward. Set action.", "artifact_collect | tunnel", "vssh_artifact_collect", "vssh_tunnel"),
		group("vssh_route", "Path selection and advisory policy check. Set action.", "select | policy_check", "vssh_route_select", "vssh_policy_check"),
		group("vssh_config", "Local config; writes require VSSH_ALLOW_CONFIG_WRITE. Set action.", "list | authorize_key | revoke_key | set_node | pin_node", "vssh_config_list", "vssh_config_authorize_key", "vssh_config_revoke_key", "vssh_config_set_node", "vssh_config_pin_node"),
		group("vssh_memory", "Fleet memory (per-node role/services/tags/notes). Set action. 'discover' auto-detects role/services/tags by probing what each node actually runs (GPUs, running units, listening ports, containers); pass apply=true to write it.", "get | set | note | auto_note | find | ask | discover", "vssh_memory_get", "vssh_memory_set", "vssh_memory_note", "vssh_memory_auto_note", "vssh_memory_find", "vssh_memory_ask", "vssh_memory_discover"),
		group("vssh_workflow", "Predefined multi-step workflows. Set action.", "list | run | status", "vssh_workflow_list", "vssh_workflow_run", "vssh_workflow_status"),
	}
	for _, n := range []string{"vssh_fleet_state", "vssh_intent", "vssh_diff"} {
		if t, ok := idx[n]; ok {
			tools = append(tools, t)
		}
	}
	return tools
}
