package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Frictionless MCP attach: emit or auto-merge the MCP client config so adding
// vssh to an AI client is one command instead of hand-editing config files.
// Known clients: claude / claude-desktop, claude-code, cursor, gemini /
// ai-studio (JSON, mcpServers), and codex (TOML, [mcp_servers.vssh]).

func vsshBinaryAbsPath() string {
	if exe, err := os.Executable(); err == nil {
		if abs, e := filepath.EvalSymlinks(exe); e == nil {
			return abs
		}
		return exe
	}
	return "vssh"
}

func mcpServerEntry() map[string]interface{} {
	return map[string]interface{}{"command": vsshBinaryAbsPath(), "args": []string{"mcp"}}
}

// mcpTOMLSnippet is the Codex (~/.codex/config.toml) form of the server entry.
func mcpTOMLSnippet() string {
	return "[mcp_servers.vssh]\ncommand = \"" + vsshBinaryAbsPath() + "\"\nargs = [\"mcp\"]"
}

const knownClients = "claude, claude-code, cursor, gemini/ai-studio, codex"

// clientConfigPath returns the MCP config file for a known AI client.
func clientConfigPath(client string) (string, bool) {
	home, _ := os.UserHomeDir()
	switch strings.ToLower(client) {
	case "claude", "claude-desktop":
		if runtime.GOOS == "darwin" {
			return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"), true
		}
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"), true
	case "cursor":
		return filepath.Join(home, ".cursor", "mcp.json"), true
	case "claude-code", "code":
		return filepath.Join(home, ".claude.json"), true
	case "gemini", "ai-studio", "aistudio", "google-ai-studio":
		return filepath.Join(home, ".gemini", "settings.json"), true
	case "codex":
		return filepath.Join(home, ".codex", "config.toml"), true
	}
	return "", false
}

// clientIsTOML reports whether the client's config is TOML (Codex) rather than
// the default JSON (mcpServers) shape.
func clientIsTOML(client string) bool { return strings.ToLower(client) == "codex" }

func parseClientFlag(args []string, def string) string {
	client := def
	for i := 0; i < len(args); i++ {
		if (args[i] == "--client" || args[i] == "-client") && i+1 < len(args) {
			client = args[i+1]
			i++
		}
	}
	return client
}

func cmdMCPConfig(args []string) {
	client := parseClientFlag(args, "")
	if clientIsTOML(client) {
		fmt.Println("# Add to ~/.codex/config.toml (or run: codex mcp add vssh -- " + vsshBinaryAbsPath() + " mcp):")
		fmt.Println(mcpTOMLSnippet())
	} else {
		full := map[string]interface{}{"mcpServers": map[string]interface{}{"vssh": mcpServerEntry()}}
		b, _ := json.MarshalIndent(full, "", "  ")
		fmt.Println("# Add to your MCP client config (merges under mcpServers.vssh):")
		fmt.Println(string(b))
	}
	if client == "" {
		fmt.Println("\n# Auto-install: vssh mcp-install --client " + strings.ReplaceAll(knownClients, "/ai-studio", "|gemini"))
		fmt.Println("# (known clients: " + knownClients + ")")
		return
	}
	if p, ok := clientConfigPath(client); ok {
		fmt.Printf("\n# %s config: %s\n# Auto-merge: vssh mcp-install --client %s\n", client, p, client)
	} else {
		fmt.Printf("\n# unknown client %q (known: %s)\n", client, knownClients)
	}
}

// installMCPServerTOML idempotently appends the Codex [mcp_servers.vssh] block.
func installMCPServerTOML(path string) (string, error) {
	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
		if strings.Contains(existing, "[mcp_servers.vssh]") {
			return path, nil // already present
		}
		_ = os.WriteFile(path+".bak."+time.Now().UTC().Format("20060102T150405Z"), data, 0600)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return path, err
	}
	sep := ""
	if existing != "" {
		if !strings.HasSuffix(existing, "\n") {
			sep = "\n"
		}
		sep += "\n"
	}
	return path, os.WriteFile(path, []byte(existing+sep+mcpTOMLSnippet()+"\n"), 0644)
}

// installMCPServer merges the vssh MCP server into the client's config file,
// preserving any other servers/keys, backing up an existing file first.
func installMCPServer(client string) (string, error) {
	path, ok := clientConfigPath(client)
	if !ok {
		return "", fmt.Errorf("unknown client %q (known: %s)", client, knownClients)
	}
	if clientIsTOML(client) {
		return installMCPServerTOML(path)
	}
	cfg := map[string]interface{}{}
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		if e := json.Unmarshal(data, &cfg); e != nil {
			return path, fmt.Errorf("existing config at %s is not valid JSON: %w", path, e)
		}
		_ = os.WriteFile(path+".bak."+time.Now().UTC().Format("20060102T150405Z"), data, 0600)
	}
	servers, _ := cfg["mcpServers"].(map[string]interface{})
	if servers == nil {
		servers = map[string]interface{}{}
	}
	servers["vssh"] = mcpServerEntry()
	cfg["mcpServers"] = servers
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return path, err
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	return path, os.WriteFile(path, out, 0644)
}

func cmdMCPInstall(args []string) {
	client := parseClientFlag(args, "claude")
	path, err := installMCPServer(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vssh: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("installed vssh MCP server -> %s (client=%s)\n", path, client)
	fmt.Printf("  command: %s mcp\n", vsshBinaryAbsPath())
	fmt.Println("Restart the client to load it.")
}
