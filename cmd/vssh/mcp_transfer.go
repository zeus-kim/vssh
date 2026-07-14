package main

import (
	"github.com/zeus-kim/vssh/internal/server"
)

// toolPut (MCP) uploads a local file to a node. The daemon enforces the
// authenticated key's path policy on the write, so exposure here is gated by
// the same policy engine as exec — not an unrestricted write.
func toolPut(args map[string]interface{}) map[string]interface{} {
	target := getString(args, "target")
	local := getString(args, "local_path")
	remote := getString(args, "remote_path")
	if target == "" || local == "" || remote == "" {
		return transferErr("vssh_put", "missing_argument", "target, local_path, and remote_path are required")
	}
	host := resolveReachableHost(target, getPort())
	n, err := server.SendFile(host, getPort(), getSecret(), local, remote)
	if err != nil {
		return transferErr("vssh_put", "transfer_failed", err.Error())
	}
	return map[string]interface{}{
		"success": true, "tool": "vssh_put", "target": target,
		"local_path": local, "remote_path": remote, "bytes": n,
	}
}

// toolGet (MCP) downloads a file from a node to the controller.
func toolGet(args map[string]interface{}) map[string]interface{} {
	target := getString(args, "target")
	remote := getString(args, "remote_path")
	local := getString(args, "local_path")
	if target == "" || remote == "" || local == "" {
		return transferErr("vssh_get", "missing_argument", "target, remote_path, and local_path are required")
	}
	host := resolveReachableHost(target, getPort())
	n, err := server.RecvFile(host, getPort(), getSecret(), remote, local)
	if err != nil {
		return transferErr("vssh_get", "transfer_failed", err.Error())
	}
	return map[string]interface{}{
		"success": true, "tool": "vssh_get", "target": target,
		"remote_path": remote, "local_path": local, "bytes": n,
	}
}

// toolDeployBinary (MCP) ships a binary to a node: upload → atomic privileged
// install → optional service restart → verify, in one call. Same policy/audit
// path as exec on the daemon.
func toolDeployBinary(args map[string]interface{}) map[string]interface{} {
	target := getString(args, "target")
	local := getString(args, "local_path")
	remote := getString(args, "remote_path")
	if target == "" || local == "" || remote == "" {
		return transferErr("vssh_deploy_binary", "missing_argument", "target, local_path, and remote_path are required")
	}
	host, port := parseHostPort(target)
	r := runDeployBinary(local, host, port, remote, getString(args, "service"), getString(args, "mode"), getString(args, "verify"))
	m := map[string]interface{}{
		"success": r.Success, "tool": "vssh_deploy_binary",
		"host": r.Host, "remote_path": r.RemotePath, "phase": r.Phase,
	}
	if r.Service != "" {
		m["service"] = r.Service
	}
	if r.VerifyOutput != "" {
		m["verify_output"] = r.VerifyOutput
	}
	if !r.Success {
		m["error"] = map[string]interface{}{"code": r.ErrorCode, "message": r.Error}
	}
	return m
}

func transferErr(tool, code, msg string) map[string]interface{} {
	return map[string]interface{}{
		"success": false, "tool": tool,
		"error": map[string]interface{}{"code": code, "message": msg},
	}
}
