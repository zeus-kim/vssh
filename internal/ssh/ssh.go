package ssh

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/zeus-kim/vssh/internal/config"
)

// Connector handles SSH connections to peers
type Connector struct {
	config  *config.WireConfig
	peers   []config.Peer
	network string
}

// ExecAttempt records one endpoint attempt for machine-readable audit output.
type ExecAttempt struct {
	Endpoint  string `json:"endpoint"`
	User      string `json:"user,omitempty"`
	Path      string `json:"path"`
	Transport string `json:"transport"`
	Error     string `json:"error,omitempty"`
}

// ExecError is a stable machine-readable execution failure.
type ExecError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// ExecResult captures remote command execution for MCP/LLM orchestration.
type ExecResult struct {
	Success      bool          `json:"success"`
	Host         string        `json:"host"`
	Target       string        `json:"target"`
	Command      string        `json:"command"`
	Endpoint     string        `json:"endpoint,omitempty"`
	Stdout       string        `json:"stdout"`
	Stderr       string        `json:"stderr"`
	ExitCode     int           `json:"exit_code"`
	DurationMs   int64         `json:"duration_ms"`
	Attempts     []ExecAttempt `json:"attempts"`
	Transport    string        `json:"transport"`
	FallbackUsed bool          `json:"fallback_used"`
	Error        *ExecError    `json:"error,omitempty"`
}

// NewConnector creates a new SSH connector
func NewConnector(network string) (*Connector, error) {
	cfg, err := config.LoadWireConfig()
	if err != nil {
		// Try to work without config (use coordinator directly)
		cfg = &config.WireConfig{}
	}

	c := &Connector{
		config:  cfg,
		network: network,
	}

	// Try to get peers
	c.loadPeers()

	return c, nil
}

// Connect connects to a peer by name or VPN IP
func (c *Connector) Connect(target string, extraArgs []string) error {
	peer := c.findPeer(target)
	if peer == nil {
		return fmt.Errorf("peer not found: %s", target)
	}

	// Try connection methods in order
	endpoints := c.getEndpoints(peer)

	for _, ep := range endpoints {
		fmt.Printf("Trying %s...\n", ep.desc)
		if c.trySSH(ep.host, ep.user, extraArgs) {
			return nil
		}
	}

	return fmt.Errorf("failed to connect to %s", target)
}

// ListPeers lists all known peers
func (c *Connector) ListPeers() []config.Peer {
	return c.peers
}

// Status returns connection status
func (c *Connector) Status() string {
	var sb strings.Builder

	Reset := "\033[0m"
	Bold := "\033[1m"
	Dim := "\033[2m"
	Green := "\033[92m"
	Red := "\033[91m"
	Cyan := "\033[96m"

	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("  %s%svssh dashboard%s\n", Bold, Cyan, Reset))
	sb.WriteString("\n")

	// Header
	sb.WriteString(fmt.Sprintf("  %s  %-8s %-15s  %5s  %4s  %4s  %s%s\n",
		Dim, "SERVER", "IP", "LOAD", "MEM", "DISK", "UPTIME", Reset))
	sb.WriteString(fmt.Sprintf("  %s%s%s\n", Dim, strings.Repeat("-", 56), Reset))

	// Sort peers by name
	sortedPeers := make([]config.Peer, len(c.peers))
	copy(sortedPeers, c.peers)
	sort.Slice(sortedPeers, func(i, j int) bool {
		return sortedPeers[i].NodeName < sortedPeers[j].NodeName
	})

	onlineCount := 0
	total := 0
	now := time.Now().Unix()

	for _, p := range sortedPeers {
		if p.NodeName == "" {
			continue
		}
		total++

		name := p.NodeName
		if len(name) > 8 {
			name = name[:8]
		}

		// Check if peer is online based on last_seen (within 60 seconds)
		online := false
		if p.LastSeen != nil {
			switch v := p.LastSeen.(type) {
			case float64:
				online = (now - int64(v)) < 60
			case int64:
				online = (now - v) < 60
			case string:
				if t, err := time.Parse(time.RFC3339, v); err == nil {
					online = time.Since(t) < 60*time.Second
				}
			}
		}

		var dot string
		if online {
			dot = Green + "●" + Reset
			onlineCount++
		} else {
			dot = Red + "○" + Reset
		}

		// Format stats
		load, mem, disk, uptime := "-", "-", "-", "-"
		if p.Stats != nil {
			if p.Stats.LoadValue > 0 {
				load = fmt.Sprintf("%.2f", p.Stats.LoadValue)
			} else if p.Stats.Load != "" {
				load = p.Stats.Load
			}
			if p.Stats.MemPct > 0 {
				mem = fmt.Sprintf("%d%%", p.Stats.MemPct)
			}
			if p.Stats.DiskPct > 0 {
				disk = fmt.Sprintf("%d%%", p.Stats.DiskPct)
			}
			if p.Stats.Uptime != "" {
				uptime = p.Stats.Uptime
			}
		}

		sb.WriteString(fmt.Sprintf("  %s %-8s %-15s  %5s  %4s  %4s  %s\n",
			dot, name, p.VpnIP, load, mem, disk, uptime))
	}

	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("  %s%d/%d online%s\n", Green, onlineCount, total, Reset))
	sb.WriteString("\n")

	return sb.String()
}

// OverlayStats replaces cached per-peer stats with freshly supplied ones, keyed
// by node name. Peers absent from m keep their existing (cached) stats. Used to
// override the node_monitor source with live daemon-RPC readings at display time
// so the dashboard reflects the current moment, not a stale monitor snapshot.
func (c *Connector) OverlayStats(m map[string]*config.PeerStats) {
	if len(m) == 0 {
		return
	}
	for i := range c.peers {
		if s, ok := m[c.peers[i].NodeName]; ok && s != nil {
			c.peers[i].Stats = s
		}
	}
}

// Exec executes a command on a peer
func (c *Connector) Exec(target string, command []string) error {
	peer := c.findPeer(target)
	if peer == nil {
		return fmt.Errorf("peer not found: %s", target)
	}

	endpoints := c.getEndpoints(peer)

	for _, ep := range endpoints {
		target := ep.host
		if ep.user != "" {
			target = ep.user + "@" + ep.host
		}
		args := []string{
			"-o", "ConnectTimeout=10",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "LogLevel=ERROR",
			target,
		}
		args = append(args, command...)

		cmd := exec.Command("ssh", args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err == nil {
			return nil
		}
	}

	return fmt.Errorf("failed to execute on %s", target)
}

// ExecCapture executes a shell command on a peer and returns structured output.
//
// The command is sent to the remote node on stdin and executed with "sh -s".
// This avoids local shell parsing and preserves quotes, pipes, JSON, and other
// command text that LLM/MCP callers commonly pass as a single string.
func (c *Connector) ExecCapture(target string, command string, timeout time.Duration) (*ExecResult, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	result := &ExecResult{
		Host:      target,
		Target:    target,
		Command:   command,
		ExitCode:  -1,
		Transport: "ssh",
	}
	peer := c.findPeer(target)
	if peer == nil {
		result.Error = &ExecError{
			Code:      "peer_not_found",
			Message:   fmt.Sprintf("peer not found: %s", target),
			Retryable: false,
		}
		return result, fmt.Errorf("%s", result.Error.Message)
	}

	start := time.Now()
	endpoints := c.getEndpoints(peer)

	for _, ep := range endpoints {
		attempt := ExecAttempt{Endpoint: ep.host, User: ep.user, Path: ep.desc, Transport: "ssh"}
		args := []string{
			"-o", "ConnectTimeout=10",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "LogLevel=ERROR",
			ep.host,
			"sh", "-s",
		}
		if ep.user != "" {
			args[8] = ep.user + "@" + ep.host
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, "ssh", args...)
		cmd.Stdin = strings.NewReader(command)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		cancel()

		result.DurationMs = time.Since(start).Milliseconds()
		result.Endpoint = ep.host
		result.Stdout = stdout.String()
		result.Stderr = stderr.String()
		result.FallbackUsed = len(result.Attempts) > 0

		if ctx.Err() == context.DeadlineExceeded {
			attempt.Error = "timeout"
			result.Attempts = append(result.Attempts, attempt)
			result.ExitCode = 124
			result.Error = &ExecError{
				Code:      "timeout",
				Message:   fmt.Sprintf("command timed out after %s", timeout),
				Retryable: true,
			}
			return result, fmt.Errorf("command timed out after %s", timeout)
		}

		if err == nil {
			result.Success = true
			result.ExitCode = 0
			result.Attempts = append(result.Attempts, attempt)
			return result, nil
		}

		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			attempt.Error = err.Error()
			result.Attempts = append(result.Attempts, attempt)
			result.Error = &ExecError{
				Code:      "remote_exit_nonzero",
				Message:   err.Error(),
				Retryable: false,
			}
			return result, err
		}

		attempt.Error = err.Error()
		result.Attempts = append(result.Attempts, attempt)
	}

	result.DurationMs = time.Since(start).Milliseconds()
	result.FallbackUsed = len(result.Attempts) > 1
	result.Error = &ExecError{
		Code:      "endpoint_unreachable",
		Message:   fmt.Sprintf("failed to execute on %s", target),
		Retryable: true,
	}
	return result, fmt.Errorf("failed to execute on %s", target)
}

// Copy copies files to/from a peer using scp
func (c *Connector) Copy(src, dst string) error {
	// Parse src and dst to determine direction
	// Format: peer:path or path
	srcPeer, srcPath := parsePath(src)
	dstPeer, dstPath := parsePath(dst)

	var peer *config.Peer
	var localPath, remotePath string
	var toRemote bool

	if srcPeer != "" {
		peer = c.findPeer(srcPeer)
		remotePath = srcPath
		localPath = dst
		toRemote = false
	} else if dstPeer != "" {
		peer = c.findPeer(dstPeer)
		remotePath = dstPath
		localPath = src
		toRemote = true
	} else {
		return fmt.Errorf("either source or destination must be a remote peer")
	}

	if peer == nil {
		return fmt.Errorf("peer not found")
	}

	endpoints := c.getEndpoints(peer)

	for _, ep := range endpoints {
		var src, dst string
		if toRemote {
			src = localPath
			dst = ep.host + ":" + remotePath
		} else {
			src = ep.host + ":" + remotePath
			dst = localPath
		}

		args := []string{
			"-o", "ConnectTimeout=10",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-r",
			src, dst,
		}

		cmd := exec.Command("scp", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err == nil {
			return nil
		}
	}

	return fmt.Errorf("failed to copy")
}

func parsePath(path string) (peer, filepath string) {
	if idx := strings.Index(path, ":"); idx > 0 {
		return path[:idx], path[idx+1:]
	}
	return "", path
}

// StreamOutput streams command output from a peer
func (c *Connector) StreamOutput(target string, command []string, stdout, stderr io.Writer) error {
	peer := c.findPeer(target)
	if peer == nil {
		return fmt.Errorf("peer not found: %s", target)
	}

	endpoints := c.getEndpoints(peer)

	for _, ep := range endpoints {
		args := []string{
			"-o", "ConnectTimeout=10",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "LogLevel=ERROR",
			ep.host,
		}
		args = append(args, command...)

		cmd := exec.Command("ssh", args...)
		cmd.Stdout = stdout
		cmd.Stderr = stderr

		if err := cmd.Run(); err == nil {
			return nil
		}
	}

	return fmt.Errorf("failed to execute on %s", target)
}
