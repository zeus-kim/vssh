package fleet

import (
	"sort"
	"strconv"
	"strings"
)

// HealthProbeCommand gathers a node's live health signals in one round-trip.
// Everything is guarded and cross-platform-ish (Linux fleet primarily): a
// missing tool yields a zero/blank field rather than a failed probe.
const HealthProbeCommand = `
echo "load=$(cat /proc/loadavg 2>/dev/null | awk '{print $1}' || uptime | sed 's/.*averages*: //' | awk -F'[, ]+' '{print $1}')"
echo "cores=$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)"
echo "disk_pct=$(df -P / 2>/dev/null | awk 'NR==2{gsub(/%/,"",$5); print $5}')"
echo "mem_pct=$(free 2>/dev/null | awk '/Mem:/{printf "%d", $3*100/$2}')"
echo "failed=$(systemctl --failed --no-legend --no-pager 2>/dev/null | grep -c . )"
`

// HealthSignals is the parsed result of HealthProbeCommand.
type HealthSignals struct {
	Load    float64
	Cores   int
	DiskPct int
	MemPct  int
	Failed  int
}

// ParseHealth reads the probe's key=value output.
func ParseHealth(out string) HealthSignals {
	var h HealthSignals
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || v == "" {
			continue
		}
		switch k {
		case "load":
			h.Load, _ = strconv.ParseFloat(v, 64)
		case "cores":
			h.Cores, _ = strconv.Atoi(v)
		case "disk_pct":
			h.DiskPct, _ = strconv.Atoi(v)
		case "mem_pct":
			h.MemPct, _ = strconv.Atoi(v)
		case "failed":
			h.Failed, _ = strconv.Atoi(v)
		}
	}
	return h
}

// Health severity levels, ordered.
const (
	HealthOK       = "ok"
	HealthWarn     = "warn"
	HealthCritical = "critical"
)

// Assess turns raw signals into a severity and the specific reasons behind it,
// so an operator (or agent) sees WHAT is wrong, not just a color. Thresholds:
// disk 90/95%, load-per-core 2.0/4.0, memory 90%, any failed unit.
func Assess(h HealthSignals) (severity string, issues []string) {
	severity = HealthOK
	raise := func(s string) {
		// critical never downgrades to warn
		if s == HealthCritical || severity == HealthOK {
			severity = s
		}
	}

	switch {
	case h.DiskPct >= 95:
		issues = append(issues, "disk "+strconv.Itoa(h.DiskPct)+"% (critical)")
		raise(HealthCritical)
	case h.DiskPct >= 90:
		issues = append(issues, "disk "+strconv.Itoa(h.DiskPct)+"%")
		raise(HealthWarn)
	}

	if h.Cores > 0 && h.Load > 0 {
		per := h.Load / float64(h.Cores)
		switch {
		case per >= 4.0:
			issues = append(issues, "load "+trimFloat(h.Load)+" on "+strconv.Itoa(h.Cores)+" cores (critical)")
			raise(HealthCritical)
		case per >= 2.0:
			issues = append(issues, "load "+trimFloat(h.Load)+" on "+strconv.Itoa(h.Cores)+" cores")
			raise(HealthWarn)
		}
	}

	if h.MemPct >= 90 {
		issues = append(issues, "memory "+strconv.Itoa(h.MemPct)+"%")
		raise(HealthWarn)
	}
	if h.Failed > 0 {
		issues = append(issues, strconv.Itoa(h.Failed)+" failed unit(s)")
		raise(HealthWarn)
	}
	return severity, issues
}

// SeverityRank orders severities worst-first for sorting reports.
func SeverityRank(s string) int {
	switch s {
	case HealthCritical:
		return 0
	case HealthWarn:
		return 1
	default:
		return 2
	}
}

func trimFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(f, 'f', 2, 64), "0"), ".")
}

// SortReports is a helper for callers holding (name, severity) pairs, ordering
// worst-first then by name. Kept here so CLI and MCP share the ordering.
func SortReports(names []string, sev map[string]string) {
	sort.Slice(names, func(i, j int) bool {
		ri, rj := SeverityRank(sev[names[i]]), SeverityRank(sev[names[j]])
		if ri != rj {
			return ri < rj
		}
		return names[i] < names[j]
	})
}
