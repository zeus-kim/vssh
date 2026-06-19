package server

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// ProtoVersion is the native wire-protocol version the daemon speaks. It is
// surfaced in error replies so a client/agent can detect a version skew instead
// of guessing why a verb was rejected.
const ProtoVersion = "1"

func isUpperASCII(b byte) bool { return b >= 'A' && b <= 'Z' }

// Server runs vssh server
type Server struct {
	Port        int
	Secret      string
	activeConns int64
}

// NewServer creates a new server
func NewServer(port int, secret string) *Server {
	return &Server{Port: port, Secret: secret}
}

// Run starts the server
func (s *Server) Run() error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		return err
	}
	defer listener.Close()

	fmt.Printf("vssh server listening on :%d\n", s.Port)
	daemonLog("listen", map[string]interface{}{"port": s.Port, "version": DaemonVersion})
	go s.healthLoop()

	var backoff time.Duration
	for {
		conn, err := listener.Accept()
		if err != nil {
			// Never busy-spin on a persistent accept error (e.g. fd exhaustion):
			// back off (capped) and keep the daemon alive.
			if backoff == 0 {
				backoff = 5 * time.Millisecond
			} else if backoff < time.Second {
				backoff *= 2
			}
			daemonLog("accept_error", map[string]interface{}{"error": err.Error(), "backoff_ms": backoff.Milliseconds()})
			time.Sleep(backoff)
			continue
		}
		backoff = 0
		atomic.AddInt64(&s.activeConns, 1)
		go func(c net.Conn) {
			defer atomic.AddInt64(&s.activeConns, -1)
			s.handleConnection(c)
		}(conn)
	}
}

// healthLoop periodically records daemon liveness (active connections + live
// goroutines) so a wedged or leaking daemon is visible in daemon.log.
func (s *Server) healthLoop() {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for range t.C {
		daemonLog("health", map[string]interface{}{
			"active_conns": atomic.LoadInt64(&s.activeConns),
			"goroutines":   runtime.NumGoroutine(),
		})
	}
}

// capAllowed reports whether a capability is granted (nil set = unrestricted).
func capAllowed(caps map[string]bool, c string) bool {
	return caps == nil || caps[c]
}

// denyCap replies with a typed capability error so an agent can branch on it.
func denyCap(conn net.Conn, required string) {
	payload, _ := json.Marshal(map[string]interface{}{
		"success":             false,
		"error":               "capability denied: key lacks '" + required + "'",
		"error_code":          "capability_denied",
		"required_capability": required,
		"proto_version":       ProtoVersion,
	})
	conn.Write(payload)
	conn.Write([]byte("\n"))
}

// peekedConn lets the TLS server replay the byte(s) we sniffed off the raw
// connection before deciding the peer speaks TLS.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (p *peekedConn) Read(b []byte) (int, error) { return p.r.Read(b) }

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	// VTLS1 sniff (docs/SECURITY_TRANSPORT_MIGRATION.md §4.2): a TLS
	// ClientHello starts with record byte 0x16; every legacy first line starts
	// with an ASCII letter/digit. On 0x16 the connection is wrapped in TLS 1.3
	// and the unchanged line protocol (auth line first) runs inside it.
	raw := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	first, perr := raw.Peek(1)
	conn.SetReadDeadline(time.Time{})
	if perr != nil {
		return
	}
	var tlsState *tls.ConnectionState
	if first[0] == 0x16 {
		cfg, cerr := ServerTLSConfig()
		if cerr != nil {
			return
		}
		tconn := tls.Server(&peekedConn{Conn: conn, r: raw}, cfg)
		tconn.SetDeadline(time.Now().Add(10 * time.Second))
		hsStart := time.Now()
		if herr := tconn.Handshake(); herr != nil {
			daemonLog("tls_handshake_fail", map[string]interface{}{"remote": conn.RemoteAddr().String(), "error": herr.Error()})
			return
		}
		if d := time.Since(hsStart); d > 2*time.Second {
			daemonLog("slow_tls_handshake", map[string]interface{}{"remote": conn.RemoteAddr().String(), "ms": d.Milliseconds()})
		}
		tconn.SetDeadline(time.Time{})
		cs := tconn.ConnectionState()
		tlsState = &cs
		defer tconn.Close()
		conn = tconn
		raw = bufio.NewReader(tconn)
	} else if envEnabled("VSSH_REQUIRE_TLS") {
		// Plaintext refused outright when the kill-switch is set.
		daemonLog("plaintext_refused", map[string]interface{}{"remote": conn.RemoteAddr().String()})
		conn.Write([]byte("AUTH_FAILED\n"))
		return
	}
	if tlsState != nil {
		setConnTransport(conn, "tls")
	} else {
		setConnTransport(conn, "plain")
	}

	// Read auth line
	reader := raw
	authLine, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	authLine = strings.TrimRight(authLine, "\r\n")

	// Authenticate. Only VAUTH1 — per-node Ed25519 challenge–response (no shared
	// secret, no replay) — is accepted. The legacy shared-secret HMAC path was
	// removed (P4): there is no fleet-wide shared secret to leak or misuse.
	// caps is the capability set granted to the authenticated key; nil means a
	// key without a caps= tag (unrestricted).
	var caps map[string]bool
	if strings.HasPrefix(authLine, "VAUTH1 ") {
		pubB64 := strings.TrimSpace(strings.TrimPrefix(authLine, "VAUTH1 "))
		// Inside TLS, a presented client certificate must carry the same key
		// the VAUTH1 line claims (no identity confusion between layers).
		if tlsState != nil {
			if certPub := PeerPubB64(*tlsState); certPub != "" && certPub != pubB64 {
				conn.Write([]byte("AUTH_FAILED\n"))
				return
			}
		}
		keyCaps, authorized := KeyCapabilities(pubB64)
		if !authorized {
			conn.Write([]byte("AUTH_FAILED\n"))
			return
		}
		caps = keyCaps
		nonce := NewNonce()
		conn.Write([]byte("CHALLENGE " + base64.StdEncoding.EncodeToString(nonce) + "\n"))
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		sigLine, serr := reader.ReadString('\n')
		conn.SetReadDeadline(time.Time{})
		if serr != nil {
			return
		}
		sigLine = strings.TrimRight(sigLine, "\r\n")
		if !strings.HasPrefix(sigLine, "SIG ") || !VerifyChallenge(pubB64, nonce, strings.TrimPrefix(sigLine, "SIG ")) {
			conn.Write([]byte("AUTH_FAILED\n"))
			return
		}
		setConnIdentity(conn, pubB64, KeyName(pubB64))
		conn.Write([]byte("AUTH_OK\n"))
	} else {
		// Legacy shared-secret (HMAC) authentication was removed (P4). The daemon
		// accepts only per-node Ed25519 VAUTH1; any other auth line is rejected.
		conn.Write([]byte("AUTH_FAILED\n"))
		return
	}

	defer clearConnIdentity(conn)

	// Check for transfer/exec command (peek first bytes)
	// Wait longer for network latency
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	peek, err := reader.Peek(4)
	conn.SetReadDeadline(time.Time{})
	if err == nil {
		prefix := string(peek[:4])
		prefix3 := prefix[:3]

		// Basic commands
		if prefix3 == "PUT" || prefix3 == "GET" || prefix3 == "EXE" {
			cmd, _ := reader.ReadString('\n')
			need := "file"
			if prefix3 == "EXE" {
				need = "exec"
			}
			if !capAllowed(caps, need) {
				denyCap(conn, need)
				return
			}
			// Check for PUTZ (compressed)
			if len(cmd) >= 4 && cmd[:4] == "PUTZ" {
				HandlePutZ(conn, cmd)
				return
			}
			HandleTransfer(conn, cmd)
			return
		}
		if prefix3 == "SYN" { // SYNC
			cmd, _ := reader.ReadString('\n')
			if !capAllowed(caps, "file") {
				denyCap(conn, "file")
				return
			}
			HandleSync(conn, cmd)
			return
		}
		if prefix3 == "REL" { // RELAY
			cmd, _ := reader.ReadString('\n')
			if !capAllowed(caps, "exec") {
				denyCap(conn, "exec")
				return
			}
			HandleRelay(conn, cmd)
			return
		}

		// Advanced commands
		if prefix == "RESU" { // RESUME
			cmd, _ := reader.ReadString('\n')
			if !capAllowed(caps, "file") {
				denyCap(conn, "file")
				return
			}
			HandleResume(conn, cmd)
			return
		}
		if prefix == "MPUT" { // Multiplexed put
			cmd, _ := reader.ReadString('\n')
			if !capAllowed(caps, "file") {
				denyCap(conn, "file")
				return
			}
			HandleMPut(conn, cmd)
			return
		}
		if prefix == "GETM" { // Parallel download init
			cmd, _ := reader.ReadString('\n')
			if !capAllowed(caps, "file") {
				denyCap(conn, "file")
				return
			}
			HandleGetM(conn, cmd)
			return
		}
		if prefix == "GETC" { // Parallel download chunk
			cmd, _ := reader.ReadString('\n')
			if !capAllowed(caps, "file") {
				denyCap(conn, "file")
				return
			}
			HandleGetC(conn, cmd)
			return
		}
		if prefix == "PIPE" { // PIPE_UP or PIPE_DOWN
			cmd, _ := reader.ReadString('\n')
			if !capAllowed(caps, "file") {
				denyCap(conn, "file")
				return
			}
			if len(cmd) >= 8 && cmd[:7] == "PIPE_UP" {
				HandlePipeUp(conn, cmd, reader)
			} else {
				HandlePipeDown(conn, cmd)
			}
			return
		}
		if prefix3 == "RPC" { // RPC call
			cmd, _ := reader.ReadString('\n')
			if !capAllowed(caps, "rpc") {
				denyCap(conn, "rpc")
				return
			}
			HandleRPCCommand(conn, cmd, reader)
			return
		}
		if prefix == "INFO" { // Server info
			reader.ReadString('\n') // consume line
			if !capAllowed(caps, "rpc") {
				denyCap(conn, "rpc")
				return
			}
			conn.Write(HandleInfo())
			conn.Write([]byte("\n"))
			return
		}
		if prefix3 == "FWD" { // TCP port-forward (ssh -L replacement)
			cmd, _ := reader.ReadString('\n')
			if !capAllowed(caps, "forward") {
				denyCap(conn, "forward")
				return
			}
			HandleForward(conn, reader, cmd)
			return
		}
		if prefix == "RFWD" { // reverse tunnel control (ssh -R)
			cmd, _ := reader.ReadString('\n')
			if !capAllowed(caps, "forward") {
				denyCap(conn, "forward")
				return
			}
			HandleReverseForward(conn, reader, cmd)
			return
		}
		if prefix == "RDAT" { // reverse tunnel data channel
			cmd, _ := reader.ReadString('\n')
			if !capAllowed(caps, "forward") {
				denyCap(conn, "forward")
				return
			}
			HandleReverseData(conn, reader, cmd)
			return
		}
		if prefix == "FMUX" { // many tunneled streams over ONE authenticated connection
			if !capAllowed(caps, "forward") {
				denyCap(conn, "forward")
				return
			}
			HandleForwardMux(conn, reader)
			return
		}
		if prefix3 == "MUX" { // multiplexed session (many commands, one connection)
			if !capAllowed(caps, "exec") {
				denyCap(conn, "exec")
				return
			}
			HandleMux(conn, reader)
			return
		}

		// Unknown but protocol-shaped verb: two or more leading uppercase ASCII
		// letters that matched none of the handlers above means a client sent a
		// typo'd, removed, or newer verb. Return a typed error (with the daemon's
		// protocol version) instead of silently dropping into an interactive PTY,
		// which would hang an automated caller. Real interactive shells send an
		// ESC-prefixed window-size sequence first, never an uppercase token, so
		// this guard never affects them.
		if isUpperASCII(peek[0]) && isUpperASCII(peek[1]) {
			line, _ := reader.ReadString('\n')
			verb := strings.TrimSpace(line)
			if i := strings.IndexByte(verb, ' '); i >= 0 {
				verb = verb[:i]
			}
			payload, _ := json.Marshal(map[string]interface{}{
				"success":       false,
				"error":         fmt.Sprintf("unsupported verb: %q", verb),
				"error_code":    "unsupported_method",
				"proto_version": ProtoVersion,
			})
			conn.Write(payload)
			conn.Write([]byte("\n"))
			return
		}
	}

	// Interactive PTY fallthrough requires the "shell" capability.
	if !capAllowed(caps, "shell") {
		denyCap(conn, "shell")
		return
	}

	// Get user shell
	shell := "/bin/bash"
	if u, err := user.Current(); err == nil {
		if sh := lookupShell(u.Uid); sh != "" {
			shell = sh
		}
	}

	// Open PTY
	pty, tty, err := openPty()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pty open failed: %v\n", err)
		return
	}
	defer pty.Close()
	defer tty.Close()

	// Start shell
	cmd := exec.Command(shell, "-l")
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "shell start failed: %v\n", err)
		return
	}

	// Close tty in parent
	tty.Close()

	// Bidirectional copy
	done := make(chan struct{})

	// PTY -> conn
	go func() {
		io.Copy(conn, pty)
		done <- struct{}{}
	}()

	// conn -> PTY
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				break
			}
			data := buf[:n]
			// Check for resize: \x1b[8;<rows>;<cols>t
			if len(data) >= 8 && data[0] == 0x1b && data[1] == '[' && data[2] == '8' && data[3] == ';' {
				var rows, cols int
				fmt.Sscanf(string(data), "\x1b[8;%d;%dt", &rows, &cols)
				if rows > 0 && cols > 0 {
					setWinsize(pty, rows, cols)
					continue
				}
			}
			pty.Write(data)
		}
		done <- struct{}{}
	}()

	cmd.Wait()
	conn.Close()
	<-done
}

func lookupShell(uid string) string {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := splitColon(line)
		if len(parts) >= 7 && parts[2] == uid {
			return parts[6]
		}
	}
	return ""
}

func splitColon(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}
