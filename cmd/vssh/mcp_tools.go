package main

import (
	"time"

	"github.com/zeus-kim/vssh/internal/server"
)

func toolRPCCall(args map[string]interface{}) map[string]interface{} {
	target := getString(args, "target")
	method := getString(args, "method")
	if target == "" || method == "" {
		return map[string]interface{}{
			"success": false,
			"tool":    "vssh_rpc_call",
			"target":  target,
			"method":  method,
			"error": map[string]interface{}{
				"code":    "missing_argument",
				"message": "target and method are required",
			},
		}
	}

	startedAt := time.Now().UTC()
	timeout := time.Duration(getFloat(args, "timeout_seconds", 30) * float64(time.Second))
	resp, err := callRPCNative(target, method, getParamsMap(args, "params"), timeout)
	endedAt := time.Now().UTC()
	payload := map[string]interface{}{
		"success":     err == nil && resp != nil && resp.Success,
		"tool":        "vssh_rpc_call",
		"target":      target,
		"method":      method,
		"started_at":  startedAt.Format(time.RFC3339Nano),
		"ended_at":    endedAt.Format(time.RFC3339Nano),
		"duration_ms": endedAt.Sub(startedAt).Milliseconds(),
		"result":      resp,
	}
	if err != nil {
		payload["error"] = map[string]interface{}{
			"code":      "rpc_failed",
			"message":   err.Error(),
			"retryable": true,
		}
	} else if resp != nil && !resp.Success {
		payload["error"] = map[string]interface{}{
			"code":      "rpc_error",
			"message":   resp.Error,
			"retryable": false,
		}
	}
	return payload
}

func toolRPCMany(args map[string]interface{}) map[string]interface{} {
	targets := getStringList(args, "targets", nil)
	method := getString(args, "method")
	if len(targets) == 0 || method == "" {
		return map[string]interface{}{
			"success": false,
			"tool":    "vssh_rpc_many",
			"targets": targets,
			"method":  method,
			"error": map[string]interface{}{
				"code":    "missing_argument",
				"message": "targets and method are required",
			},
		}
	}

	startedAt := time.Now().UTC()
	maxParallelism := int(getFloat(args, "max_parallelism", float64(defaultMaxParallelism())))
	results := rpcMany(targets, method, getParamsMap(args, "params"), time.Duration(getFloat(args, "timeout_seconds", 30)*float64(time.Second)), maxParallelism)
	endedAt := time.Now().UTC()
	ok, failed := countRPCMany(results)
	return map[string]interface{}{
		"success":         failed == 0,
		"tool":            "vssh_rpc_many",
		"targets":         targets,
		"method":          method,
		"max_parallelism": normalizedMaxParallelism(maxParallelism, len(targets)),
		"started_at":      startedAt.Format(time.RFC3339Nano),
		"ended_at":        endedAt.Format(time.RFC3339Nano),
		"duration_ms":     endedAt.Sub(startedAt).Milliseconds(),
		"summary": map[string]interface{}{
			"total":  len(targets),
			"ok":     ok,
			"failed": failed,
		},
		"results": results,
	}
}

func toolFacts(args map[string]interface{}) map[string]interface{} {
	target := getString(args, "target")
	if target == "" {
		return map[string]interface{}{
			"success": false,
			"tool":    "vssh_facts",
			"error": map[string]interface{}{
				"code":    "missing_argument",
				"message": "target is required",
			},
		}
	}
	startedAt := time.Now().UTC()
	host, port := parseHostPort(target)
	host = resolveReachableHost(host, port)
	info, err := server.GetInfo(host, port, getSecret(), time.Duration(getFloat(args, "timeout_seconds", 30)*float64(time.Second)))
	endedAt := time.Now().UTC()
	payload := map[string]interface{}{
		"success":     err == nil,
		"tool":        "vssh_facts",
		"target":      target,
		"endpoint":    host,
		"started_at":  startedAt.Format(time.RFC3339Nano),
		"ended_at":    endedAt.Format(time.RFC3339Nano),
		"duration_ms": endedAt.Sub(startedAt).Milliseconds(),
		"facts":       info,
	}
	if err != nil {
		payload["error"] = map[string]interface{}{
			"code":      "facts_failed",
			"message":   err.Error(),
			"retryable": true,
		}
	}
	return payload
}

func toolFactsMany(args map[string]interface{}) map[string]interface{} {
	targets := getStringList(args, "targets", nil)
	if len(targets) == 0 {
		return map[string]interface{}{
			"success": false,
			"tool":    "vssh_facts_many",
			"error": map[string]interface{}{
				"code":    "missing_argument",
				"message": "targets is required",
			},
		}
	}
	startedAt := time.Now().UTC()
	maxParallelism := int(getFloat(args, "max_parallelism", float64(defaultMaxParallelism())))
	results := factsMany(targets, time.Duration(getFloat(args, "timeout_seconds", 30)*float64(time.Second)), maxParallelism)
	endedAt := time.Now().UTC()
	ok, failed := countFactsMany(results)
	return map[string]interface{}{
		"success":         failed == 0,
		"tool":            "vssh_facts_many",
		"targets":         targets,
		"max_parallelism": normalizedMaxParallelism(maxParallelism, len(targets)),
		"started_at":      startedAt.Format(time.RFC3339Nano),
		"ended_at":        endedAt.Format(time.RFC3339Nano),
		"duration_ms":     endedAt.Sub(startedAt).Milliseconds(),
		"summary": map[string]interface{}{
			"total":  len(targets),
			"ok":     ok,
			"failed": failed,
		},
		"results": results,
	}
}

func countFactsMany(results []multiFactsResult) (ok int, failed int) {
	for _, result := range results {
		if result.Error != "" || result.Result == nil {
			failed++
			continue
		}
		ok++
	}
	return ok, failed
}

func toolJobStart(args map[string]interface{}) map[string]interface{} {
	target := getString(args, "target")
	command := getString(args, "command")
	if target == "" || command == "" {
		return map[string]interface{}{
			"success": false,
			"tool":    "vssh_job_start",
			"error": map[string]interface{}{
				"code":    "missing_argument",
				"message": "target and command are required",
			},
		}
	}
	policy := classifyCommand(command)
	allowDangerous := getBool(args, "allow_dangerous", false)
	if !policy.Allowed && !allowDangerous {
		return map[string]interface{}{
			"success": false,
			"tool":    "vssh_job_start",
			"target":  target,
			"command": command,
			"policy":  policy,
			"blocked": true,
			"error": map[string]interface{}{
				"code":      "policy_blocked",
				"message":   policy.Reason,
				"retryable": false,
			},
		}
	}
	return toolRPCJobCall("vssh_job_start", target, "job_start", map[string]interface{}{"command": command})
}

func toolJobRPC(args map[string]interface{}, tool, method string) map[string]interface{} {
	target := getString(args, "target")
	id := getString(args, "id")
	if target == "" || id == "" {
		return map[string]interface{}{
			"success": false,
			"tool":    tool,
			"error": map[string]interface{}{
				"code":    "missing_argument",
				"message": "target and id are required",
			},
		}
	}
	params := map[string]interface{}{"id": id}
	if method == "job_logs" {
		params["tail_bytes"] = getFloat(args, "tail_bytes", 0)
	}
	return toolRPCJobCall(tool, target, method, params)
}

func toolRPCJobCall(tool, target, method string, params map[string]interface{}) map[string]interface{} {
	startedAt := time.Now().UTC()
	resp, err := callRPCNative(target, method, params, 30*time.Second)
	endedAt := time.Now().UTC()
	payload := map[string]interface{}{
		"success":     err == nil && resp != nil && resp.Success,
		"tool":        tool,
		"target":      target,
		"method":      method,
		"started_at":  startedAt.Format(time.RFC3339Nano),
		"ended_at":    endedAt.Format(time.RFC3339Nano),
		"duration_ms": endedAt.Sub(startedAt).Milliseconds(),
		"response":    resp,
	}
	if err != nil {
		payload["error"] = map[string]interface{}{"code": "job_rpc_failed", "message": err.Error(), "retryable": true}
	} else if resp != nil && !resp.Success {
		payload["error"] = map[string]interface{}{"code": "job_rpc_error", "message": resp.Error, "retryable": false}
	}
	return payload
}

func toolArtifactCollect(args map[string]interface{}) map[string]interface{} {
	target := getString(args, "target")
	path := getString(args, "path")
	if target == "" || path == "" {
		return map[string]interface{}{
			"success": false,
			"tool":    "vssh_artifact_collect",
			"error": map[string]interface{}{
				"code":    "missing_argument",
				"message": "target and path are required",
			},
		}
	}
	return toolRPCJobCall("vssh_artifact_collect", target, "artifact_collect", map[string]interface{}{
		"path":      path,
		"max_bytes": getFloat(args, "max_bytes", 1024*1024),
	})
}

func countRPCMany(results []multiRPCResult) (int, int) {
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
