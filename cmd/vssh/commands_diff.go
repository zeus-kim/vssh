package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zeus-kim/vssh/internal/diff"
	"github.com/zeus-kim/vssh/internal/server"
)

// readAuditSource returns the raw audit-log bytes to analyze: the local daemon
// log when node is empty, otherwise the remote node's log fetched over vssh's
// own transport (no external dependency). The daemon log lives at one of two
// paths depending on whether it runs as root, so both are catted.
func readAuditSource(node string) ([]byte, error) {
	if strings.TrimSpace(node) == "" {
		return os.ReadFile(server.AuditLogPath())
	}
	host := resolveReachableHost(node, getPort())
	cmd := `cat /var/log/vssh/audit.log "$HOME/.vssh/audit.log" 2>/dev/null`
	res, err := server.ExecCommandStructuredTimeout(host, getPort(), getSecret(), cmd, 30*time.Second)
	if err != nil {
		return nil, err
	}
	return []byte(res.Stdout), nil
}

func diffOptionsFromArgs(node *string, jsonOut *bool, args []string) (diff.Options, error) {
	opts := diff.Options{LastN: 10}
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--node="):
			*node = strings.TrimPrefix(a, "--node=")
		case strings.HasPrefix(a, "--last="):
			n, err := strconv.Atoi(strings.TrimPrefix(a, "--last="))
			if err != nil {
				return opts, fmt.Errorf("invalid --last: %v", err)
			}
			opts.LastN = n
		case strings.HasPrefix(a, "--since="):
			d, err := time.ParseDuration(strings.TrimPrefix(a, "--since="))
			if err != nil {
				return opts, fmt.Errorf("invalid --since: %v", err)
			}
			opts.Since = d
		case a == "--json":
			*jsonOut = true
		default:
			return opts, fmt.Errorf("unknown flag %q", a)
		}
	}
	return opts, nil
}

func cmdDiff(args []string) {
	node := ""
	jsonOut := false
	opts, err := diffOptionsFromArgs(&node, &jsonOut, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vssh: %v\nusage: vssh diff [--node=X] [--last=N] [--since=1h] [--json]\n", err)
		os.Exit(1)
	}
	data, err := readAuditSource(node)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vssh: %v\n", err)
		os.Exit(1)
	}
	sessions, err := diff.AnalyzeReader(bytes.NewReader(data), opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vssh: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		b, _ := json.MarshalIndent(sessions, "", "  ")
		fmt.Println(string(b))
		return
	}
	if len(sessions) == 0 {
		fmt.Println("no audit activity in range")
		return
	}
	for i, s := range sessions {
		key := s.KeyName
		if key == "" {
			key = "-"
		}
		fmt.Printf("session %d  key=%s  endpoint=%s  %s  (%d cmds, %dms)\n",
			i+1, key, s.Remote, s.Start.Format(time.RFC3339), len(s.Outcomes), s.DurationMs)
		fmt.Printf("  summary: %s\n", s.Summary)
		for _, o := range s.Outcomes {
			status := "ok"
			if !o.Success {
				status = fmt.Sprintf("exit %d", o.ExitCode)
			}
			line := fmt.Sprintf("  - [%s] %s", status, o.Command)
			if o.Detail != "" {
				line += "   (" + o.Detail + ")"
			}
			fmt.Println(line)
		}
	}
}

// toolDiff (MCP) summarizes recent audit activity into sessions with one-line
// natural-language summaries.
func toolDiff(args map[string]interface{}) map[string]interface{} {
	opts := diff.Options{LastN: int(getFloat(args, "last_n", 10))}
	if s := getString(args, "since"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return map[string]interface{}{"success": false, "tool": "vssh_diff",
				"error": map[string]interface{}{"code": "bad_argument", "message": "invalid since: " + err.Error()}}
		}
		opts.Since = d
	}
	node := getString(args, "node")
	data, err := readAuditSource(node)
	if err != nil {
		return map[string]interface{}{"success": false, "tool": "vssh_diff",
			"error": map[string]interface{}{"code": "audit_read_failed", "message": err.Error()}}
	}
	sessions, err := diff.AnalyzeReader(bytes.NewReader(data), opts)
	if err != nil {
		return map[string]interface{}{"success": false, "tool": "vssh_diff",
			"error": map[string]interface{}{"code": "parse_failed", "message": err.Error()}}
	}
	return map[string]interface{}{
		"success":       true,
		"tool":          "vssh_diff",
		"node":          node,
		"session_count": len(sessions),
		"sessions":      sessions,
	}
}
