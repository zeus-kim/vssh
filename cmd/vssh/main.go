package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zeus-kim/vssh/internal/agent"
	"github.com/zeus-kim/vssh/internal/server"
	"github.com/zeus-kim/vssh/internal/ssh"
)

var (
	version   = "0.7.48"
	buildTime = ""
)

func main() {
	if len(os.Args) < 2 {
		// Default to status
		cmdStatus()
		return
	}

	cmd := os.Args[1]

	switch cmd {
	case "server", "daemon", "vsshd":
		cmdServer(os.Args[2:])
	case "shell":
		cmdShell(os.Args[2:])
	case "put":
		cmdPut(os.Args[2:])
	case "get":
		cmdGet(os.Args[2:])
	case "deploy-binary", "deploy":
		cmdDeployBinary(os.Args[2:])
	case "run":
		cmdRun(os.Args[2:])
	case "run-many", "exec-many":
		cmdRunMany(os.Args[2:])
	case "run-async", "run-job":
		cmdRunAsync(os.Args[2:])
	case "run-batch":
		cmdRunBatch(os.Args[2:])
	case "rpc":
		cmdRPC(os.Args[2:])
	case "rpc-many":
		cmdRPCMany(os.Args[2:])
	case "facts":
		cmdFacts(os.Args[2:])
	case "facts-many":
		cmdFactsMany(os.Args[2:])
	case "job-start":
		cmdJobStart(os.Args[2:])
	case "job-status":
		cmdJobStatus(os.Args[2:])
	case "job-logs":
		cmdJobLogs(os.Args[2:])
	case "job-cancel":
		cmdJobCancel(os.Args[2:])
	case "artifact-collect":
		cmdArtifactCollect(os.Args[2:])
	case "pubkey":
		cmdPubkey()
	case "keygen", "rotate-id", "rotate-key":
		cmdKeygen(os.Args[2:])
	case "handshake-test":
		cmdHandshakeTest(os.Args[2:])
	case "audit-verify":
		cmdAuditVerify(os.Args[2:])
	case "fwd", "tunnel":
		cmdFwd(os.Args[2:])
	case "setup":
		cmdSetup()
	case "doctor", "setup-check", "install-check":
		cmdDoctor(os.Args[2:])
	case "status":
		cmdStatus()
	case "list", "ls":
		cmdList()
	case "exec":
		cmdRun(os.Args[2:])
	case "bench":
		cmdBench(os.Args[2:])
	case "mcp":
		cmdMcp()
	case "mcp-config":
		cmdMCPConfig(os.Args[2:])
	case "mcp-install":
		cmdMCPInstall(os.Args[2:])
	case "fleet-state", "fleetstate":
		cmdFleetState(os.Args[2:])
	case "memory", "mem":
		cmdMemory(os.Args[2:])
	case "diff":
		cmdDiff(os.Args[2:])
	case "intent":
		cmdIntent(os.Args[2:])
	case "workflow", "wf":
		cmdWorkflow(os.Args[2:])
	case "agent":
		cmdAgent(os.Args[2:])
	case "version", "-v", "--version":
		if buildTime != "" {
			fmt.Printf("vssh %s (built %s)\n", version, buildTime)
		} else {
			fmt.Printf("vssh %s\n", version)
		}
	case "help", "-h", "--help":
		printUsage()
	default:
		// Assume it is a native vssh daemon target.
		cmdShell(os.Args[1:])
	}
}

func printUsage() {
	fmt.Print(`vssh - AI-native remote execution daemon (no sshd required)

Usage:
  vssh server [-p port]       Run vssh server daemon
  vssh shell <host[:port]>    Interactive shell (native protocol)
  vssh run <host> <cmd>       Execute command (native protocol)
  vssh run-many <hosts> <cmd> Execute command on comma-separated hosts
  vssh run-async <host> <cmd> [--wait <s>]  Run as a job; return inline if it finishes
                              within <s> seconds, else return a job id to poll
  vssh rpc <host> <method> [json]  Call native typed RPC
  vssh rpc-many <hosts> <method> [json]  Call RPC on comma-separated hosts
  vssh facts <host>           Return typed daemon facts as JSON
  vssh facts-many <hosts>     Return facts for comma-separated hosts
  vssh job-start <host> <cmd> Start a long-running daemon job
  vssh job-status <host> <id> Return job status
  vssh job-logs <host> <id>   Return job logs
  vssh job-cancel <host> <id> Cancel a running job
  vssh artifact-collect <host> <path> [max-bytes] Collect file/dir artifact metadata
  vssh put <file> <host:path> Upload file (native protocol)
  vssh get <host:path> <file> Download file (native protocol)
  vssh deploy-binary <local> <host> <remote-path> [--service <svc>] [--mode 0755] [--verify <cmd>]
                              Atomic+checksum upload, privileged install, optional
                              service restart, and verify — in one auditable call
  vssh <host[:port]>          Interactive shell (native protocol)
  vssh exec <host> <cmd>      Alias for native run
  vssh bench <host> [count]   Measure native exec latency

  vssh status                 Show connection status
  vssh doctor [--json]        Diagnose local binary, secret, peers, and MCP readiness
  vssh list                   List all peers
  vssh agent                  Run monitoring agent
  vssh mcp                    Run MCP JSON-RPC server
  vssh memory get [node]      Show fleet memory (role/services/tags/notes)
  vssh memory set <node> [--role=R] [--services=a,b] [--tags=x,y]
  vssh memory note <node> <text>  Append a timestamped event note
  vssh memory find [--role=R] [--tag=T] [--service=S] [query]  Filter/search nodes
  vssh memory auto-note <node> [output]  Extract notes from command output
  vssh memory ask <query>     Natural-language fleet memory query
  vssh intent "<request>" [--target <host>] [--run]  NL request → command plan (optionally run)
  vssh workflow list          List predefined multi-step workflows
  vssh workflow run <name> --target <host> [--param k=v] [--dry-run]  Run a workflow
  vssh workflow status <run-id>  Show a past workflow run
  vssh diff [--node <host>] [--last N] [--since 1h]  Human summary of audit-log changes

Examples:
  vssh server                    # Start server on :48291
  vssh shell web1                # Interactive shell to web1
	vssh run db1 "df -h"           # Run command on db1
  vssh run-many d1,v3 "uptime"    # Run in parallel
  vssh rpc d1 get_disk            # Typed disk facts
  vssh facts d1                   # Typed node facts
  vssh facts-many d1,v3           # Parallel typed node facts
  vssh job-start d1 "sleep 30"    # Start async job
  vssh bench db1 20              # Measure native exec latency
	vssh put file.tar web1:/tmp/   # Upload file
  vssh get web1:/var/log/x .     # Download file
  vssh intent "disk check" --target d1 --run   # NL → plan → run
  vssh workflow run service-restart --target d1 --param service=nginx
  vssh diff --node d1 --since 2h  # What changed on d1 in the last 2h

Environment:
  VSSH_PORT                Server port (default: 48291)
  VSSH_REQUIRE_TLS=1       Refuse non-TLS connections
  VSSH_NO_HOSTKEY_VERIFY=1 Opt out of host-identity verification (not recommended)

`)
}

const defaultPort = 48291

// getSecret is vestigial: vssh authenticates with per-node Ed25519 keys
// (VAUTH1), not a shared secret. Several server-package call sites still take a
// secret parameter, which the daemon ignores, so this always returns "".
func getSecret() string {
	return ""
}

func getPort() int {
	if p := os.Getenv("VSSH_PORT"); p != "" {
		if port, err := strconv.Atoi(p); err == nil {
			return port
		}
	}
	return defaultPort
}

// cmdRunBatch runs many commands (read from stdin, one per line) over a SINGLE
// multiplexed connection — one connect + one auth for the whole batch, the
// ssh-ControlMaster-style fast-repeat path. Outputs a JSON array of results.
// cmdPubkey prints this host's base64 Ed25519 public identity (creating it if needed).
func cmdPubkey() {
	_, pub := server.LoadOrCreateIdentity()
	fmt.Println(pub)
}

// cmdHandshakeTest performs the VAUTH1 Ed25519 challenge–response against a node and
// reports whether the node accepted this host's key. Proves the Phase-A security path
// end to end without touching the legacy auth used by the other verbs.
// With --tls the same exchange runs inside a TLS 1.3 channel (VTLS1): the
// daemon's Ed25519 key is pinned via ~/.vssh/known_hosts (TOFU on first
// contact) and the JSON result reports the negotiated TLS state.
func cmdHandshakeTest(args []string) {
	useTLS := false
	var rest []string
	for _, a := range args {
		if a == "--tls" {
			useTLS = true
			continue
		}
		rest = append(rest, a)
	}
	args = rest
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: vssh handshake-test [--tls] <host[:port]>")
		os.Exit(1)
	}
	host, port := parseHostPort(args[0])
	host = resolveReachableHost(host, port)
	priv, pub := server.LoadOrCreateIdentity()
	var conn net.Conn
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 8*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	tlsInfo := map[string]interface{}{}
	if useTLS {
		pinned := server.KnownHostPub(host)
		cfg, cerr := server.ClientTLSConfig(pinned)
		if cerr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", cerr)
			os.Exit(1)
		}
		tconn := tls.Client(conn, cfg)
		tconn.SetDeadline(time.Now().Add(8 * time.Second))
		if herr := tconn.Handshake(); herr != nil {
			fmt.Fprintf(os.Stderr, "Error: vtls handshake: %v\n", herr)
			os.Exit(1)
		}
		tconn.SetDeadline(time.Time{})
		cs := tconn.ConnectionState()
		serverPub := server.PeerPubB64(cs)
		if pinned == "" {
			server.RecordKnownHost(host, serverPub)
		}
		tlsInfo = map[string]interface{}{
			"tls":        true,
			"tls_alpn":   cs.NegotiatedProtocol,
			"server_key": serverPub,
			"pinned":     pinned != "",
		}
		conn = tconn
	}
	defer conn.Close()
	conn.Write([]byte("VAUTH1 " + pub + "\n"))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: no challenge: %v\n", err)
		os.Exit(1)
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "CHALLENGE ") {
		fmt.Fprintf(os.Stderr, "Error: expected CHALLENGE, got %q\n", line)
		os.Exit(1)
	}
	nonce, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "CHALLENGE "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: bad nonce: %v\n", err)
		os.Exit(1)
	}
	conn.Write([]byte("SIG " + server.SignChallenge(priv, nonce) + "\n"))
	resp, _ := reader.ReadString('\n')
	resp = strings.TrimRight(resp, "\r\n")
	out := map[string]interface{}{"host": args[0], "method": "VAUTH1-ed25519", "pubkey": pub, "result": resp}
	for k, v := range tlsInfo {
		out[k] = v
	}
	writeJSON(out)
	if resp != "AUTH_OK" {
		os.Exit(1)
	}
}

func splitTargets(s string) []string {
	parts := strings.Split(s, ",")
	targets := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			targets = append(targets, part)
		}
	}
	return targets
}

func defaultMaxParallelism() int {
	if value := strings.TrimSpace(os.Getenv("VSSH_MAX_PARALLELISM")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return 16
}

func normalizedMaxParallelism(maxParallelism, total int) int {
	if total <= 0 {
		return 1
	}
	if maxParallelism <= 0 {
		return total
	}
	if maxParallelism > total {
		return total
	}
	return maxParallelism
}

func writeJSON(value interface{}) {
	data, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(data))
}

func parseHostPort(s string) (string, int) {
	if idx := strings.LastIndex(s, ":"); idx != -1 {
		host := s[:idx]
		if port, err := strconv.Atoi(s[idx+1:]); err == nil {
			return host, port
		}
	}
	return s, getPort()
}

func resolveNativeHost(target string) string {
	connector, err := ssh.NewConnector("default")
	if err != nil {
		return target
	}
	host, err := connector.ResolveHost(target)
	if err != nil || host == "" {
		return target
	}
	server.SetExpectedHostKey(host, server.NodeKey(target))
	return host
}

// resolveReachableHost picks a candidate endpoint that actually accepts a TCP
// connection on the daemon port, instead of blindly dialing the preferred IP and
// stalling for the full timeout when that IP has moved (the stale-endpoint stall).
// It probes all candidates concurrently with a short deadline and returns the
// highest-preference reachable one, falling back to legacy resolution otherwise.
func resolveReachableHost(target string, port int) string {
	connector, err := ssh.NewConnector("default")
	if err != nil {
		return resolveNativeHost(target)
	}
	hosts, err := connector.CandidateHosts(target)
	if err != nil || len(hosts) == 0 {
		return resolveNativeHost(target)
	}
	if len(hosts) == 1 {
		server.SetExpectedHostKey(hosts[0], server.NodeKey(target))
		return hosts[0]
	}
	const perProbe = 1500 * time.Millisecond
	dial := func(h string) bool {
		conn, derr := net.DialTimeout("tcp", net.JoinHostPort(h, strconv.Itoa(port)), perProbe)
		if derr == nil {
			conn.Close()
		}
		return derr == nil
	}
	if best, ok := pickReachable(hosts, dial); ok {
		server.SetExpectedHostKey(best, server.NodeKey(target))
		return best
	}
	server.SetExpectedHostKey(hosts[0], server.NodeKey(target))
	return hosts[0]
}

// pickReachable probes hosts concurrently and returns the highest-preference
// (lowest-index) reachable host as soon as that is decided — host[i] wins once
// it is reachable AND every higher-preference host has already come back
// unreachable. It does NOT wait on slower lower-priority probes that can no
// longer change the answer; draining all of them was what stalled every
// vssh_rpc_call for the full per-probe timeout (~1.5s) whenever a node had a
// single dead secondary candidate. Returns ok=false when none are reachable.
func pickReachable(hosts []string, dial func(host string) bool) (string, bool) {
	if len(hosts) == 0 {
		return "", false
	}
	type probe struct {
		idx int
		ok  bool
	}
	ch := make(chan probe, len(hosts))
	for i, h := range hosts {
		go func(i int, h string) { ch <- probe{idx: i, ok: dial(h)} }(i, h)
	}
	const (
		pending = 0
		isUp    = 1
		isDown  = 2
	)
	state := make([]int, len(hosts))
	for range hosts {
		p := <-ch
		if p.ok {
			state[p.idx] = isUp
		} else {
			state[p.idx] = isDown
		}
		for i := 0; i < len(hosts); i++ {
			if state[i] == pending {
				break // can't decide while a higher-preference host is unknown
			}
			if state[i] == isUp {
				return hosts[i], true
			}
		}
	}
	return "", false
}

func cmdStatus() {
	connector, err := ssh.NewConnector("default")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(connector.Status())
}

func cmdList() {
	connector, err := ssh.NewConnector("default")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	peers := connector.ListPeers()
	if len(peers) == 0 {
		fmt.Println("No peers found")
		return
	}

	fmt.Println("Peers:")
	fmt.Println("───────────────────────────────────────────")
	fmt.Printf("%-12s %-15s %-15s %s\n", "NAME", "VPN IP", "PUBLIC IP", "LAN IP")
	fmt.Println("───────────────────────────────────────────")
	for _, p := range peers {
		fmt.Printf("%-12s %-15s %-15s %s\n", p.NodeName, p.VpnIP, p.PublicIP, p.LanIP)
	}
}

func cmdAgent(args []string) {
	cfg := agent.DefaultConfig()

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-l", "--log":
			if i+1 < len(args) {
				cfg.EventLogPath = args[i+1]
				i++
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down agent...")
		cancel()
	}()

	a := agent.New(cfg)
	if err := a.Run(ctx); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "Agent error: %v\n", err)
		os.Exit(1)
	}
}

func cmdSetup() {
	fmt.Println("vssh Setup")
	fmt.Println("──────────────────────────────────")

	home, _ := os.UserHomeDir()
	configDir := home + "/.vssh"

	// Create config directory
	if err := os.MkdirAll(configDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating config dir: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Config directory: %s\n", configDir)

	// Check SSH key
	sshKey := home + "/.ssh/id_ed25519"
	if _, err := os.Stat(sshKey); err == nil {
		fmt.Println("✓ SSH key found")
	} else {
		fmt.Println("○ No SSH key (will use password auth)")
	}

	fmt.Println()
	fmt.Println("Setup complete!")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  vssh server          # Start native daemon")
	fmt.Println("  vssh <hostname>      # Connect to native daemon")
	fmt.Println("  vssh list            # Show all peers")
}

// cmdAuditVerify validates the daemon audit log's hash chain: every record's
// "prev" field must equal the SHA-256 of the previous line. Any in-place edit,
// deletion, or reordering after a record was written breaks the chain. Trimming
// whole lines from the HEAD of the file is tolerated (rotation); anything else
// is reported. Exit 0 = verified, 1 = violations found.
func cmdAuditVerify(args []string) {
	path := server.AuditLogPath()
	if len(args) > 0 && args[0] != "" {
		path = args[0]
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	prev := ""
	total, chained := 0, 0
	violations := []map[string]interface{}{}
	first := true
	for i, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		total++
		var rec map[string]interface{}
		if jerr := json.Unmarshal([]byte(ln), &rec); jerr != nil {
			violations = append(violations, map[string]interface{}{"line": i + 1, "reason": "not valid json"})
		} else if p, ok := rec["prev"].(string); ok {
			chained++
			if !first && p != prev {
				violations = append(violations, map[string]interface{}{
					"line": i + 1, "reason": "chain break",
					"expected_prev": prev, "got_prev": p,
				})
			}
		}
		sum := sha256.Sum256([]byte(ln))
		prev = hex.EncodeToString(sum[:])
		first = false
	}
	verified := len(violations) == 0
	writeJSON(map[string]interface{}{
		"path":            path,
		"records":         total,
		"chained_records": chained,
		"verified":        verified,
		"violations":      violations,
	})
	if !verified {
		os.Exit(1)
	}
}
