package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// MCP Protocol structures
type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id"`
}

type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// filteredMCPTools is the tool list advertised over tools/list. It trims the
// set to cut token cost: the gated config-WRITE tools are hidden unless
// VSSH_ALLOW_CONFIG_WRITE is set (they cannot run otherwise, so advertising them
// is pure waste), and VSSH_MCP_TOOLSET=core advertises only a curated essential
// subset (opt-in). Default (unset/full) = everything minus disabled writes.
// filteredMCPTools is the tool surface advertised over tools/list. The default
// is the GROUPED surface (~12 action-tools) to minimise per-session token cost;
// VSSH_MCP_TOOLSET=flat advertises the legacy per-verb tools, =core a tiny
// curated subset. Flat tool names stay callable regardless (resolveGroupedTool).
func filteredMCPTools() []Tool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("VSSH_MCP_TOOLSET"))) {
	case "core":
		all := getMCPTools()
		core := map[string]bool{
			"vssh_doctor": true, "vssh_list": true, "vssh_fleet_state": true,
			"vssh_exec": true, "vssh_exec_safe": true, "vssh_facts": true,
			"vssh_rpc_call": true,
		}
		out := make([]Tool, 0, len(core))
		for _, t := range all {
			if core[t.Name] {
				out = append(out, t)
			}
		}
		return out
	case "flat", "full":
		return flatMCPTools()
	default:
		return groupedMCPTools()
	}
}

// flatMCPTools is the legacy per-verb surface (VSSH_MCP_TOOLSET=flat). The gated
// config-write tools are hidden unless VSSH_ALLOW_CONFIG_WRITE is set.
func flatMCPTools() []Tool {
	all := getMCPTools()
	if configWriteAllowed() {
		return all
	}
	hidden := map[string]bool{
		"vssh_config_authorize_key": true, "vssh_config_revoke_key": true,
		"vssh_config_set_node": true, "vssh_config_pin_node": true,
	}
	out := make([]Tool, 0, len(all))
	for _, t := range all {
		if !hidden[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

type Property struct {
	Type        string      `json:"type"`
	Description string      `json:"description"`
	Default     interface{} `json:"default,omitempty"`
}

type PolicyDecision struct {
	Allowed          bool     `json:"allowed"`
	Risk             string   `json:"risk"`
	Reason           string   `json:"reason"`
	MatchedPatterns  []string `json:"matched_patterns,omitempty"`
	RequiresApproval bool     `json:"requires_approval"`
}

type RouteRequest struct {
	RequiredCapabilities []string `json:"required_capabilities"`
	PreferredTags        []string `json:"preferred_tags"`
	AvoidHealth          []string `json:"avoid_health"`
	Target               string   `json:"target,omitempty"`
	IncludeHealth        bool     `json:"include_health"`
	HealthTimeoutSeconds float64  `json:"health_timeout_seconds,omitempty"`
}

type RouteCandidate struct {
	Name         string                 `json:"name"`
	Score        int                    `json:"score"`
	Selected     bool                   `json:"selected"`
	Reasons      []string               `json:"reasons"`
	Missing      []string               `json:"missing_capabilities,omitempty"`
	Health       string                 `json:"health"`
	Tags         []string               `json:"tags"`
	Capabilities []string               `json:"capabilities"`
	Host         map[string]interface{} `json:"host"`
}

type RouteDecision struct {
	Success    bool             `json:"success"`
	Selected   string           `json:"selected,omitempty"`
	Reason     string           `json:"reason"`
	Request    RouteRequest     `json:"request"`
	Candidates []RouteCandidate `json:"candidates"`
	Error      interface{}      `json:"error,omitempty"`
}

func cmdMcp() {
	scanner := bufio.NewScanner(os.Stdin)
	// Allow large tool-call request lines (file/script payloads) — a strict MCP
	// client (Cursor/Codex/custom) may send >64KB JSON; the default scanner caps
	// at bufio.MaxScanTokenSize and would silently drop the request and end the
	// session. Mirror run-batch's raised buffer.
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var req MCPRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}

		// Skip notifications (requests without ID)
		if req.ID == nil {
			continue
		}

		resp := handleMCPRequest(req)
		// Skip empty responses (notifications)
		if resp.ID == nil && resp.Result == nil && resp.Error == nil {
			continue
		}
		output, _ := json.Marshal(resp)
		fmt.Println(string(output))
	}
}

func handleMCPRequest(req MCPRequest) MCPResponse {
	switch req.Method {
	case "notifications/initialized", "notifications/cancelled":
		// These are notifications, return empty (will be skipped)
		return MCPResponse{}

	case "initialize":
		return MCPResponse{
			JSONRPC: "2.0",
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
				"serverInfo":      map[string]string{"name": "vssh-mcp", "version": version},
			},
			ID: req.ID,
		}

	case "tools/list":
		return MCPResponse{
			JSONRPC: "2.0",
			Result:  map[string]interface{}{"tools": filteredMCPTools()},
			ID:      req.ID,
		}

	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		json.Unmarshal(req.Params, &params)

		result := callMCPTool(params.Name, params.Arguments)
		resultJSON, _ := json.MarshalIndent(result, "", "  ")

		return MCPResponse{
			JSONRPC: "2.0",
			Result: map[string]interface{}{
				"content": []map[string]string{
					{"type": "text", "text": string(resultJSON)},
				},
			},
			ID: req.ID,
		}

	default:
		return MCPResponse{
			JSONRPC: "2.0",
			Error:   &MCPError{Code: -32601, Message: "Unknown method: " + req.Method},
			ID:      req.ID,
		}
	}
}

func callMCPTool(name string, args map[string]interface{}) interface{} {
	// Grouped tools (default surface) carry an "action" arg; resolve to the flat
	// tool name before auto-setup/dispatch. Flat names pass through unchanged.
	if flat, gerr := resolveGroupedTool(name, args); gerr != nil {
		return gerr
	} else {
		name = flat
	}
	// Zero-touch onboarding (P2): the first time an operational tool runs on a
	// fresh controller, provision host-identity verification automatically.
	if isOperationalMCPTool(name) {
		if as := autoSetupOnce(); as != nil {
			result := dispatchMCPTool(name, args)
			if m, ok := result.(map[string]interface{}); ok {
				m["_autosetup"] = as
				return m
			}
			return result
		}
	}
	return dispatchMCPTool(name, args)
}

// isOperationalMCPTool reports whether a tool actually contacts the fleet (and
// thus benefits from a provisioned host-identity registry). Read-only/meta tools
// (doctor, setup, status, list, policy_check, route_select) are excluded.
func isOperationalMCPTool(name string) bool {
	switch name {
	case "vssh_exec", "vssh_exec_safe", "vssh_exec_routed", "vssh_exec_many",
		"vssh_rpc_call", "vssh_rpc_many", "vssh_facts", "vssh_facts_many",
		"vssh_job_start", "vssh_job_status", "vssh_job_logs", "vssh_job_cancel",
		"vssh_artifact_collect", "vssh_tunnel":
		return true
	}
	return false
}

func dispatchMCPTool(name string, args map[string]interface{}) interface{} {
	switch name {
	case "vssh_doctor":
		return doctorJSON(runDoctor())
	case "vssh_setup":
		return toolSetup()
	case "vssh_status":
		return toolStatus()
	case "vssh_list", "vssh_hosts_list":
		return toolList(args)
	case "vssh_exec":
		return toolExec(getString(args, "target"), getString(args, "command"), getFloat(args, "timeout_seconds", 30), getBool(args, "allow_dangerous", false))
	case "vssh_exec_safe":
		return toolExec(getString(args, "target"), getString(args, "command"), getFloat(args, "timeout_seconds", 30), false)
	case "vssh_policy_check":
		command := getString(args, "command")
		if command == "" {
			return map[string]interface{}{
				"success": false,
				"error": map[string]interface{}{
					"code":    "missing_argument",
					"message": "command is required",
				},
			}
		}
		return map[string]interface{}{
			"success": true,
			"command": command,
			"policy":  classifyCommand(command),
		}
	case "vssh_route_select":
		return toolRouteSelect(args)
	case "vssh_exec_routed":
		return toolExecRouted(args)
	case "vssh_rpc_call":
		return toolRPCCall(args)
	case "vssh_exec_many":
		return toolExecMany(args)
	case "vssh_rpc_many":
		return toolRPCMany(args)
	case "vssh_facts":
		return toolFacts(args)
	case "vssh_facts_many":
		return toolFactsMany(args)
	case "vssh_job_start":
		return toolJobStart(args)
	case "vssh_job_status":
		return toolJobRPC(args, "vssh_job_status", "job_status")
	case "vssh_job_logs":
		return toolJobRPC(args, "vssh_job_logs", "job_logs")
	case "vssh_job_cancel":
		return toolJobRPC(args, "vssh_job_cancel", "job_cancel")
	case "vssh_artifact_collect":
		return toolArtifactCollect(args)
	case "vssh_tunnel":
		return toolTunnel(args)
	case "vssh_fleet_state":
		return toolFleetState(args)
	case "vssh_memory_get":
		return toolMemoryGet(args)
	case "vssh_memory_set":
		return toolMemorySet(args)
	case "vssh_memory_note":
		return toolMemoryNote(args)
	case "vssh_memory_find":
		return toolMemoryFind(args)
	case "vssh_memory_auto_note":
		return toolMemoryAutoNote(args)
	case "vssh_memory_ask":
		return toolMemoryAsk(args)
	case "vssh_diff":
		return toolDiff(args)
	case "vssh_intent":
		return toolIntent(args)
	case "vssh_workflow_list":
		return toolWorkflowList(args)
	case "vssh_workflow_run":
		return toolWorkflowRun(args)
	case "vssh_workflow_status":
		return toolWorkflowStatus(args)
	case "vssh_config_list":
		return toolConfigList(args)
	case "vssh_config_authorize_key":
		return toolConfigAuthorizeKey(args)
	case "vssh_config_revoke_key":
		return toolConfigRevokeKey(args)
	case "vssh_config_set_node":
		return toolConfigSetNode(args)
	case "vssh_config_pin_node":
		return toolConfigPinNode(args)
	default:
		return map[string]string{"error": "Unknown tool: " + name}
	}
}

// Helper functions
func getString(args map[string]interface{}, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getFloat(args map[string]interface{}, key string, fallback float64) float64 {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		}
	}
	return fallback
}

func getBool(args map[string]interface{}, key string, fallback bool) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return fallback
}

func getStringList(args map[string]interface{}, key string, fallback []string) []string {
	value, ok := args[key]
	if !ok {
		return fallback
	}
	switch v := value.(type) {
	case []string:
		return normalizeList(v)
	case []interface{}:
		values := []string{}
		for _, item := range v {
			if s, ok := item.(string); ok {
				values = append(values, s)
			}
		}
		return normalizeList(values)
	case string:
		if strings.TrimSpace(v) == "" {
			return fallback
		}
		return normalizeList(strings.Split(v, ","))
	default:
		return fallback
	}
}
