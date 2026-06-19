package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// auditLogPath resolves (once) the destination for the daemon audit trail:
// a system path when running as root, else the user's ~/.vssh, else /tmp.
func auditLogPath() string {
	auditPathOnce.Do(func() {
		var candidates []string
		if os.Geteuid() == 0 {
			candidates = append(candidates, "/var/log/vssh")
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			candidates = append(candidates, filepath.Join(home, ".vssh"))
		}
		candidates = append(candidates, "/tmp/vssh")
		for _, dir := range candidates {
			if os.MkdirAll(dir, 0700) == nil {
				auditPathCache = filepath.Join(dir, "audit.log")
				return
			}
		}
	})
	return auditPathCache
}

// AuditLogPath exposes the daemon audit-log destination (for the audit-verify CLI).
func AuditLogPath() string { return auditLogPath() }

// lastAuditHash seeds the hash chain from an existing log: the chain value is
// simply the SHA-256 of the last line, so verification needs no side state.
func lastAuditHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(last))
	return hex.EncodeToString(sum[:])
}

// connIdentities maps a live connection to the identity that authenticated it
// (VAUTH1 pubkey + authorized_keys comment, or legacy-hmac), so the audit
// trail can attribute every record to a key instead of just a source IP.
var connIdentities sync.Map

func setConnIdentity(conn net.Conn, pub, name string) {
	if conn != nil {
		connIdentities.Store(conn, [2]string{pub, name})
	}
}

func clearConnIdentity(conn net.Conn) {
	if conn != nil {
		connIdentities.Delete(conn)
		connTransports.Delete(conn)
		connPolicyMeta.Delete(conn)
	}
}

// connTransports records whether an authenticated connection arrived over TLS
// ("tls") or the legacy plaintext wire ("plain"). Audit records carry it so
// the §5.3 stabilization gate — zero plaintext-auth records fleet-wide — is
// measurable from the logs alone (grep '"transport":"plain"').
var connTransports sync.Map

func setConnTransport(conn net.Conn, transport string) {
	if conn != nil && transport != "" {
		connTransports.Store(conn, transport)
	}
}

func connTransport(conn net.Conn) string {
	if conn == nil {
		return ""
	}
	if v, ok := connTransports.Load(conn); ok {
		return v.(string)
	}
	return ""
}

func connIdentity(conn net.Conn) (string, string) {
	if conn == nil {
		return "", ""
	}
	if v, ok := connIdentities.Load(conn); ok {
		id := v.([2]string)
		return id[0], id[1]
	}
	return "", ""
}

// auditLog appends a structured, best-effort JSONL record for every command the
// daemon executes. It is server-side and unconditional, so it captures activity a
// client (or a compromised one) cannot suppress — the compliance backbone that a
// plain-ssh wrapper can't provide. Write failures never affect execution.
//
// Records are hash-chained: each carries "prev" = SHA-256 of the previous line,
// so any in-place edit, deletion, or reordering breaks the chain and is caught
// by `vssh audit-verify` (append-only tamper evidence).
func auditLog(conn net.Conn, command string, result ExecCommandResult) {
	path := auditLogPath()
	if path == "" {
		return
	}
	remote := ""
	if conn != nil {
		if addr := conn.RemoteAddr(); addr != nil {
			remote = addr.String()
		}
	}
	cmd := command
	if len(cmd) > 800 {
		cmd = cmd[:800] + "…"
	}
	auditChainMu.Lock()
	defer auditChainMu.Unlock()
	if !auditChainInit {
		auditChainPrev = lastAuditHash(path)
		auditChainInit = true
	}
	rec := map[string]interface{}{
		"ts":          time.Now().UTC().Format(time.RFC3339Nano),
		"remote":      remote,
		"command":     cmd,
		"success":     result.Success,
		"exit_code":   result.ExitCode,
		"duration_ms": result.DurationMs,
		"prev":        auditChainPrev,
	}
	if keyPub, keyName := connIdentity(conn); keyPub != "" || keyName != "" {
		if keyPub != "" {
			rec["key"] = keyPub
		}
		if keyName != "" {
			rec["key_name"] = keyName
		}
	}
	if tp := connTransport(conn); tp != "" {
		rec["transport"] = tp
	}
	if rid, pre := connPolicyRule(conn); rid != "" {
		rec["policy_rule"] = rid
		if pre {
			rec["preapproved"] = true
		}
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	if _, werr := f.Write(append(line, '\n')); werr == nil {
		sum := sha256.Sum256(line)
		auditChainPrev = hex.EncodeToString(sum[:])
	}
}
