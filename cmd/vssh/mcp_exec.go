package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zeus-kim/vssh/internal/server"
	"github.com/zeus-kim/vssh/internal/ssh"
)

// rceRegexps catch arbitrary-code-execution shapes that the destructive
// substring list misses — a fetched payload handed to a shell/interpreter.
// These are the highest risk: the actual command run is whatever the remote
// returns, so the policy can't reason about it at all.
var rceRegexps = []*regexp.Regexp{
	// download piped straight into an interpreter: `curl https://x | bash`
	// (also sudo with flags: `… | sudo -E bash`)
	regexp.MustCompile(`\|\s*(sudo\s+(-{1,2}\S+\s+)*)?(bash|sh|zsh|dash|ksh|fish|python[0-9.]*|perl|ruby|node|nodejs|php)\b`),
	// download handed to an interpreter via xargs: `curl … | xargs bash`
	regexp.MustCompile(`\|\s*xargs\b[^\n]*\b(bash|sh|zsh|dash|ksh|fish|python[0-9.]*|perl|ruby|node|nodejs|php)\b`),
	// interpreter eating a fetched script via process/command substitution:
	// `bash <(curl …)`, `sh -c "$(wget …)"`
	regexp.MustCompile(`(bash|sh|zsh|dash|ksh|python[0-9.]*|perl|ruby|node|php|eval)\b[^\n]*<\(\s*(curl|wget|fetch)\b`),
	regexp.MustCompile(`(bash|sh|zsh|dash|ksh|python[0-9.]*|perl|ruby|node|php|eval)\b[^\n]*\$\(\s*(curl|wget|fetch)\b`),
	// sourcing a fetched script: `source <(curl …)`, `. <(curl …)`
	regexp.MustCompile(`(^|[;&| ])(source|\.)\s+<\(\s*(curl|wget|fetch)\b`),
	// eval of any command substitution is arbitrary code execution
	regexp.MustCompile(`\beval\b[^\n]*\$\(`),
}

// sensitivePathPatterns mark credential/identity files whose mere read is an
// exfiltration path. Matched as substrings of the normalized command.
var sensitivePathPatterns = []string{
	"/etc/shadow", "/etc/gshadow", "/etc/passwd", "/etc/sudoers",
	"/etc/krb5.keytab",
	".ssh/id_", "id_rsa", "id_ed25519", "id_ecdsa", "id_dsa",
	".aws/credentials", ".gnupg", ".docker/config.json", ".kube/config",
	".netrc", ".pgpass",
}

func classifyCommand(command string) PolicyDecision {
	normalized := strings.ToLower(strings.Join(strings.Fields(command), " "))
	if normalized == "" {
		return PolicyDecision{
			Allowed:          false,
			Risk:             "invalid",
			Reason:           "empty command",
			RequiresApproval: false,
		}
	}

	// 1. Arbitrary remote-code execution (curl|bash and friends) — block first;
	// the effective payload is unknowable, so this outranks every other check.
	for _, re := range rceRegexps {
		if m := re.FindString(normalized); m != "" {
			return PolicyDecision{
				Allowed:          false,
				Risk:             "dangerous",
				Reason:           "command pipes or substitutes a download into a shell/interpreter (arbitrary code execution)",
				MatchedPatterns:  []string{strings.TrimSpace(m)},
				RequiresApproval: true,
			}
		}
	}

	// 2. Sensitive credential/identity files (reading them is exfiltration).
	sensMatches := []string{}
	for _, p := range sensitivePathPatterns {
		if strings.Contains(normalized, p) {
			sensMatches = append(sensMatches, p)
		}
	}
	if len(sensMatches) > 0 {
		return PolicyDecision{
			Allowed:          false,
			Risk:             "sensitive",
			Reason:           "command accesses sensitive credential or identity files",
			MatchedPatterns:  sensMatches,
			RequiresApproval: true,
		}
	}

	dangerous := []string{
		"rm -rf",
		"rm -fr",
		"shutdown",
		"reboot",
		"poweroff",
		"halt",
		"mkfs",
		"dd if=",
		"dd of=",
		"docker rm",
		"docker rmi",
		"docker system prune",
		"kubectl delete",
		"helm uninstall",
		"terraform destroy",
		"systemctl stop",
		"systemctl restart",
		"systemctl disable",
		"iptables -f",
		"nft flush",
	}

	matches := []string{}
	for _, pattern := range dangerous {
		if strings.Contains(normalized, pattern) {
			matches = append(matches, pattern)
		}
	}
	if len(matches) > 0 {
		return PolicyDecision{
			Allowed:          false,
			Risk:             "dangerous",
			Reason:           "command matches a destructive or service-impacting pattern",
			MatchedPatterns:  matches,
			RequiresApproval: true,
		}
	}

	return PolicyDecision{
		Allowed:          true,
		Risk:             "low",
		Reason:           "no dangerous pattern matched",
		RequiresApproval: false,
	}
}

func toolExecMany(args map[string]interface{}) map[string]interface{} {
	targets := getStringList(args, "targets", nil)
	command := getString(args, "command")
	if len(targets) == 0 || command == "" {
		return map[string]interface{}{
			"success": false,
			"tool":    "vssh_exec_many",
			"targets": targets,
			"command": command,
			"error": map[string]interface{}{
				"code":    "missing_argument",
				"message": "targets and command are required",
			},
		}
	}

	policy := classifyCommand(command)
	allowDangerous := getBool(args, "allow_dangerous", false)
	if !policy.Allowed && !allowDangerous {
		return map[string]interface{}{
			"success":       false,
			"tool":          "vssh_exec_many",
			"targets":       targets,
			"command":       command,
			"policy":        policy,
			"blocked":       true,
			"evidence_type": "policy_decision",
			"error": map[string]interface{}{
				"code":      "policy_blocked",
				"message":   policy.Reason,
				"retryable": false,
			},
		}
	}
	if allowDangerous && !policy.Allowed {
		policy.Allowed = true
		policy.Reason = "dangerous command allowed by explicit allow_dangerous flag"
	}

	startedAt := time.Now().UTC()
	maxParallelism := int(getFloat(args, "max_parallelism", float64(defaultMaxParallelism())))
	results := runMany(targets, command, time.Duration(getFloat(args, "timeout_seconds", 30)*float64(time.Second)), maxParallelism)
	endedAt := time.Now().UTC()
	ok, failed := countExecMany(results)
	return map[string]interface{}{
		"success":         failed == 0,
		"tool":            "vssh_exec_many",
		"targets":         targets,
		"command":         command,
		"started_at":      startedAt.Format(time.RFC3339Nano),
		"ended_at":        endedAt.Format(time.RFC3339Nano),
		"duration_ms":     endedAt.Sub(startedAt).Milliseconds(),
		"policy":          policy,
		"approved":        allowDangerous,
		"max_parallelism": normalizedMaxParallelism(maxParallelism, len(targets)),
		"evidence_type":   "execution_result_many",
		"summary": map[string]interface{}{
			"total":  len(targets),
			"ok":     ok,
			"failed": failed,
		},
		"results": results,
	}
}

func getParamsMap(args map[string]interface{}, key string) map[string]interface{} {
	value, ok := args[key]
	if !ok || value == nil {
		return map[string]interface{}{}
	}
	if params, ok := value.(map[string]interface{}); ok {
		return params
	}
	return map[string]interface{}{}
}

func countExecMany(results []multiExecResult) (int, int) {
	ok, failed := 0, 0
	for _, result := range results {
		if result.Error != "" || result.Result == nil || !result.Result.Success {
			failed++
		} else {
			ok++
		}
	}
	return ok, failed
}

// Tool implementations
func toolStatus() map[string]interface{} {
	connector, err := ssh.NewConnector("default")
	if err != nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("Failed to create connector: %v", err),
		}
	}

	return map[string]interface{}{
		"status": connector.Status(),
	}
}

func toolList(args map[string]interface{}) map[string]interface{} {
	opts := hostRecordOptions{
		IncludeHealth: getBool(args, "include_health", false),
		HealthTimeout: time.Duration(getFloat(args, "health_timeout_seconds", 1) * float64(time.Second)),
	}
	connector, err := ssh.NewConnector("default")
	if err != nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("Failed to create connector: %v", err),
		}
	}

	// When live health is requested, override the (possibly stale) node_monitor
	// stats with a live daemon-RPC reading so callers get the current moment.
	if opts.IncludeHealth {
		connector.OverlayStats(liveStatsForPeers(connector, 2000*time.Millisecond))
	}

	peers := connector.ListPeers()
	hostList := []map[string]interface{}{}

	for _, p := range peers {
		if p.NodeName == "" {
			continue
		}
		hostList = append(hostList, buildHostRecordWithOptions(p, opts))
	}
	sort.Slice(hostList, func(i, j int) bool {
		return fmt.Sprint(hostList[i]["name"]) < fmt.Sprint(hostList[j]["name"])
	})

	return map[string]interface{}{
		"success": true,
		"count":   len(hostList),
		"hosts":   hostList,
		"peers":   hostList,
		"health": map[string]interface{}{
			"included": opts.IncludeHealth,
			"source":   "node_monitor",
		},
	}
}

// daemonPreapproves asks the target daemon whether the authenticated key's policy
// would permit (or pre-approve) the command — single source of truth (docs §6.5
// step 2). Lets unattended MCP runs proceed on commands the operator pre-approved
// via the key's policy danger_preapproved, instead of demanding allow_dangerous.
func daemonPreapproves(target, command string) (bool, string) {
	host := resolveReachableHost(target, defaultPort)
	resp, err := server.CallRPC(host, defaultPort, getSecret(), "policy_check", map[string]interface{}{"command": command}, 8*time.Second)
	if err != nil || !resp.Success {
		return false, ""
	}
	data, _ := resp.Data.(map[string]interface{})
	decision, _ := data["decision"].(string)
	return decision == "preapproved" || decision == "allow", decision
}

func toolExec(target, command string, timeoutSeconds float64, allowDangerous bool) map[string]interface{} {
	startedAt := time.Now().UTC()
	if target == "" || command == "" {
		return map[string]interface{}{
			"success": false,
			"tool":    "vssh_exec",
			"target":  target,
			"command": command,
			"error": map[string]interface{}{
				"code":    "missing_argument",
				"message": "target and command are required",
			},
		}
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}

	policy := classifyCommand(command)
	preapproved := false
	if !policy.Allowed && !allowDangerous {
		if ok, decision := daemonPreapproves(target, command); ok {
			policy.Allowed = true
			policy.Reason = "pre-approved by daemon policy (" + decision + ")"
			preapproved = true
		} else {
			endedAt := time.Now().UTC()
			return map[string]interface{}{
				"success":       false,
				"tool":          "vssh_exec",
				"target":        target,
				"command":       command,
				"started_at":    startedAt.Format(time.RFC3339Nano),
				"ended_at":      endedAt.Format(time.RFC3339Nano),
				"duration_ms":   endedAt.Sub(startedAt).Milliseconds(),
				"timeout_sec":   timeoutSeconds,
				"policy":        policy,
				"approved":      false,
				"blocked":       true,
				"evidence_type": "policy_decision",
				"error": map[string]interface{}{
					"code":      "policy_blocked",
					"message":   policy.Reason,
					"retryable": false,
				},
			}
		}
	}
	_ = preapproved
	if allowDangerous && !policy.Allowed {
		policy.Allowed = true
		policy.Reason = "dangerous command allowed by explicit allow_dangerous flag"
	}

	result, err := execNativeCapture(target, command, timeoutSeconds)
	if result == nil {
		result = &ssh.ExecResult{
			Success:   false,
			Host:      target,
			Target:    target,
			Command:   command,
			ExitCode:  -1,
			Transport: "vssh-native",
			Error: &ssh.ExecError{
				Code:      "execution_failed",
				Message:   "execution failed without result",
				Retryable: true,
			},
		}
	}
	endedAt := time.Now().UTC()
	payload := map[string]interface{}{
		"success":       err == nil,
		"tool":          "vssh_exec",
		"target":        target,
		"command":       command,
		"started_at":    startedAt.Format(time.RFC3339Nano),
		"ended_at":      endedAt.Format(time.RFC3339Nano),
		"duration_ms":   endedAt.Sub(startedAt).Milliseconds(),
		"timeout_sec":   timeoutSeconds,
		"policy":        policy,
		"approved":      allowDangerous,
		"blocked":       false,
		"evidence_type": "execution_result",
		"result":        result,
	}
	if err != nil {
		if result.Error != nil {
			payload["error"] = result.Error
		} else {
			payload["error"] = map[string]interface{}{
				"code":      "execution_failed",
				"message":   err.Error(),
				"retryable": true,
			}
		}
	}
	return payload
}

func execNativeCapture(target, command string, timeoutSeconds float64) (*ssh.ExecResult, error) {
	start := time.Now()
	result := &ssh.ExecResult{
		Host:      target,
		Target:    target,
		Command:   command,
		ExitCode:  -1,
		Transport: "vssh-native",
	}

	host := target
	if connector, err := ssh.NewConnector("default"); err == nil {
		if resolved, err := connector.ResolveHost(target); err == nil && resolved != "" {
			host = resolved
			server.SetExpectedHostKey(host, server.NodeKey(target))
		}
	}
	result.Endpoint = host
	result.Attempts = append(result.Attempts, ssh.ExecAttempt{
		Endpoint:  host,
		Path:      "native daemon",
		Transport: "vssh-native",
	})

	deadline := time.Duration(timeoutSeconds * float64(time.Second))
	if deadline <= 0 {
		deadline = 30 * time.Second
	}
	nativeResult, err := server.ExecCommandStructuredTimeout(host, getPort(), getSecret(), command, deadline)
	result.DurationMs = time.Since(start).Milliseconds()
	result.Stdout = nativeResult.Stdout
	result.Stderr = nativeResult.Stderr
	result.ExitCode = nativeResult.ExitCode
	if nativeResult.DurationMs > 0 {
		result.DurationMs = nativeResult.DurationMs
	}
	if err != nil {
		// Prefer the structured code the transport layer classified
		// (auth_failed / unreachable / timeout / bad_response); fall back to a
		// generic transport code only when none was set.
		code := nativeResult.ErrorCode
		retryable := nativeResult.Retryable
		if code == "" {
			code = "native_execution_failed"
			retryable = true
		}
		result.Error = &ssh.ExecError{
			Code:      code,
			Message:   err.Error(),
			Retryable: retryable,
		}
		return result, err
	}
	result.Success = nativeResult.Success
	if !nativeResult.Success {
		code := nativeResult.ErrorCode
		if code == "" {
			code = "remote_exit_nonzero"
		}
		result.Error = &ssh.ExecError{
			Code:      code,
			Message:   nativeResult.Error,
			Retryable: nativeResult.Retryable,
		}
		if result.Error.Message == "" {
			result.Error.Message = fmt.Sprintf("remote command exited with code %d", nativeResult.ExitCode)
		}
		return result, fmt.Errorf("%s", result.Error.Message)
	}
	return result, nil
}
