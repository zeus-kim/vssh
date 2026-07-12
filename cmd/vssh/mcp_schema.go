package main

func getMCPTools() []Tool {
	tools := []Tool{
		{Name: "vssh_config_list", Description: "Read this host's local vssh config (authorized_keys, config name->ip, node_keys pins) and whether config writes are enabled.", InputSchema: InputSchema{Type: "object", Properties: map[string]Property{}}},
		{Name: "vssh_config_authorize_key", Description: "Authorize an operator public key in local authorized_keys (gated by VSSH_ALLOW_CONFIG_WRITE). args: pubkey, caps, comment.", InputSchema: InputSchema{Type: "object", Properties: map[string]Property{"pubkey": {Type: "string", Description: "base64 Ed25519 public key"}, "caps": {Type: "string", Description: "comma list e.g. exec,rpc,file (optional)"}, "comment": {Type: "string", Description: "label (optional)"}}, Required: []string{"pubkey"}}},
		{Name: "vssh_config_revoke_key", Description: "Remove an operator key from local authorized_keys (gated). args: pubkey.", InputSchema: InputSchema{Type: "object", Properties: map[string]Property{"pubkey": {Type: "string", Description: "base64 public key to revoke"}}, Required: []string{"pubkey"}}},
		{Name: "vssh_config_set_node", Description: "Set/replace a node name->ip mapping in local config (gated). args: name, ip.", InputSchema: InputSchema{Type: "object", Properties: map[string]Property{"name": {Type: "string", Description: "node name"}, "ip": {Type: "string", Description: "node IP"}}, Required: []string{"name", "ip"}}},
		{Name: "vssh_config_pin_node", Description: "Set/replace a node host-identity pin (name->pubkey) in local node_keys (gated). args: name, pubkey.", InputSchema: InputSchema{Type: "object", Properties: map[string]Property{"name": {Type: "string", Description: "node name"}, "pubkey": {Type: "string", Description: "node daemon base64 public key"}}, Required: []string{"name", "pubkey"}}},
		{
			Name:        "vssh_fleet_state",
			Description: "Read the consolidated, controller-signed fleet state (inventory + node keys + caps/tags + liveness) with signature verification and age. action=build refreshes+signs it on the controller.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"action": {Type: "string", Description: "omit to read; 'build' to refresh+sign on the controller"},
					"live":   {Type: "boolean", Description: "with action=build, probe node reachability live instead of using cached liveness", Default: false},
				},
			},
		},
		{
			Name:        "vssh_memory_get",
			Description: "Read controller-local fleet memory: a node's role, services, tags, and recent event notes. Omit 'node' to return the whole store. Use this to manage nodes with historical context.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"node": {Type: "string", Description: "node name; omit to return memory for all nodes"},
				},
			},
		},
		{
			Name:        "vssh_memory_set",
			Description: "Set a node's role/services/tags in fleet memory. Provided fields replace; omitted fields and existing notes are preserved.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"node":     {Type: "string", Description: "node name"},
					"role":     {Type: "string", Description: "node role, e.g. storage, gpu, network (optional)"},
					"services": {Type: "array", Description: "services running on the node, e.g. [nfs, postgres] (optional)"},
					"tags":     {Type: "array", Description: "free-form tags, e.g. [gpu, linux] (optional)"},
				},
				Required: []string{"node"},
			},
		},
		{
			Name:        "vssh_memory_note",
			Description: "Append a timestamped event note to a node's fleet memory (rolling log, most recent 20 kept), e.g. 'disk hit 94% last week'.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"node": {Type: "string", Description: "node name"},
					"text": {Type: "string", Description: "the note to record"},
				},
				Required: []string{"node", "text"},
			},
		},
		{
			Name:        "vssh_memory_find",
			Description: "Filter/search fleet memory by role, tag, service, and/or free text (substring over name/role/services/tags/notes). All supplied filters are ANDed.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"role":    {Type: "string", Description: "match nodes with this exact role (optional)"},
					"tag":     {Type: "string", Description: "match nodes carrying this tag (optional)"},
					"service": {Type: "string", Description: "match nodes running this service (optional)"},
					"text":    {Type: "string", Description: "substring search over name/role/services/tags/notes (optional)"},
				},
			},
		},
		{
			Name:        "vssh_memory_auto_note",
			Description: "Extract noteworthy operational signals from raw command output (df disk pressure, failed systemd units, high load average, nvidia driver errors) and record them as notes. Pure local pattern matching, no LLM.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"node":    {Type: "string", Description: "node name"},
					"output":  {Type: "string", Description: "raw command stdout to analyze"},
					"command": {Type: "string", Description: "the command that produced the output, used as a hint (optional)"},
				},
				Required: []string{"node", "output"},
			},
		},
		{
			Name:        "vssh_memory_ask",
			Description: "Answer a natural-language question about the fleet ('GPU nodes', 'nodes with disk problems', 'who runs postgres') via keyword+structure matching over fleet memory. Returns matching nodes with reasons. No LLM/network.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"query": {Type: "string", Description: "natural-language question"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "vssh_diff",
			Description: "Summarize what was done: parse the daemon audit log into operator sessions, each with the commands run, inferred before/after detail (e.g. 'nginx.conf changed (listen 80 → 443)'), and a one-line natural summary. node omitted = local daemon log.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"node":   {Type: "string", Description: "fetch a remote node's audit log; omit for the local daemon"},
					"last_n": {Type: "number", Description: "keep only the most recent N sessions", Default: 10},
					"since":  {Type: "string", Description: "only records newer than this Go duration, e.g. '1h', '30m' (optional)"},
				},
			},
		},
		{
			Name:        "vssh_intent",
			Description: "Plan commands from a short natural-language request ('disk check', 'service check nginx', 'gpu status') via rule-based matching — no LLM. Returns the intent, command plan, rationale, and matched keywords. With execute=true + target, also runs the plan.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"query":   {Type: "string", Description: "natural-language request, e.g. 'disk check' or 'service check nginx'"},
					"target":  {Type: "string", Description: "where to run (required when execute=true). Comma-separated hosts and/or fleet-memory selectors: '@gpu' (role/tag/service), '@role:gpu', '@tag:prod', '@service:ollama', '@all'. Runs in parallel; per-node results are returned under 'nodes'."},
					"execute": {Type: "boolean", Description: "run the planned commands on target instead of only planning", Default: false},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "vssh_workflow_list",
			Description: "List available multi-step workflows (built-in + ~/.vssh/workflows/*.json) with their params and step counts.",
			InputSchema: InputSchema{Type: "object", Properties: map[string]Property{}},
		},
		{
			Name:        "vssh_workflow_run",
			Description: "Run a predefined multi-step workflow with conditional branching and failure handling (e.g. service-restart, health-check, disk-cleanup, log-collect). Returns the full run record with per-step results. Use dry_run=true to preview the plan without executing.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"name":    {Type: "string", Description: "workflow name (see vssh_workflow_list)"},
					"target":  {Type: "string", Description: "node to run on (required unless dry_run=true)"},
					"params":  {Type: "object", Description: "workflow parameters, e.g. {\"service\": \"nginx\"}"},
					"dry_run": {Type: "boolean", Description: "plan only, do not execute", Default: false},
				},
				Required: []string{"name"},
			},
		},
		{
			Name:        "vssh_workflow_status",
			Description: "Fetch a previously executed workflow run by its run_id.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"run_id": {Type: "string", Description: "run id returned by vssh_workflow_run"},
				},
				Required: []string{"run_id"},
			},
		},
		{
			Name:        "vssh_tunnel",
			Description: "Manage long-lived port forwards to a fleet node as detached background processes: local (-L), reverse (-R), or dynamic SOCKS (-D). action=start|list|stop.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"action": {Type: "string", Description: "start | list | stop (default list)"},
					"target": {Type: "string", Description: "Target peer name or IP (start)"},
					"type":   {Type: "string", Description: "local (-L), reverse (-R), or socks (-D) (start)"},
					"spec":   {Type: "string", Description: "local/reverse: '[bind:]listenPort:host:port'; socks: '[bind:]port' (start)"},
					"id":     {Type: "string", Description: "Tunnel id from list (stop)"},
				},
			},
		},
		{
			Name:        "vssh_doctor",
			Description: "Diagnose local vssh setup: effective binary, stale binary conflicts, auth model, wire config, and peer inventory. Use this first when vssh tools appear installed but execution or facts fail.",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]Property{},
			},
		},
		{
			Name:        "vssh_setup",
			Description: "First-run / idempotent setup. Auto-detects peers, builds the trusted node-key registry (enables host-identity verification so a misrouted command can't run on the wrong host), runs the doctor, and reports any remaining manual step. Call this ONCE right after connecting (safe to re-run anytime to refresh).",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]Property{},
			},
		},
		{
			Name:        "vssh_status",
			Description: "Show connection status for all peers",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]Property{},
			},
		},
		{
			Name:        "vssh_list",
			Description: "List all peers",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: hostListProperties(),
			},
		},
		{
			Name:        "vssh_hosts_list",
			Description: "List configured/discovered hosts with metadata, capabilities, tags, and health for AI agent routing",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: hostListProperties(),
			},
		},
		{
			Name:        "vssh_exec",
			Description: "Execute a shell command on a remote server and return structured stdout/stderr/exit code evidence",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"target":          {Type: "string", Description: "Target peer name or IP"},
					"command":         {Type: "string", Description: "Shell command to execute as one script; quotes and pipes are preserved"},
					"timeout_seconds": {Type: "number", Description: "Execution timeout in seconds", Default: 30},
					"allow_dangerous": {Type: "boolean", Description: "Allow commands blocked by the safety policy; use only after explicit human approval", Default: false},
				},
				Required: []string{"target", "command"},
			},
		},
		{
			Name:        "vssh_exec",
			Description: "Execute a shell command with policy checks and return an evidence envelope",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"target":          {Type: "string", Description: "Target peer name or IP"},
					"command":         {Type: "string", Description: "Shell command to execute as one script; quotes and pipes are preserved"},
					"timeout_seconds": {Type: "number", Description: "Execution timeout in seconds", Default: 30},
					"allow_dangerous": {Type: "boolean", Description: "Allow commands blocked by the safety policy; use only after explicit human approval", Default: false},
				},
				Required: []string{"target", "command"},
			},
		},
		{
			Name:        "vssh_exec_safe",
			Description: "Execute only commands allowed by the built-in read/diagnostic safety policy",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"target":          {Type: "string", Description: "Target peer name or IP"},
					"command":         {Type: "string", Description: "Shell command to execute as one script; dangerous write/destructive operations are blocked"},
					"timeout_seconds": {Type: "number", Description: "Execution timeout in seconds", Default: 30},
				},
				Required: []string{"target", "command"},
			},
		},
		{
			Name:        "vssh_policy_check",
			Description: "Classify a shell command before execution and report whether approval is required",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"command": {Type: "string", Description: "Shell command to classify"},
				},
				Required: []string{"command"},
			},
		},
		{
			Name:        "vssh_route_select",
			Description: "Select the best host for required capabilities, preferred tags, and health constraints",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"required_capabilities":  {Type: "array", Description: "Capabilities that must be present, e.g. cuda, ollama, browser"},
					"preferred_tags":         {Type: "array", Description: "Tags that improve route score but are not required"},
					"avoid_health":           {Type: "array", Description: "Health statuses to avoid", Default: []string{"offline", "degraded"}},
					"target":                 {Type: "string", Description: "Optional explicit target host to validate against routing constraints"},
					"include_health":         {Type: "boolean", Description: "Fetch live monitor health from configured monitor_url or monitor_port before routing", Default: false},
					"health_timeout_seconds": {Type: "number", Description: "Per-host live health HTTP timeout in seconds", Default: 1},
				},
			},
		},
		{
			Name:        "vssh_route_select",
			Description: "Compatibility alias for vssh_route_select",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"required_capabilities":  {Type: "array", Description: "Capabilities that must be present, e.g. cuda, ollama, browser"},
					"preferred_tags":         {Type: "array", Description: "Tags that improve route score but are not required"},
					"avoid_health":           {Type: "array", Description: "Health statuses to avoid", Default: []string{"offline", "degraded"}},
					"target":                 {Type: "string", Description: "Optional explicit target host to validate against routing constraints"},
					"include_health":         {Type: "boolean", Description: "Fetch live monitor health from configured monitor_url or monitor_port before routing", Default: false},
					"health_timeout_seconds": {Type: "number", Description: "Per-host live health HTTP timeout in seconds", Default: 1},
				},
			},
		},
		{
			Name:        "vssh_exec_routed",
			Description: "Route by capability/tag/health, then execute a command with policy and evidence",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"required_capabilities":  {Type: "array", Description: "Capabilities that must be present"},
					"preferred_tags":         {Type: "array", Description: "Tags that improve route score"},
					"avoid_health":           {Type: "array", Description: "Health statuses to avoid", Default: []string{"offline", "degraded"}},
					"target":                 {Type: "string", Description: "Optional explicit target host"},
					"include_health":         {Type: "boolean", Description: "Fetch live monitor health from configured monitor_url or monitor_port before routing", Default: false},
					"health_timeout_seconds": {Type: "number", Description: "Per-host live health HTTP timeout in seconds", Default: 1},
					"command":                {Type: "string", Description: "Shell command to execute on the selected host"},
					"timeout_seconds":        {Type: "number", Description: "Execution timeout in seconds", Default: 30},
					"allow_dangerous":        {Type: "boolean", Description: "Allow commands blocked by policy; use only after explicit human approval", Default: false},
				},
				Required: []string{"command"},
			},
		},
		{
			Name:        "vssh_rpc_call",
			Description: "Call a typed native daemon RPC method on one target and return JSON data",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"target":          {Type: "string", Description: "Target peer name or IP"},
					"method":          {Type: "string", Description: "RPC method such as get_disk, get_memory, get_gpu, service_status"},
					"params":          {Type: "object", Description: "RPC parameters"},
					"timeout_seconds": {Type: "number", Description: "Execution timeout in seconds", Default: 30},
				},
				Required: []string{"target", "method"},
			},
		},
		{
			Name:        "vssh_exec_many",
			Description: "Execute one command across many native daemon targets in parallel",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"targets":         {Type: "array", Description: "Target peer names or IPs"},
					"command":         {Type: "string", Description: "Shell command to execute on every target"},
					"timeout_seconds": {Type: "number", Description: "Per-target timeout in seconds", Default: 30},
					"allow_dangerous": {Type: "boolean", Description: "Allow commands blocked by policy; use only after explicit human approval", Default: false},
					"max_parallelism": {Type: "number", Description: "Maximum concurrent target executions", Default: 16},
					"allow_partial":   {Type: "boolean", Description: "Return partial results when some targets fail", Default: true},
				},
				Required: []string{"targets", "command"},
			},
		},
		{
			Name:        "vssh_rpc_many",
			Description: "Call one typed native daemon RPC method across many targets in parallel",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"targets":         {Type: "array", Description: "Target peer names or IPs"},
					"method":          {Type: "string", Description: "RPC method such as get_disk, get_memory, get_gpu, service_status"},
					"params":          {Type: "object", Description: "RPC parameters"},
					"timeout_seconds": {Type: "number", Description: "Per-target timeout in seconds", Default: 30},
					"max_parallelism": {Type: "number", Description: "Maximum concurrent target RPC calls", Default: 16},
				},
				Required: []string{"targets", "method"},
			},
		},
		{
			Name:        "vssh_facts",
			Description: "Return typed daemon facts for one target using INFO",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"target":          {Type: "string", Description: "Target peer name or IP"},
					"timeout_seconds": {Type: "number", Description: "Execution timeout in seconds", Default: 30},
				},
				Required: []string{"target"},
			},
		},
		{
			Name:        "vssh_facts_many",
			Description: "Return typed daemon facts for many targets in parallel using INFO",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"targets":         {Type: "array", Description: "Target peer names or IPs"},
					"timeout_seconds": {Type: "number", Description: "Per-target timeout in seconds", Default: 30},
					"max_parallelism": {Type: "number", Description: "Maximum concurrent target fact probes", Default: 16},
				},
				Required: []string{"targets"},
			},
		},
		{
			Name:        "vssh_job_start",
			Description: "Start a long-running command as a daemon-side job and return its job id",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"target":          {Type: "string", Description: "Target peer name or IP"},
					"command":         {Type: "string", Description: "Command to run asynchronously"},
					"allow_dangerous": {Type: "boolean", Description: "Allow commands blocked by safety policy after approval", Default: false},
				},
				Required: []string{"target", "command"},
			},
		},
		{
			Name:        "vssh_job_status",
			Description: "Return daemon-side job status",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"target": {Type: "string", Description: "Target peer name or IP"},
					"id":     {Type: "string", Description: "Job id"},
				},
				Required: []string{"target", "id"},
			},
		},
		{
			Name:        "vssh_job_logs",
			Description: "Return daemon-side job stdout/stderr logs",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"target":     {Type: "string", Description: "Target peer name or IP"},
					"id":         {Type: "string", Description: "Job id"},
					"tail_bytes": {Type: "number", Description: "Return only the last N bytes of stdout/stderr", Default: 0},
				},
				Required: []string{"target", "id"},
			},
		},
		{
			Name:        "vssh_job_cancel",
			Description: "Cancel a running daemon-side job",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"target": {Type: "string", Description: "Target peer name or IP"},
					"id":     {Type: "string", Description: "Job id"},
				},
				Required: []string{"target", "id"},
			},
		},
		{
			Name:        "vssh_artifact_collect",
			Description: "Collect remote artifact metadata; files return bounded base64 content, directories return shallow entries",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"target":    {Type: "string", Description: "Target peer name or IP"},
					"path":      {Type: "string", Description: "Remote file or directory path"},
					"max_bytes": {Type: "number", Description: "Maximum file bytes to return before base64 encoding", Default: 1048576},
				},
				Required: []string{"target", "path"},
			},
		},
		{
			Name:        "vssh_exec_routed",
			Description: "Compatibility alias for vssh_exec_routed",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"required_capabilities":  {Type: "array", Description: "Capabilities that must be present"},
					"preferred_tags":         {Type: "array", Description: "Tags that improve route score"},
					"avoid_health":           {Type: "array", Description: "Health statuses to avoid", Default: []string{"offline", "degraded"}},
					"target":                 {Type: "string", Description: "Optional explicit target host"},
					"include_health":         {Type: "boolean", Description: "Fetch live monitor health from configured monitor_url or monitor_port before routing", Default: false},
					"health_timeout_seconds": {Type: "number", Description: "Per-host live health HTTP timeout in seconds", Default: 1},
					"command":                {Type: "string", Description: "Shell command to execute on the selected host"},
					"timeout_seconds":        {Type: "number", Description: "Execution timeout in seconds", Default: 30},
					"allow_dangerous":        {Type: "boolean", Description: "Allow commands blocked by policy; use only after explicit human approval", Default: false},
				},
				Required: []string{"command"},
			},
		},
	}
	return dedupeMCPTools(tools)
}

func dedupeMCPTools(tools []Tool) []Tool {
	seen := map[string]bool{}
	out := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		if seen[tool.Name] {
			continue
		}
		seen[tool.Name] = true
		out = append(out, tool)
	}
	return out
}

func hostListProperties() map[string]Property {
	return map[string]Property{
		"include_health":         {Type: "boolean", Description: "Fetch live monitor health from configured monitor_url or monitor_port", Default: false},
		"health_timeout_seconds": {Type: "number", Description: "Per-host live health HTTP timeout in seconds", Default: 1},
	}
}
