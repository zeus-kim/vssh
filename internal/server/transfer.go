package server

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/md5"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrAuthFailed marks an authentication rejection by the daemon (both VAUTH1
// and the legacy token were refused), as opposed to a transport failure.
var ErrAuthFailed = errors.New("auth failed")

// transferBufSize is the copy-buffer size for bulk file streaming. 256 KiB lets
// the TLS layer batch many records per syscall, keeping high-bandwidth·high-
// latency links (e.g. cross-internet nodes) filled where the default 32 KiB
// io.Copy buffer leaves the pipe under-fed. Local links are already at line
// rate, so this only helps the long-fat paths. MultiWriter (used at every call
// site, for inline hashing) hides the ReadFrom/WriteTo fast paths, so the
// buffer size actually takes effect.
const transferBufSize = 256 * 1024

// dialAuth dials a daemon and authenticates with the VAUTH1 Ed25519
// challenge-response (per-node key + server nonce: no shared secret, no
// replay). The legacy shared-HMAC fallback was removed in 0.7.25 (design doc
// finding F3): the fleet is 100% strict VAUTH1, so the fallback was dead code
// whose only remaining caller would be an attacker harvesting replayable
// tokens by refusing VAUTH1. The secret parameter is kept for call-site
// compatibility and ignored.
//
// When VSSH_PREFER_TLS=1, the connection is first wrapped in TLS 1.3 (VTLS1,
// daemon key pinned via ~/.vssh/known_hosts, TOFU on first contact) and the
// same VAUTH1 exchange runs inside the channel. Daemons older than 0.7.25
// don't speak TLS, so the preference is opt-in until the fleet is upgraded
// (flips to default in 0.7.26 per docs/SECURITY_TRANSPORT_MIGRATION.md §5).
//
// It returns the authenticated connection plus the buffered reader used for
// the handshake; callers MUST read all subsequent daemon output from the
// returned reader, never the raw conn (the reader may already hold buffered
// bytes).
func dialAuth(host string, port int, secret string, dialTimeout time.Duration) (net.Conn, *bufio.Reader, error) {
	_ = secret // legacy parameter; shared-HMAC fallback removed (F3)
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	authDeadline := 5 * time.Second
	if dialTimeout < authDeadline {
		authDeadline = dialTimeout
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	priv, pub := LoadOrCreateIdentity()
	if priv == nil || pub == "" {
		return nil, nil, errors.New("no vssh identity")
	}

	requireTLS := envEnabled("VSSH_REQUIRE_TLS")
	// VSSH_NO_TLS=1 is a debugging escape hatch only; REQUIRE_TLS wins.
	tryTLS := os.Getenv("VSSH_NO_TLS") != "1" || requireTLS

	// 1) TLS 1.3 first (default since 0.7.26; §5 step 2 of the migration doc).
	if tryTLS {
		conn, err := net.DialTimeout("tcp", addr, dialTimeout)
		if err != nil {
			return nil, nil, err
		}
		pinned := KnownHostPub(host)
		cfg, cerr := ClientTLSConfig(pinned)
		if cerr != nil {
			conn.Close()
			return nil, nil, cerr
		}
		tconn := tls.Client(conn, cfg)
		tconn.SetDeadline(time.Now().Add(authDeadline))
		herr := tconn.Handshake()
		if herr == nil {
			tconn.SetDeadline(time.Time{})
			cs := tconn.ConnectionState()
			// A resumed TLS 1.3 session carries NO peer certificate: identity was
			// verified, pinned, and recorded on the original full handshake that
			// issued the ticket (and Go only offers a ticket back to the same
			// host it came from). Re-checking here would read an empty key —
			// false-mismatching a pinned host and clobbering known_hosts with "".
			// So the pin/TOFU logic runs on full handshakes only; VAUTH1 below
			// still authenticates every connection, resumed or not.
			if !cs.DidResume {
				// Host-identity verification (opt-in): refuse to proceed if the
				// reached daemon key differs from the one EXPECTED for the logical
				// target (set by the caller from the target's canonical config-IP
				// pin). Catches name->wrong-host misroutes (e.g. stale/colliding
				// Tailscale address) that an IP-keyed pin alone cannot.
				if os.Getenv("VSSH_NO_HOSTKEY_VERIFY") != "1" {
					if exp := expectedKeyFor(host); exp != "" {
						if got := PeerPubB64(cs); got != exp {
							tconn.Close()
							return nil, nil, fmt.Errorf("host identity mismatch for %s: reached daemon key %s, expected %s — refusing to run on the wrong host", host, got, exp)
						}
					}
				}
				if pinned == "" {
					// TOFU: record the daemon key on first contact.
					RecordKnownHost(host, PeerPubB64(cs))
				}
			}
			return vauth1(tconn, priv, pub, authDeadline)
		}
		tconn.Close()
		// A pinned-key mismatch is a security failure, never a compat
		// fallback: an attacker must not be able to force plaintext by
		// presenting a wrong certificate.
		if pinned != "" && strings.Contains(herr.Error(), "known_hosts") {
			return nil, nil, fmt.Errorf("vtls handshake: %w", herr)
		}
		if requireTLS {
			return nil, nil, fmt.Errorf("vtls handshake (VSSH_REQUIRE_TLS=1, no plaintext fallback): %w", herr)
		}
		// Pre-0.7.25 daemon (or non-TLS listener): fall back to plaintext
		// VAUTH1 on a fresh connection, loudly — these messages are the
		// fleet-upgrade TODO list and disappear once REQUIRE_TLS flips.
		fmt.Fprintf(os.Stderr, "vssh: WARNING: %s does not speak TLS (pre-0.7.25 daemon?) — falling back to PLAINTEXT VAUTH1 (%v)\n", addr, herr)
	}

	// 2) Plaintext VAUTH1 (legacy daemons; removed when REQUIRE_TLS flips fleet-wide).
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, nil, err
	}
	return vauth1(conn, priv, pub, authDeadline)
}

// vauth1 runs the Ed25519 challenge–response over an established (possibly
// TLS-wrapped) connection and hands back the authenticated conn + reader.
func vauth1(conn net.Conn, priv ed25519.PrivateKey, pub string, authDeadline time.Duration) (net.Conn, *bufio.Reader, error) {
	reader := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(authDeadline))
	conn.Write([]byte("VAUTH1 " + pub + "\n"))
	line, rerr := reader.ReadString('\n')
	if rerr == nil && strings.HasPrefix(line, "CHALLENGE ") {
		nonce, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(line, "CHALLENGE ")))
		if derr == nil && len(nonce) > 0 {
			conn.Write([]byte("SIG " + SignChallenge(priv, nonce) + "\n"))
			if ok, oerr := reader.ReadString('\n'); oerr == nil && strings.HasPrefix(ok, "AUTH_OK") {
				conn.SetReadDeadline(time.Time{})
				return conn, reader, nil
			}
		}
	}
	conn.Close()
	return nil, nil, ErrAuthFailed
}

// SendFile sends a file to remote host
func SendFile(host string, port int, secret, localPath, remotePath string) error {
	// Open local file
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return err
	}

	// Connect + authenticate (VAUTH1 preferred, legacy HMAC fallback).
	conn, reader, err := dialAuth(host, port, secret, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Send transfer command: PUT <size> <path>
	if remotePath == "" {
		remotePath = filepath.Base(localPath)
	}
	cmd := fmt.Sprintf("PUT %d %s\n", stat.Size(), remotePath)
	conn.Write([]byte(cmd))

	// Read ready
	resp, err := reader.ReadString('\n')
	if err != nil || resp[:5] != "READY" {
		return fmt.Errorf("server not ready: %s", resp)
	}

	// Send file data, hashing as we go for end-to-end integrity.
	h := md5.New()
	n, err := io.CopyBuffer(io.MultiWriter(conn, h), f, make([]byte, transferBufSize))
	if err != nil {
		return err
	}
	localSum := hex.EncodeToString(h.Sum(nil))

	// Read confirmation: "OK <bytes> [md5]"
	resp, err = reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("no confirmation")
	}
	if len(resp) < 2 || resp[:2] != "OK" {
		return fmt.Errorf("transfer failed: %s", strings.TrimSpace(resp))
	}
	// Verify the checksum when the server reports one (newer daemons do);
	// silently skip against older daemons that only send "OK <bytes>".
	if fields := strings.Fields(resp); len(fields) >= 3 {
		if remoteSum := fields[2]; remoteSum != localSum {
			return fmt.Errorf("checksum mismatch: local=%s remote=%s", localSum, remoteSum)
		}
	}

	fmt.Printf("Sent %d bytes\n", n)
	return nil
}

// RecvFile receives a file from remote host
func RecvFile(host string, port int, secret, remotePath, localPath string) error {
	conn, reader, err := dialAuth(host, port, secret, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Send GET command
	cmd := fmt.Sprintf("GET %s\n", remotePath)
	conn.Write([]byte(cmd))

	// Read size
	resp, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	var size int64
	if _, err := fmt.Sscanf(resp, "SIZE %d", &size); err != nil {
		return fmt.Errorf("invalid response: %s", strings.TrimSpace(resp))
	}
	// "SIZE <n> [md5]" — capture the checksum when present (newer daemons).
	var remoteSum string
	if fields := strings.Fields(resp); len(fields) >= 3 {
		remoteSum = fields[2]
	}

	if localPath == "" {
		localPath = filepath.Base(remotePath)
	}
	// Stage to a temp file in the same dir, then atomically rename — a partial
	// or corrupt transfer never leaves a half-written file at the target path.
	tmp := fmt.Sprintf("%s.vssh.tmp.%d", localPath, os.Getpid())
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	// Receive data (use reader to handle buffered data), hashing as we go.
	h := md5.New()
	n, err := io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(reader, size), make([]byte, transferBufSize))
	if err == nil && n < size {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Sync()
	f.Close()
	// Newer daemons send the MD5 as a trailing line after the data instead of on
	// the SIZE line. Read it when we didn't already capture one.
	if remoteSum == "" {
		if tline, _ := reader.ReadString('\n'); tline != "" {
			if t := strings.TrimSpace(tline); len(t) == 32 {
				remoteSum = t
			}
		}
	}
	if remoteSum != "" {
		if localSum := hex.EncodeToString(h.Sum(nil)); localSum != remoteSum {
			os.Remove(tmp)
			return fmt.Errorf("checksum mismatch: remote=%s local=%s", remoteSum, localSum)
		}
	}
	if err := os.Rename(tmp, localPath); err != nil {
		os.Remove(tmp)
		return err
	}

	fmt.Printf("Received %d bytes\n", n)
	return nil
}

// HandleTransfer handles file transfer on server side
func HandleTransfer(conn net.Conn, cmd string) {
	if len(cmd) < 4 {
		conn.Write([]byte("ERROR invalid command\n"))
		return
	}

	if strings.HasPrefix(cmd, "EXEJ ") {
		encoded := strings.TrimSpace(strings.TrimPrefix(cmd, "EXEJ "))
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			writeExecJSON(conn, ExecCommandResult{
				Success:  false,
				ExitCode: -1,
				Error:    "invalid base64 command",
			})
			return
		}
		if d := enforceExecPolicy(conn, string(raw)); d != nil {
			writeExecJSON(conn, *d)
			return
		}
		result := ExecLocalStructured(string(raw))
		auditLog(conn, string(raw), result)
		writeExecJSON(conn, result)
		return
	}

	switch cmd[:3] {
	case "PUT":
		var size int64
		var path string
		fmt.Sscanf(cmd, "PUT %d %s", &size, &path)
		transferUser := effectiveTransferUser()

		// Expand ~ to the effective transfer user. When vsshd runs as root,
		// this keeps Runtime staging files usable by the same non-root user
		// that command execution selects.
		path = expandTransferPath(path, transferUser)
		if policyPathDenied(conn, path) {
			return
		}

		// Create directory if needed
		dir := filepath.Dir(path)
		os.MkdirAll(dir, 0755)
		chownToTransferUser(dir, transferUser)

		// Stage into a temp file in the SAME directory, then atomically rename.
		// This is ETXTBSY-safe (never truncates a running binary — rename swaps
		// the directory entry, the busy inode lingers until its process exits)
		// and gives concurrent readers an all-or-nothing view. We also hash the
		// bytes as they land so the client can verify integrity.
		tmp := fmt.Sprintf("%s.vssh.tmp.%d", path, os.Getpid())
		f, err := os.Create(tmp)
		if err != nil {
			conn.Write([]byte(fmt.Sprintf("ERROR %v\n", err)))
			return
		}

		conn.Write([]byte("READY\n"))

		h := md5.New()
		n, err := io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(conn, size), make([]byte, transferBufSize))
		if err == nil && n < size {
			err = io.ErrUnexpectedEOF
		}
		if err != nil {
			f.Close()
			os.Remove(tmp)
			conn.Write([]byte(fmt.Sprintf("ERROR %v\n", err)))
			return
		}
		f.Sync()
		f.Close()
		_ = os.Chmod(tmp, 0644)
		if err := os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			conn.Write([]byte(fmt.Sprintf("ERROR %v\n", err)))
			return
		}
		chownToTransferUser(path, transferUser)
		// "OK <bytes> <md5>" — legacy clients only check the "OK" prefix, new
		// clients verify the checksum. Backward compatible.
		conn.Write([]byte(fmt.Sprintf("OK %d %s\n", n, hex.EncodeToString(h.Sum(nil)))))

	case "GET":
		path := cmd[4:]
		path = path[:len(path)-1] // remove newline
		transferUser := effectiveTransferUser()

		// Expand ~ consistently with PUT.
		path = expandTransferPath(path, transferUser)
		if policyPathDenied(conn, path) {
			return
		}

		f, err := os.Open(path)
		if err != nil {
			conn.Write([]byte(fmt.Sprintf("ERROR %v\n", err)))
			return
		}
		defer f.Close()

		stat, _ := f.Stat()
		// Single pass: announce size, then stream the file once while hashing, and
		// emit the MD5 as a trailing line after the data. New clients verify the
		// trailer; older clients read exactly <size> bytes and ignore it (backward
		// compatible). This removes the double read (separate checksum + send pass)
		// that doubled GET disk I/O in 0.7.7–0.7.13.
		conn.Write([]byte(fmt.Sprintf("SIZE %d\n", stat.Size())))
		h := md5.New()
		io.CopyBuffer(io.MultiWriter(conn, h), f, make([]byte, transferBufSize))
		conn.Write([]byte(hex.EncodeToString(h.Sum(nil)) + "\n"))

	case "EXE": // EXEC
		cmdStr := cmd[5:]
		if len(cmdStr) > 0 && cmdStr[len(cmdStr)-1] == '\n' {
			cmdStr = cmdStr[:len(cmdStr)-1]
		}

		if d := enforceExecPolicy(conn, cmdStr); d != nil {
			conn.Write([]byte("ERROR: " + d.Error + "\n"))
			return
		}
		// Execute command
		out, err := execShell(cmdStr)
		if err != nil {
			conn.Write([]byte(fmt.Sprintf("ERROR: %v\n%s", err, out)))
		} else {
			conn.Write(out)
		}
	}
}

// ExecCommandResult is the AI/runtime-oriented native execution envelope.
type ExecCommandResult struct {
	Success    bool   `json:"success"`
	Command    string `json:"command,omitempty"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	// ErrorCode is a stable, machine-branchable classification of a failure so an
	// agent can decide retry-vs-fix without parsing the free-text Error. Empty on
	// success. Retryable marks transient failures (timeout/unreachable) worth a retry.
	ErrorCode string `json:"error_code,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

// Stable error codes for ExecCommandResult.ErrorCode.
const (
	ErrCodeAuthFailed  = "auth_failed"         // secret rejected by daemon
	ErrCodeUnreachable = "unreachable"         // could not connect / no route
	ErrCodeTimeout     = "timeout"             // dial or read deadline exceeded
	ErrCodeBadResponse = "bad_response"        // malformed/unparseable daemon reply
	ErrCodeRemoteExit  = "remote_exit_nonzero" // command ran but exited non-zero
)

// classifyDialError maps a Go network/transport error to a stable code and
// whether a retry is worthwhile. Transport failures are treated as retryable.
func classifyDialError(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "i/o timeout"), strings.Contains(s, "deadline exceeded"):
		return ErrCodeTimeout, true
	case strings.Contains(s, "no such host"), strings.Contains(s, "refused"),
		strings.Contains(s, "no route"), strings.Contains(s, "unreachable"),
		strings.Contains(s, "connection reset"), strings.Contains(s, "broken pipe"):
		return ErrCodeUnreachable, true
	default:
		return ErrCodeUnreachable, true
	}
}

var (
	auditPathOnce  sync.Once
	auditPathCache string
)

var (
	auditChainMu   sync.Mutex
	auditChainPrev string
	auditChainInit bool
)

// ExecCommandStructured executes a command through the native daemon and returns
// a machine-readable execution envelope.
func ExecCommandStructured(host string, port int, secret, command string) (ExecCommandResult, error) {
	return ExecCommandStructuredTimeout(host, port, secret, command, 30*time.Second)
}

// ExecCommandStructuredTimeout executes a command with a caller-supplied network
// deadline. Short deadlines are useful for latency probes and fleet fan-out.
func ExecCommandStructuredTimeout(host string, port int, secret, command string, deadline time.Duration) (ExecCommandResult, error) {
	if deadline <= 0 {
		deadline = 30 * time.Second
	}

	conn, reader, err := dialAuth(host, port, secret, deadline)
	if err != nil {
		// Distinguish "rejected" (AUTH_FAILED on both methods) from a transport
		// failure — the agent's next action differs.
		if errors.Is(err, ErrAuthFailed) {
			return ExecCommandResult{Success: false, Command: command, ExitCode: -1, Error: err.Error(), ErrorCode: ErrCodeAuthFailed, Retryable: false}, err
		}
		code, retry := classifyDialError(err)
		return ExecCommandResult{Success: false, Command: command, ExitCode: -1, Error: err.Error(), ErrorCode: code, Retryable: retry}, err
	}
	defer conn.Close()

	encoded := base64.StdEncoding.EncodeToString([]byte(command))
	conn.Write([]byte(fmt.Sprintf("EXEJ %s\n", encoded)))

	conn.SetReadDeadline(time.Now().Add(deadline))
	line, err := reader.ReadBytes('\n')
	if err != nil {
		code, retry := classifyDialError(err)
		return ExecCommandResult{Success: false, Command: command, ExitCode: -1, Error: err.Error(), ErrorCode: code, Retryable: retry}, err
	}

	var result ExecCommandResult
	if err := json.Unmarshal(line, &result); err != nil {
		return ExecCommandResult{Success: false, Command: command, ExitCode: -1, Error: err.Error(), ErrorCode: ErrCodeBadResponse, Retryable: false}, err
	}
	if result.Command == "" {
		result.Command = command
	}
	// Classify a non-zero remote exit so the daemon's plain envelope still carries
	// a code the agent can branch on.
	if !result.Success && result.ErrorCode == "" && result.ExitCode > 0 {
		result.ErrorCode = ErrCodeRemoteExit
	}
	return result, nil
}

// CallRPC invokes a typed RPC method through the native daemon.
func CallRPC(host string, port int, secret, method string, params map[string]interface{}, deadline time.Duration) (RPCResponse, error) {
	if deadline <= 0 {
		deadline = 30 * time.Second
	}
	var zero RPCResponse

	conn, reader, err := dialAuth(host, port, secret, deadline)
	if err != nil {
		return zero, err
	}
	defer conn.Close()

	paramsJSON, _ := json.Marshal(params)
	conn.Write([]byte(fmt.Sprintf("RPC %s %d\n", method, len(paramsJSON))))
	if len(paramsJSON) > 0 {
		conn.Write(paramsJSON)
	}

	conn.SetReadDeadline(time.Now().Add(deadline))
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return zero, err
	}

	var resp RPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return zero, err
	}
	return resp, nil
}

// GetInfo returns native daemon facts through the INFO command.
func GetInfo(host string, port int, secret string, deadline time.Duration) (*ServerInfo, error) {
	if deadline <= 0 {
		deadline = 30 * time.Second
	}

	conn, reader, err := dialAuth(host, port, secret, deadline)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	conn.Write([]byte("INFO\n"))
	conn.SetReadDeadline(time.Now().Add(deadline))
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	var info ServerInfo
	if err := json.Unmarshal(bytes.TrimSpace(data), &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// --- host-identity verification (opt-in: VSSH_VERIFY_HOST_IDENTITY=1) ---
// Mitigates the misroute class where a logical node name resolves to the wrong
// machine (e.g. a stale/colliding Tailscale address) and the IP-keyed pin still
// matches that wrong host. The caller records, keyed by the address it is about
// to dial, the TLS key it EXPECTS for the logical target (the pin of the target's
// canonical config IP). dialAuth then hard-fails if the reached daemon key differs.

var expectedHostKey sync.Map // dialAddr(host) -> expected TLS pubkey(b64)

// SetExpectedHostKey records the expected daemon key for an address about to be
// dialed. Empty key clears any prior expectation (no check).
func SetExpectedHostKey(host, key string) {
	if host == "" {
		return
	}
	if key == "" {
		expectedHostKey.Delete(host)
		return
	}
	expectedHostKey.Store(host, key)
}

func expectedKeyFor(host string) string {
	if v, ok := expectedHostKey.Load(host); ok {
		return v.(string)
	}
	return ""
}
