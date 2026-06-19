package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/zeus-kim/vssh/internal/server"
)

// AI-driven local config management (MCP). Mutating tools are OFF by default and
// require VSSH_ALLOW_CONFIG_WRITE=1 on the vssh mcp server — the operator
// explicitly delegates local config authority to the AI. They edit only this
// host's config dir; fleet-wide propagation stays an explicit scripts/* step.

func configWriteAllowed() bool {
	v := strings.TrimSpace(os.Getenv("VSSH_ALLOW_CONFIG_WRITE"))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

func configDisabledResult(tool string) map[string]interface{} {
	return map[string]interface{}{"success": false, "tool": tool,
		"error": map[string]interface{}{"code": "config_write_disabled",
			"message": "config-mutating tools are OFF by default; set VSSH_ALLOW_CONFIG_WRITE=1 on the vssh mcp server to let the AI manage local config"}}
}

func cfgErr(tool, code, msg string) map[string]interface{} {
	return map[string]interface{}{"success": false, "tool": tool, "error": map[string]interface{}{"code": code, "message": msg}}
}

func toolConfigAuthorizeKey(args map[string]interface{}) map[string]interface{} {
	const tool = "vssh_config_authorize_key"
	if !configWriteAllowed() {
		return configDisabledResult(tool)
	}
	pub := strings.TrimSpace(getString(args, "pubkey"))
	if pub == "" {
		return cfgErr(tool, "missing_argument", "pubkey is required")
	}
	if err := server.AuthorizeKey(pub, getString(args, "caps"), getString(args, "comment")); err != nil {
		return cfgErr(tool, "write_failed", err.Error())
	}
	return map[string]interface{}{"success": true, "tool": tool, "authorized": pub,
		"path": filepath.Join(server.ConfigDir(), "authorized_keys"),
		"note": "local controller only; use scripts/rotate_authorized_key.sh to propagate fleet-wide"}
}

func toolConfigRevokeKey(args map[string]interface{}) map[string]interface{} {
	const tool = "vssh_config_revoke_key"
	if !configWriteAllowed() {
		return configDisabledResult(tool)
	}
	pub := strings.TrimSpace(getString(args, "pubkey"))
	if pub == "" {
		return cfgErr(tool, "missing_argument", "pubkey is required")
	}
	removed, err := server.RevokeKey(pub)
	if err != nil {
		return cfgErr(tool, "write_failed", err.Error())
	}
	return map[string]interface{}{"success": true, "tool": tool, "removed": removed, "pubkey": pub}
}

func toolConfigSetNode(args map[string]interface{}) map[string]interface{} {
	const tool = "vssh_config_set_node"
	if !configWriteAllowed() {
		return configDisabledResult(tool)
	}
	name := strings.TrimSpace(getString(args, "name"))
	ip := strings.TrimSpace(getString(args, "ip"))
	if name == "" || ip == "" {
		return cfgErr(tool, "missing_argument", "name and ip are required")
	}
	if err := server.SetNodeConfig(name, ip); err != nil {
		return cfgErr(tool, "write_failed", err.Error())
	}
	return map[string]interface{}{"success": true, "tool": tool, "name": name, "ip": ip}
}

func toolConfigPinNode(args map[string]interface{}) map[string]interface{} {
	const tool = "vssh_config_pin_node"
	if !configWriteAllowed() {
		return configDisabledResult(tool)
	}
	name := strings.TrimSpace(getString(args, "name"))
	pub := strings.TrimSpace(getString(args, "pubkey"))
	if name == "" || pub == "" {
		return cfgErr(tool, "missing_argument", "name and pubkey are required")
	}
	if err := server.PinNode(name, pub); err != nil {
		return cfgErr(tool, "write_failed", err.Error())
	}
	return map[string]interface{}{"success": true, "tool": tool, "name": name, "pubkey": pub}
}

// toolConfigList is read-only (no gate): shows local config the AI can manage.
func toolConfigList(args map[string]interface{}) map[string]interface{} {
	dir := server.ConfigDir()
	readLines := func(name string) []string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil
		}
		var out []string
		for _, l := range strings.Split(string(data), "\n") {
			l = strings.TrimSpace(l)
			if l != "" && !strings.HasPrefix(l, "#") {
				out = append(out, l)
			}
		}
		return out
	}
	authorized := []map[string]string{}
	for _, l := range readLines("authorized_keys") {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		e := map[string]string{"pubkey": f[0]}
		var comment []string
		for _, x := range f[1:] {
			switch {
			case strings.HasPrefix(x, "caps="):
				e["caps"] = strings.TrimPrefix(x, "caps=")
			case strings.HasPrefix(x, "policy="):
				e["policy"] = strings.TrimPrefix(x, "policy=")
			default:
				comment = append(comment, x)
			}
		}
		if len(comment) > 0 {
			e["comment"] = strings.Join(comment, " ")
		}
		authorized = append(authorized, e)
	}
	return map[string]interface{}{"success": true, "tool": "vssh_config_list",
		"config_dir": dir, "authorized_keys": authorized,
		"config": readLines("config"), "node_keys": readLines("node_keys"),
		"write_enabled": configWriteAllowed()}
}
