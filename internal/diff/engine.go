// Package diff turns the daemon's append-only audit log into a human-readable
// account of what was done: records are grouped into sessions (same operator
// key + endpoint, within a time gap) and each session gets a one-line natural
// summary. The audit log stores commands but not their output, so before/after
// is recovered from the command text itself (e.g. `sed -i 's/listen 80/.../'`
// reveals `listen 80 → 443`). Pure stdlib; operates on bytes a caller supplies.
package diff

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// defaultGap splits two consecutive records into separate sessions.
const defaultGap = 5 * time.Minute

// Record is one parsed audit-log line.
type Record struct {
	TS         time.Time
	Command    string
	Success    bool
	ExitCode   int
	DurationMs int64
	KeyName    string
	Remote     string
	Transport  string
}

// Outcome is one command's result plus any inferred before/after detail.
type Outcome struct {
	Command  string `json:"command"`
	Success  bool   `json:"success"`
	ExitCode int    `json:"exit_code"`
	Detail   string `json:"detail,omitempty"`
}

// Session is a contiguous run of commands by one operator key/endpoint.
type Session struct {
	KeyName    string    `json:"key_name,omitempty"`
	Remote     string    `json:"remote,omitempty"`
	Transport  string    `json:"transport,omitempty"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	DurationMs int64     `json:"duration_ms"`
	Commands   []string  `json:"commands"`
	Outcomes   []Outcome `json:"outcomes"`
	Summary    string    `json:"summary"`
}

// Options tune which records are considered and how sessions are cut.
type Options struct {
	LastN int           // keep only the most recent N sessions (0 = all)
	Since time.Duration // only records newer than Now-Since (0 = all)
	Now   time.Time     // reference for Since (zero = time.Now())
	Gap   time.Duration // session split gap (0 = defaultGap)
}

// AnalyzeReader parses an audit log and returns summarized sessions, newest last.
func AnalyzeReader(r io.Reader, opts Options) ([]Session, error) {
	recs, err := parseRecords(r)
	if err != nil {
		return nil, err
	}
	return Analyze(recs, opts), nil
}

// Analyze groups records into summarized sessions.
func Analyze(recs []Record, opts Options) []Session {
	if opts.Since > 0 {
		now := opts.Now
		if now.IsZero() {
			now = time.Now()
		}
		cutoff := now.Add(-opts.Since)
		filtered := recs[:0:0]
		for _, r := range recs {
			if r.TS.After(cutoff) {
				filtered = append(filtered, r)
			}
		}
		recs = filtered
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].TS.Before(recs[j].TS) })

	gap := opts.Gap
	if gap <= 0 {
		gap = defaultGap
	}
	var sessions []Session
	var cur *Session
	for _, r := range recs {
		newSession := cur == nil ||
			r.KeyName != cur.KeyName || r.Remote != cur.Remote ||
			r.TS.Sub(cur.End) > gap
		if newSession {
			if cur != nil {
				finalize(cur)
				sessions = append(sessions, *cur)
			}
			cur = &Session{
				KeyName:   r.KeyName,
				Remote:    r.Remote,
				Transport: r.Transport,
				Start:     r.TS,
			}
		}
		cur.End = r.TS
		cur.Commands = append(cur.Commands, r.Command)
		cur.Outcomes = append(cur.Outcomes, Outcome{
			Command:  r.Command,
			Success:  r.Success,
			ExitCode: r.ExitCode,
			Detail:   headlineFor(r.Command),
		})
	}
	if cur != nil {
		finalize(cur)
		sessions = append(sessions, *cur)
	}

	if opts.LastN > 0 && len(sessions) > opts.LastN {
		sessions = sessions[len(sessions)-opts.LastN:]
	}
	return sessions
}

func finalize(s *Session) {
	s.DurationMs = s.End.Sub(s.Start).Milliseconds()
	s.Summary = summarize(s)
}

func parseRecords(r io.Reader) ([]Record, error) {
	var out []Record
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var raw struct {
			TS         string `json:"ts"`
			Command    string `json:"command"`
			Success    bool   `json:"success"`
			ExitCode   int    `json:"exit_code"`
			DurationMs int64  `json:"duration_ms"`
			KeyName    string `json:"key_name"`
			Remote     string `json:"remote"`
			Transport  string `json:"transport"`
		}
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue // skip malformed lines, keep going
		}
		ts, err := time.Parse(time.RFC3339Nano, raw.TS)
		if err != nil {
			ts, err = time.Parse(time.RFC3339, raw.TS)
			if err != nil {
				continue
			}
		}
		out = append(out, Record{
			TS:         ts,
			Command:    raw.Command,
			Success:    raw.Success,
			ExitCode:   raw.ExitCode,
			DurationMs: raw.DurationMs,
			KeyName:    raw.KeyName,
			Remote:     raw.Remote,
			Transport:  raw.Transport,
		})
	}
	return out, sc.Err()
}

var (
	reSystemd  = regexp.MustCompile(`systemctl\s+(restart|start|stop|reload|enable|disable)\s+([\w.@-]+)`)
	reEditor   = regexp.MustCompile(`\b(sed|perl|awk)\b`)
	reSedSub   = regexp.MustCompile(`\bs/([^/]+)/([^/]*)/`)
	reTee      = regexp.MustCompile(`\btee\s+(?:-a\s+)?([^\s|;&]+)`)
	reRedirect = regexp.MustCompile(`>>?\s*([^\s|;&>]+)`)
	rePath     = regexp.MustCompile(`/?[\w./-]+\.(?:conf|cfg|json|ya?ml|toml|ini|env|service|sh|py|rb|js|ts|go)\b`)
	verbPast   = map[string]string{
		"restart": "restarted", "start": "started", "stop": "stopped",
		"reload": "reloaded", "enable": "enabled", "disable": "disabled",
	}
)

// headlineFor turns a command into a short human phrase ("nginx.conf changed
// (listen 80 → 443)", "service nginx restarted", "deploy.sh written"), or "" if
// the command is a plain read/diagnostic with nothing notable to report.
func headlineFor(cmd string) string {
	if m := reSystemd.FindStringSubmatch(cmd); m != nil {
		v := verbPast[m[1]]
		if v == "" {
			v = m[1]
		}
		return fmt.Sprintf("service %s %s", m[2], v)
	}
	if reEditor.MatchString(cmd) {
		if sm := reSedSub.FindStringSubmatch(cmd); sm != nil {
			from, to := trimSharedPrefix(strings.TrimSpace(sm[1]), strings.TrimSpace(sm[2]))
			if f := firstPath(cmd); f != "" {
				return fmt.Sprintf("%s changed (%s → %s)", filepath.Base(f), from, to)
			}
			return fmt.Sprintf("changed (%s → %s)", from, to)
		}
	}
	if m := reTee.FindStringSubmatch(cmd); m != nil && !isSink(m[1]) {
		return fmt.Sprintf("%s written", filepath.Base(m[1]))
	}
	// A command may hold several redirects (e.g. `cmd 2>/dev/null > out`); the
	// first is often `2>/dev/null`, a discard — not a write. Report the first
	// redirect whose target is a real file, skipping /dev sinks.
	for _, m := range reRedirect.FindAllStringSubmatch(cmd, -1) {
		if !isSink(m[1]) {
			return fmt.Sprintf("%s written", filepath.Base(m[1]))
		}
	}
	return ""
}

// isSink reports whether a redirect target is a null/device sink rather than a
// real file, so `2>/dev/null` isn't misread as "null written".
func isSink(target string) bool {
	return target == "/dev/null" || strings.HasPrefix(target, "/dev/")
}

func firstPath(cmd string) string { return rePath.FindString(cmd) }

// trimSharedPrefix drops the leading words common to from/to from the `to`
// side, so a substitution like `listen 80` → `listen 443` reads as
// "listen 80 → 443" instead of repeating the unchanged prefix.
func trimSharedPrefix(from, to string) (string, string) {
	fw := strings.Fields(from)
	tw := strings.Fields(to)
	i := 0
	for i < len(fw) && i < len(tw) && fw[i] == tw[i] {
		i++
	}
	if i > 0 && i < len(tw) {
		return from, strings.Join(tw[i:], " ")
	}
	return from, to
}

func summarize(s *Session) string {
	var parts []string
	failed := 0
	for _, o := range s.Outcomes {
		if o.Detail != "" {
			parts = append(parts, o.Detail)
		}
		if !o.Success {
			failed++
		}
	}
	result := "OK"
	if failed > 0 {
		result = fmt.Sprintf("%d failed", failed)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d command(s), result: %s", len(s.Outcomes), result)
	}
	return fmt.Sprintf("%s, result: %s", strings.Join(dedupeAdjacent(parts), ", "), result)
}

func dedupeAdjacent(in []string) []string {
	out := in[:0:0]
	for i, v := range in {
		if i == 0 || v != in[i-1] {
			out = append(out, v)
		}
	}
	return out
}
