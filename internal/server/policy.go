package server

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrCodePolicyDenied is the typed error_code returned when a per-key policy
// (the policy=<name> tag on an authorized_keys line, docs §6) refuses a command.
// Distinct from capability_denied: caps verbs are the hard floor, the policy
// narrows what an exec-capable key may actually run.
const ErrCodePolicyDenied = "policy_denied"

// Policy is a per-key command/path whitelist loaded from
// <configdir>/policies/<name>.json or /etc/vssh/policies/<name>.json (docs §6.3).
// Evaluation order is deny-first, then danger_preapproved/allow; no match =
// refuse. A key tagged policy=<name> whose file is missing/invalid fails closed.
type Policy struct {
	Name       string   `json:"name"`
	ExecAllow  []string `json:"exec_allow"`
	ExecDeny   []string `json:"exec_deny"`
	FilePaths  []string `json:"file_paths"`
	FwdTargets []string `json:"fwd_targets"`
	Rate       struct {
		ExecPerMin int `json:"exec_per_min"`
	} `json:"rate"`
	DangerPreapproved []string `json:"danger_preapproved"`

	execAllowRE []*regexp.Regexp
	execDenyRE  []*regexp.Regexp
	dangerRE    []*regexp.Regexp
}

type policyCacheEntry struct {
	mtime int64
	path  string
	pol   *Policy
	err   error
}

var policyCache sync.Map // name -> *policyCacheEntry

func policyPaths(name string) []string {
	return []string{
		filepath.Join(vsshConfigDir(), "policies", name+".json"),
		filepath.Join("/etc/vssh/policies", name+".json"),
	}
}

// compileAnchoredWarn compiles a rule list and lints each entry: an unanchored
// rule (not ^...$) can match a substring of a smuggled command, so warn loudly
// (docs §6.4). A bad regex is a hard load error (the key then fails closed).
func compileAnchoredWarn(name, kind string, pats []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(pats))
	for i, p := range pats {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("policy %q %s[%d] bad regex %q: %w", name, kind, i, p, err)
		}
		if !strings.HasPrefix(p, "^") || !strings.HasSuffix(p, "$") {
			fmt.Fprintf(os.Stderr, "vssh: WARNING: policy %q %s rule #%d not fully anchored (^...$): %q — substring/metachar smuggling risk\n", name, kind, i, p)
		}
		out = append(out, re)
	}
	return out, nil
}

// LoadPolicy reads and compiles the named policy with an mtime cache (hot-reload
// on file change). Missing/unreadable/invalid -> non-nil error; callers MUST
// fail closed (docs §6.4: a policy=<name> key whose file is gone is unusable,
// never unrestricted).
func LoadPolicy(name string) (*Policy, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "/\\.") {
		return nil, fmt.Errorf("invalid policy name %q", name)
	}
	var path string
	var mtime int64
	for _, p := range policyPaths(name) {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			path, mtime = p, fi.ModTime().UnixNano()
			break
		}
	}
	if path == "" {
		return nil, fmt.Errorf("policy %q not found", name)
	}
	if v, ok := policyCache.Load(name); ok {
		if e := v.(*policyCacheEntry); e.path == path && e.mtime == mtime {
			return e.pol, e.err
		}
	}
	pol, err := loadPolicyFile(name, path)
	policyCache.Store(name, &policyCacheEntry{mtime: mtime, path: path, pol: pol, err: err})
	return pol, err
}

func loadPolicyFile(name, path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("policy %q read: %w", name, err)
	}
	var p Policy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("policy %q parse: %w", name, err)
	}
	if p.execDenyRE, err = compileAnchoredWarn(name, "exec_deny", p.ExecDeny); err != nil {
		return nil, err
	}
	if p.execAllowRE, err = compileAnchoredWarn(name, "exec_allow", p.ExecAllow); err != nil {
		return nil, err
	}
	if p.dangerRE, err = compileAnchoredWarn(name, "danger_preapproved", p.DangerPreapproved); err != nil {
		return nil, err
	}
	return &p, nil
}

// EvalExec decides a command: deny-first, then danger_preapproved, then allow;
// no match = refuse. ruleID is recorded in the audit record.
func (p *Policy) EvalExec(command string) (allowed bool, ruleID string, preapproved bool) {
	cmd := strings.TrimRight(command, "\r\n")
	for i, re := range p.execDenyRE {
		if re.MatchString(cmd) {
			return false, fmt.Sprintf("exec_deny[%d]", i), false
		}
	}
	for i, re := range p.dangerRE {
		if re.MatchString(cmd) {
			return true, fmt.Sprintf("danger_preapproved[%d]", i), true
		}
	}
	for i, re := range p.execAllowRE {
		if re.MatchString(cmd) {
			return true, fmt.Sprintf("exec_allow[%d]", i), false
		}
	}
	return false, "no_match", false
}

// PathAllowed reports whether path is within file_paths, resolving symlinks/..
// first (docs §6.4 path-escape mitigation). Empty file_paths = deny (fail-closed).
// Both the candidate path and each glob's static base are symlink-resolved so a
// benign prefix symlink (e.g. macOS /var -> /private/var) doesn't break matching
// while a symlink/.. that escapes the allowed subtree still fails.
func (p *Policy) PathAllowed(path string) bool {
	if len(p.FilePaths) == 0 {
		return false
	}
	resolved := resolvePath(path)
	for _, glob := range p.FilePaths {
		if matchGlob(resolveGlob(glob), resolved) {
			return true
		}
	}
	return false
}

// resolvePath makes path absolute, lexically cleans .. , then symlink-resolves
// the longest existing ancestor and re-appends any non-existent tail (so a file
// that doesn't exist yet still gets its directory prefix canonicalized, e.g.
// macOS /var -> /private/var). EvalSymlinks of an existing symlinked ancestor
// also defeats a symlink that would escape the allowed subtree.
func resolvePath(path string) string {
	r := path
	if abs, err := filepath.Abs(r); err == nil {
		r = abs
	}
	r = filepath.Clean(r)
	rest := ""
	cur := r
	for {
		if ev, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Clean(filepath.Join(ev, rest))
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break // reached root; nothing existed to resolve
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
	return r
}

// resolveGlob symlink-resolves the static (wildcard-free) base of a glob so it
// compares against resolvePath output on the same footing.
func resolveGlob(glob string) string {
	i := strings.IndexAny(glob, "*?[")
	if i < 0 {
		return resolvePath(glob)
	}
	base := glob[:i]
	if j := strings.LastIndex(base, "/"); j >= 0 {
		base = base[:j]
	}
	suffix := glob[len(base):]
	return resolvePath(base) + suffix
}

// matchGlob adds a trailing /** recursive-subtree match on top of filepath.Match.
func matchGlob(glob, path string) bool {
	if strings.HasSuffix(glob, "/**") {
		base := strings.TrimSuffix(glob, "/**")
		return path == base || strings.HasPrefix(path, base+string(os.PathSeparator))
	}
	ok, _ := filepath.Match(glob, path)
	return ok
}

// --- per-connection policy attribution carried into the audit record ---

type policyMeta struct {
	ruleID      string
	preapproved bool
}

var connPolicyMeta sync.Map // net.Conn -> policyMeta

func setConnPolicyMeta(conn net.Conn, ruleID string, preapproved bool) {
	if conn != nil {
		connPolicyMeta.Store(conn, policyMeta{ruleID, preapproved})
	}
}

func connPolicyRule(conn net.Conn) (string, bool) {
	if conn == nil {
		return "", false
	}
	if v, ok := connPolicyMeta.Load(conn); ok {
		m := v.(policyMeta)
		return m.ruleID, m.preapproved
	}
	return "", false
}

// enforceExecPolicy gates one exec against the connection key's policy=<name>.
// Returns nil when execution may proceed (recording the matched rule for the
// audit log); returns a populated deny result (and audits the denial itself)
// when refused. Keys without a policy= tag keep current behavior (opt-in).
func enforceExecPolicy(conn net.Conn, command string) *ExecCommandResult {
	pub, _ := connIdentity(conn)
	if pub == "" {
		return nil // legacy/unattributed auth: no per-key policy
	}
	name, hasTag := KeyPolicy(pub)
	if !hasTag {
		return nil // opt-in: absent policy= tag = current behavior
	}
	pol, err := LoadPolicy(name)
	if err != nil {
		setConnPolicyMeta(conn, "load_error", false)
		res := ExecCommandResult{
			Success: false, ExitCode: -1, ErrorCode: ErrCodePolicyDenied,
			Error: fmt.Sprintf("policy %q unavailable (fail-closed): %v", name, err),
		}
		auditLog(conn, command, res)
		return &res
	}
	allowed, ruleID, preapproved := pol.EvalExec(command)
	setConnPolicyMeta(conn, ruleID, preapproved)
	if !allowed {
		res := ExecCommandResult{
			Success: false, ExitCode: -1, ErrorCode: ErrCodePolicyDenied,
			Error: fmt.Sprintf("command not permitted by policy %q (rule %s)", name, ruleID),
		}
		auditLog(conn, command, res)
		return &res
	}
	if pol.Rate.ExecPerMin > 0 && rateExceeded(pub, pol.Rate.ExecPerMin) {
		setConnPolicyMeta(conn, "rate_exceeded", false)
		res := ExecCommandResult{
			Success: false, ExitCode: -1, ErrorCode: ErrCodePolicyDenied,
			Error: fmt.Sprintf("policy %q rate limit exceeded (%d/min)", name, pol.Rate.ExecPerMin),
		}
		auditLog(conn, command, res)
		return &res
	}
	return nil
}

// connPolicyName returns the policy name bound to the connection's key, or "".
func connPolicyName(conn net.Conn) string {
	pub, _ := connIdentity(conn)
	if pub == "" {
		return ""
	}
	if name, ok := KeyPolicy(pub); ok {
		return name
	}
	return ""
}

// policyPathDenied gates a file-path op for a policied key against file_paths.
// Returns true (writing an error + auditing) when refused; false when allowed or
// the key carries no policy. Fail-closed when the policy cannot be loaded.
func policyPathDenied(conn net.Conn, path string) bool {
	name := connPolicyName(conn)
	if name == "" {
		return false
	}
	pol, err := LoadPolicy(name)
	if err != nil {
		setConnPolicyMeta(conn, "load_error", false)
		msg := fmt.Sprintf("policy %q unavailable (fail-closed): %v", name, err)
		auditLog(conn, "FILE "+path, ExecCommandResult{Success: false, ExitCode: -1, ErrorCode: ErrCodePolicyDenied, Error: msg})
		conn.Write([]byte("ERROR policy_denied: " + msg + "\n"))
		return true
	}
	if pol.PathAllowed(path) {
		setConnPolicyMeta(conn, "file_path_ok", false)
		return false
	}
	setConnPolicyMeta(conn, "file_path_denied", false)
	msg := fmt.Sprintf("path not permitted by policy %q: %s", name, path)
	auditLog(conn, "FILE "+path, ExecCommandResult{Success: false, ExitCode: -1, ErrorCode: ErrCodePolicyDenied, Error: msg})
	conn.Write([]byte("ERROR policy_denied: " + msg + "\n"))
	return true
}

// policyBlockUnscoped fail-closes a verb the policy engine does not yet scope
// (forwarding + multiplexed/advanced file verbs — P1b step 3) for a policied
// key. Returns true (writing an error + auditing) when blocked.
func policyBlockUnscoped(conn net.Conn, verb string) bool {
	name := connPolicyName(conn)
	if name == "" {
		return false
	}
	setConnPolicyMeta(conn, "verb_blocked", false)
	msg := fmt.Sprintf("verb %s not permitted under policy %q (unscoped; fail-closed)", verb, name)
	auditLog(conn, verb, ExecCommandResult{Success: false, ExitCode: -1, ErrorCode: ErrCodePolicyDenied, Error: msg})
	conn.Write([]byte("ERROR policy_denied: " + msg + "\n"))
	return true
}

// --- fwd_targets matching (P1b step 3) ---

// policyFwdDenied gates an outbound forward (FWD) target for a policied key
// against fwd_targets. Returns true (writing a forward error + auditing) when
// refused. Empty fwd_targets for a policied key = deny all forwards (fail-closed).
func policyFwdDenied(conn net.Conn, host string, port int) bool {
	name := connPolicyName(conn)
	if name == "" {
		return false
	}
	deny := func(msg string) bool {
		setConnPolicyMeta(conn, "fwd_denied", false)
		auditLog(conn, fmt.Sprintf("FWD %s:%d", host, port), ExecCommandResult{Success: false, ExitCode: -1, ErrorCode: ErrCodePolicyDenied, Error: msg})
		writeFwdErr(conn, msg, ErrCodePolicyDenied)
		return true
	}
	pol, err := LoadPolicy(name)
	if err != nil {
		return deny(fmt.Sprintf("policy %q unavailable (fail-closed): %v", name, err))
	}
	if fwdTargetMatch(pol.FwdTargets, host, port) {
		setConnPolicyMeta(conn, "fwd_ok", false)
		return false
	}
	return deny(fmt.Sprintf("forward %s:%d not permitted by policy %q", host, port, name))
}

// fwdTargetMatch reports whether host:port is allowed by any rule. A rule is
// "<hostOrCIDR>:<port|*>": host exact match or CIDR membership (when host parses
// as an IP); port exact or "*" for any.
func fwdTargetMatch(rules []string, host string, port int) bool {
	for _, r := range rules {
		i := strings.LastIndex(r, ":")
		if i < 0 {
			continue
		}
		hp, pp := r[:i], r[i+1:]
		if pp != "*" {
			if pv, err := strconv.Atoi(pp); err != nil || pv != port {
				continue
			}
		}
		if hp == host {
			return true
		}
		if strings.Contains(hp, "/") {
			if _, cidr, err := net.ParseCIDR(hp); err == nil {
				if ip := net.ParseIP(host); ip != nil && cidr.Contains(ip) {
					return true
				}
			}
		}
	}
	return false
}

// --- per-key exec rate limiting (P1b step 3) ---

type rateWindow struct {
	mu     sync.Mutex
	events []int64
}

var execRate sync.Map // keyPub -> *rateWindow

// rateExceeded reports whether keyPub already ran perMin execs in the last 60s;
// if not, it records this attempt. perMin<=0 (or empty key) disables the limit.
func rateExceeded(keyPub string, perMin int) bool {
	if perMin <= 0 || keyPub == "" {
		return false
	}
	v, _ := execRate.LoadOrStore(keyPub, &rateWindow{})
	w := v.(*rateWindow)
	w.mu.Lock()
	defer w.mu.Unlock()
	cutoff := time.Now().Unix() - 60
	kept := w.events[:0]
	for _, t := range w.events {
		if t >= cutoff {
			kept = append(kept, t)
		}
	}
	w.events = kept
	if len(w.events) >= perMin {
		return true
	}
	w.events = append(w.events, time.Now().Unix())
	return false
}

// PolicyCheckRPC evaluates a command against the connection key's policy WITHOUT
// executing it, for the MCP danger_preapproved auto-approve flow (docs §6.2/6.5
// step 2). Returns an RPCResponse whose Data carries {policied, decision, rule,
// preapproved}; decision is one of none|allow|preapproved|deny. The daemon stays
// authoritative — this only tells the client whether a later exec WOULD run.
func PolicyCheckRPC(conn net.Conn, command string) RPCResponse {
	name := connPolicyName(conn)
	if name == "" {
		return RPCResponse{Success: true, Data: map[string]interface{}{"policied": false, "decision": "none"}}
	}
	pol, err := LoadPolicy(name)
	if err != nil {
		return RPCResponse{Success: true, Data: map[string]interface{}{"policied": true, "decision": "deny", "rule": "load_error", "error": err.Error()}}
	}
	allowed, rule, pre := pol.EvalExec(command)
	decision := "deny"
	if allowed && pre {
		decision = "preapproved"
	} else if allowed {
		decision = "allow"
	}
	return RPCResponse{Success: true, Data: map[string]interface{}{"policied": true, "decision": decision, "rule": rule, "preapproved": pre}}
}
