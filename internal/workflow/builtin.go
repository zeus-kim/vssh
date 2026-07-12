package workflow

// builtinWorkflows ships the core operational playbooks. Mutating workflows use
// tolerant on_fail ("continue") for best-effort cleanup; service-restart aborts
// if the unit doesn't exist and reports if it fails to come back up.
func builtinWorkflows() []Workflow {
	return []Workflow{
		{
			Name:        "service-restart",
			Description: "Restart a systemd service with a pre-check and post-verify.",
			Params:      []string{"service"},
			Steps: []Step{
				{ID: "check", Cmd: "systemctl status {{service}} --no-pager", OnFail: "abort"},
				{ID: "stop", Cmd: "systemctl stop {{service}}", OnFail: "continue"},
				{ID: "start", Cmd: "systemctl start {{service}}", OnFail: "report"},
				{ID: "verify", Cmd: "systemctl is-active {{service}}", OnFail: "report"},
				{ID: "report", Type: "summary"},
			},
		},
		{
			Name:        "health-check",
			Description: "Quick box health: uptime, disk, memory, top processes.",
			Steps: []Step{
				{ID: "uptime", Cmd: "uptime", OnFail: "continue"},
				{ID: "disk", Cmd: "df -h", OnFail: "continue"},
				{ID: "memory", Cmd: "free -h 2>/dev/null || vm_stat", OnFail: "continue"},
				{ID: "top", Cmd: "ps -eo pid,pcpu,pmem,comm 2>/dev/null | sort -k2 -nr | head -10", OnFail: "continue"},
				{ID: "summary", Type: "summary"},
			},
		},
		{
			Name:        "disk-cleanup",
			Description: "Best-effort space reclaim: old /tmp files, journal vacuum, package cache.",
			Steps: []Step{
				{ID: "before", Cmd: "df -h /", OnFail: "continue"},
				{ID: "tmp", Cmd: "find /tmp -type f -atime +7 -delete 2>/dev/null; echo '/tmp pruned'", OnFail: "continue"},
				{ID: "journal", Cmd: "journalctl --vacuum-time=7d 2>/dev/null || echo 'no journald'", OnFail: "continue"},
				{ID: "pkgcache", Cmd: "apt-get clean 2>/dev/null || yum clean all 2>/dev/null || dnf clean all 2>/dev/null || echo 'no apt/yum/dnf'", OnFail: "continue"},
				{ID: "after", Cmd: "df -h /", OnFail: "continue"},
				{ID: "summary", Type: "summary"},
			},
		},
		{
			Name:        "log-collect",
			Description: "Collect recent system logs, error-priority entries, and failed units.",
			Steps: []Step{
				{ID: "recent", Cmd: "journalctl -n 100 --no-pager 2>/dev/null || tail -n 100 /var/log/syslog 2>/dev/null || tail -n 100 /var/log/messages", OnFail: "continue"},
				{ID: "errors", Cmd: "journalctl -p err -n 50 --no-pager 2>/dev/null", OnFail: "continue"},
				{ID: "failed", Cmd: "systemctl --failed --no-pager 2>/dev/null", OnFail: "continue"},
				{ID: "summary", Type: "summary"},
			},
		},
	}
}
