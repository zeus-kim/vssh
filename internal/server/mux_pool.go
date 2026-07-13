package server

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// persistentExec, when enabled by a long-lived process (the `vssh mcp` server),
// makes ExecCommandStructuredTimeout reuse ONE authenticated MUX session per
// host instead of dialing + TLS + VAUTH on every call. The daemon side
// (HandleMux) has shipped fleet-wide since 0.7.x, so this needs no daemon
// change: the first exec to a host pays the full connect+auth, and every later
// exec is a single round-trip over the open session — the ssh-ControlMaster win
// that makes an AI agent's tool loop fast. It falls back to a one-shot
// connection if the daemon doesn't speak MUX or a pooled session breaks.
var persistentExec atomic.Bool

// EnablePersistentExec turns on the process-wide MUX session pool.
func EnablePersistentExec() { persistentExec.Store(true) }

// PersistentExecEnabled reports whether the pool is active (for diagnostics).
func PersistentExecEnabled() bool { return persistentExec.Load() }

// muxSession is one authenticated, reusable MUX connection to a host. Its mutex
// serializes commands (a MUX connection is a single request/response stream);
// fan-out across DIFFERENT hosts uses different sessions and stays parallel.
type muxSession struct {
	mu     sync.Mutex
	conn   net.Conn
	reader *bufio.Reader
	noMux  bool // daemon answered without MUX_OK — stop probing, always one-shot
}

// safeToRetry reports whether a failed round-trip on a REUSED session can be
// re-run without risking a double execution. Only a connection that was closed
// BEFORE the daemon processed the command (idle-timeout: EOF / reset / broken
// pipe / closed) is safe. A read TIMEOUT means the command may have executed and
// only its reply was lost — re-running it could restart a service or rewrite a
// file twice, so it is never retried.
func safeToRetry(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if strings.Contains(s, "timeout") || strings.Contains(s, "deadline exceeded") {
		return false
	}
	return errors.Is(err, io.EOF) ||
		strings.Contains(s, "EOF") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "use of closed")
}

var (
	muxPoolMu sync.Mutex
	muxPool   = map[string]*muxSession{}
)

func muxSessionFor(host string, port int) *muxSession {
	key := host + ":" + strconv.Itoa(port)
	muxPoolMu.Lock()
	defer muxPoolMu.Unlock()
	s := muxPool[key]
	if s == nil {
		s = &muxSession{}
		muxPool[key] = s
	}
	return s
}

// establish opens and authenticates a fresh MUX session. Caller holds s.mu.
// Returns hasMux=false (err=nil) when the daemon simply doesn't speak MUX, so
// the caller can fall back to a one-shot connection.
func (s *muxSession) establish(host string, port int, secret string, deadline time.Duration) (hasMux bool, err error) {
	conn, reader, derr := dialAuth(host, port, secret, deadline)
	if derr != nil {
		return false, derr
	}
	conn.Write([]byte("MUX\n"))
	conn.SetReadDeadline(time.Now().Add(deadline))
	line, rerr := reader.ReadString('\n')
	conn.SetReadDeadline(time.Time{})
	if rerr != nil || !strings.HasPrefix(line, "MUX_OK") {
		conn.Close()
		// A clean "no MUX_OK" reply (rerr==nil) means the daemon predates MUX;
		// remember it so we don't pay a probe dial on every future exec.
		if rerr == nil {
			s.noMux = true
		}
		return false, nil
	}
	s.conn = conn
	s.reader = reader
	return true, nil
}

// closeLocked drops the session (best-effort QUIT). Caller holds s.mu.
func (s *muxSession) closeLocked() {
	if s.conn != nil {
		s.conn.Write([]byte("QUIT\n"))
		s.conn.Close()
		s.conn = nil
		s.reader = nil
	}
}

// roundtrip sends one EXEJ and reads its JSON reply. Caller holds s.mu.
func (s *muxSession) roundtrip(command string, deadline time.Duration) (ExecCommandResult, error) {
	enc := base64.StdEncoding.EncodeToString([]byte(command))
	if _, err := s.conn.Write([]byte("EXEJ " + enc + "\n")); err != nil {
		return ExecCommandResult{}, err
	}
	s.conn.SetReadDeadline(time.Now().Add(deadline))
	line, err := s.reader.ReadBytes('\n')
	s.conn.SetReadDeadline(time.Time{})
	if err != nil {
		return ExecCommandResult{}, err
	}
	var r ExecCommandResult
	if jerr := json.Unmarshal(line, &r); jerr != nil {
		return ExecCommandResult{}, jerr
	}
	if r.Command == "" {
		r.Command = command
	}
	if !r.Success && r.ErrorCode == "" && r.ExitCode > 0 {
		r.ErrorCode = ErrCodeRemoteExit
	}
	return r, nil
}

// execViaMux runs one command over the pooled session. usable=false means the
// daemon doesn't speak MUX and the caller must use a one-shot connection.
//
// Retry policy is deliberately at-most-once and only for a REUSED session: the
// dominant reuse failure is the daemon's idle-timeout closing the connection
// BEFORE it read the command, so re-running is safe. A freshly established
// session that fails is returned as a real error — never blindly re-executed,
// so a command that may already have run on the daemon is not double-executed.
func execViaMux(host string, port int, secret, command string, deadline time.Duration) (result ExecCommandResult, usable bool, err error) {
	if deadline <= 0 {
		deadline = 30 * time.Second
	}
	s := muxSessionFor(host, port)
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.noMux {
		return ExecCommandResult{}, false, nil // known non-MUX daemon → one-shot
	}

	reused := s.conn != nil
	if s.conn == nil {
		hasMux, derr := s.establish(host, port, secret, deadline)
		if !hasMux {
			if derr != nil {
				code, retry := classifyDialError(derr)
				return errResult(command, derr, code, retry), true, derr
			}
			return ExecCommandResult{}, false, nil // no MUX support → fall back
		}
	}

	res, rerr := s.roundtrip(command, deadline)
	if rerr == nil {
		return res, true, nil
	}

	// The session failed. Drop it, and transparently retry once only if it was a
	// reused session AND the failure is safe to re-run (idle close before the
	// command was processed — never a read timeout, which may have executed it).
	s.closeLocked()
	if reused && safeToRetry(rerr) {
		if hasMux, derr := s.establish(host, port, secret, deadline); hasMux && derr == nil {
			if res2, rerr2 := s.roundtrip(command, deadline); rerr2 == nil {
				return res2, true, nil
			}
			s.closeLocked()
		}
	}
	code, retry := classifyDialError(rerr)
	return errResult(command, rerr, code, retry), true, rerr
}

func errResult(command string, err error, code string, retry bool) ExecCommandResult {
	return ExecCommandResult{Success: false, Command: command, ExitCode: -1, Error: err.Error(), ErrorCode: code, Retryable: retry}
}
