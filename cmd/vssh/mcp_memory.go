package main

import (
	"github.com/zeus-kim/vssh/internal/fleet"
)

// memErr builds the standard MCP error envelope used by the memory tools.
func memErr(tool, code, msg string) map[string]interface{} {
	return map[string]interface{}{
		"success": false,
		"tool":    tool,
		"error":   map[string]interface{}{"code": code, "message": msg},
	}
}

// toolMemoryGet returns one node's memory, or the whole store when node is omitted.
func toolMemoryGet(args map[string]interface{}) map[string]interface{} {
	fm, err := fleet.Load()
	if err != nil {
		return memErr("vssh_memory_get", "load_failed", err.Error())
	}
	node := getString(args, "node")
	if node == "" {
		return map[string]interface{}{
			"success":    true,
			"tool":       "vssh_memory_get",
			"version":    fm.Version,
			"updated_at": fm.UpdatedAt,
			"node_count": len(fm.Nodes),
			"nodes":      fm.Nodes,
		}
	}
	mem, ok := fm.GetNode(node)
	if !ok {
		return memErr("vssh_memory_get", "not_found", "no memory for node "+node)
	}
	return map[string]interface{}{
		"success": true,
		"tool":    "vssh_memory_get",
		"node":    node,
		"memory":  mem,
	}
}

// toolMemorySet updates a node's role/services/tags, preserving its notes and
// any fields not supplied in this call.
func toolMemorySet(args map[string]interface{}) map[string]interface{} {
	node := getString(args, "node")
	if node == "" {
		return memErr("vssh_memory_set", "missing_argument", "node is required")
	}
	fm, err := fleet.Load()
	if err != nil {
		return memErr("vssh_memory_set", "load_failed", err.Error())
	}
	mem, _ := fm.GetNode(node)
	if _, ok := args["role"]; ok {
		mem.Role = getString(args, "role")
	}
	if _, ok := args["services"]; ok {
		mem.Services = getStringList(args, "services", mem.Services)
	}
	if _, ok := args["tags"]; ok {
		mem.Tags = getStringList(args, "tags", mem.Tags)
	}
	fm.SetNode(node, mem)
	if err := fm.Save(); err != nil {
		return memErr("vssh_memory_set", "save_failed", err.Error())
	}
	saved, _ := fm.GetNode(node)
	return map[string]interface{}{"success": true, "tool": "vssh_memory_set", "node": node, "memory": saved}
}

// toolMemoryFind returns nodes matching role/tag/service/text filters.
func toolMemoryFind(args map[string]interface{}) map[string]interface{} {
	fm, err := fleet.Load()
	if err != nil {
		return memErr("vssh_memory_find", "load_failed", err.Error())
	}
	hits := fm.Find(fleet.FleetFilter{
		Role:    getString(args, "role"),
		Tag:     getString(args, "tag"),
		Service: getString(args, "service"),
		Text:    getString(args, "text"),
	})
	return map[string]interface{}{
		"success":     true,
		"tool":        "vssh_memory_find",
		"match_count": len(hits),
		"nodes":       hits,
	}
}

// toolMemoryAutoNote extracts noteworthy notes from raw command output and
// records them on the node.
func toolMemoryAutoNote(args map[string]interface{}) map[string]interface{} {
	node := getString(args, "node")
	output := getString(args, "output")
	if node == "" || output == "" {
		return memErr("vssh_memory_auto_note", "missing_argument", "node and output are required")
	}
	fm, err := fleet.Load()
	if err != nil {
		return memErr("vssh_memory_auto_note", "load_failed", err.Error())
	}
	extracted := fm.AutoNote(node, getString(args, "command"), output)
	if err := fm.Save(); err != nil {
		return memErr("vssh_memory_auto_note", "save_failed", err.Error())
	}
	mem, _ := fm.GetNode(node)
	return map[string]interface{}{
		"success":   true,
		"tool":      "vssh_memory_auto_note",
		"node":      node,
		"extracted": extracted,
		"memory":    mem,
	}
}

// toolMemoryAsk answers a natural-language query over fleet memory.
func toolMemoryAsk(args map[string]interface{}) map[string]interface{} {
	query := getString(args, "query")
	if query == "" {
		return memErr("vssh_memory_ask", "missing_argument", "query is required")
	}
	fm, err := fleet.Load()
	if err != nil {
		return memErr("vssh_memory_ask", "load_failed", err.Error())
	}
	hits := fm.Ask(query)
	return map[string]interface{}{
		"success":     true,
		"tool":        "vssh_memory_ask",
		"query":       query,
		"match_count": len(hits),
		"matches":     hits,
	}
}

// toolMemoryNote appends a timestamped note to a node.
func toolMemoryNote(args map[string]interface{}) map[string]interface{} {
	node := getString(args, "node")
	text := getString(args, "text")
	if node == "" || text == "" {
		return memErr("vssh_memory_note", "missing_argument", "node and text are required")
	}
	fm, err := fleet.Load()
	if err != nil {
		return memErr("vssh_memory_note", "load_failed", err.Error())
	}
	fm.AddNote(node, text)
	if err := fm.Save(); err != nil {
		return memErr("vssh_memory_note", "save_failed", err.Error())
	}
	mem, _ := fm.GetNode(node)
	return map[string]interface{}{"success": true, "tool": "vssh_memory_note", "node": node, "memory": mem}
}
