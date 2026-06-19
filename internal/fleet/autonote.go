package fleet

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Auto-note: turn raw command output into concise, operationally meaningful
// notes using pure local pattern matching — no LLM, no network, no external
// process. Each detector recognizes its own output shape so the command hint is
// optional; when nothing matches we fall back to the first non-empty line.

const (
	diskThreshold     = 85  // percent; partitions at or above this are noteworthy
	highLoadThreshold = 4.0 // 1-minute load average above this is noteworthy
)

var (
	dfPctRe   = regexp.MustCompile(`^(\d+)%$`)
	loadRe    = regexp.MustCompile(`load average[s]?:\s*([0-9.]+)`)
	unitRe    = regexp.MustCompile(`([\w.@-]+\.service)`)
	cmdUnitRe = regexp.MustCompile(`systemctl\s+(?:status|is-active|show|restart|start|stop)\s+([\w.@-]+)`)
)

// ExtractNotes scans command output for noteworthy operational signals and
// returns human-readable note strings. command is an optional hint (e.g. the
// systemctl invocation) used to recover a unit name.
func ExtractNotes(command, output string) []string {
	var notes []string
	notes = append(notes, extractDisk(output)...)
	if n, ok := extractService(command, output); ok {
		notes = append(notes, n)
	}
	if n, ok := extractLoad(output); ok {
		notes = append(notes, n)
	}
	if n, ok := extractNvidia(output); ok {
		notes = append(notes, n)
	}
	if len(notes) == 0 {
		if s := firstLine(output); s != "" {
			notes = append(notes, s+" (auto)")
		}
	}
	return notes
}

// AutoNote extracts notes from output and appends each to the node, returning
// the notes that were recorded.
func (fm *FleetMemory) AutoNote(node, command, output string) []string {
	notes := ExtractNotes(command, output)
	for _, n := range notes {
		fm.AddNote(node, n)
	}
	return notes
}

// extractDisk flags every df-style row whose usage percentage is at/above the
// threshold, naming the filesystem (first column) to match `df -h` output.
func extractDisk(output string) []string {
	var out []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		for _, f := range fields {
			m := dfPctRe.FindStringSubmatch(f)
			if m == nil {
				continue
			}
			if pct, _ := strconv.Atoi(m[1]); pct >= diskThreshold {
				out = append(out, fmt.Sprintf("disk %s at %d%% (auto)", fields[0], pct))
			}
			break // one percentage column per row
		}
	}
	return out
}

// extractService detects a failed/dead unit in systemctl output and names it
// from the command hint or the output's "<unit>.service" token.
func extractService(command, output string) (string, bool) {
	low := strings.ToLower(output)
	if !strings.Contains(low, "active: failed") && !strings.Contains(low, "(dead)") {
		return "", false
	}
	unit := ""
	if m := cmdUnitRe.FindStringSubmatch(command); m != nil {
		unit = m[1]
	} else if m := unitRe.FindStringSubmatch(output); m != nil {
		unit = m[1]
	}
	unit = strings.TrimSuffix(unit, ".service")
	if unit == "" {
		unit = "service"
	}
	return fmt.Sprintf("service %s failed (auto)", unit), true
}

// extractLoad notes a high 1-minute load average from `uptime`/`/proc/loadavg`.
func extractLoad(output string) (string, bool) {
	m := loadRe.FindStringSubmatch(output)
	if m == nil {
		return "", false
	}
	one, err := strconv.ParseFloat(m[1], 64)
	if err != nil || one < highLoadThreshold {
		return "", false
	}
	return fmt.Sprintf("high load avg %.2f (auto)", one), true
}

// extractNvidia flags common nvidia-smi failure markers (driver mismatch, NVML
// init failure, ERR! fields).
func extractNvidia(output string) (string, bool) {
	low := strings.ToLower(output)
	markers := []string{
		"failed to initialize nvml",
		"driver/library version mismatch",
		"err!",
		"nvidia-smi has failed",
		"no devices were found",
	}
	for _, mk := range markers {
		if strings.Contains(low, mk) {
			return "nvidia driver issue (auto)", true
		}
	}
	return "", false
}

// firstLine returns the first non-empty, trimmed line, capped at 160 runes.
func firstLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if r := []rune(line); len(r) > 160 {
			return string(r[:160])
		}
		return line
	}
	return ""
}
