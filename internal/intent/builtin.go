package intent

// builtinIntents is the shipped intent library: read/diagnostic-leaning plans
// for the common "what's going on with this box" questions. Users override any
// of these by name via ~/.vssh/intents.json. Commands use `||` fallbacks so a
// plan stays useful across distros/platforms.
func builtinIntents() []Intent {
	return []Intent{
		{
			Name:      "disk-check",
			Keywords:  []string{"disk check", "disk usage", "disk space", "disk", "storage usage"},
			Commands:  []string{"df -h", "du -sh /var/* /home/* 2>/dev/null | sort -rh | head -20"},
			Rationale: "filesystem usage and the largest top-level consumers",
		},
		{
			Name:      "inode-check",
			Keywords:  []string{"inode", "inodes"},
			Commands:  []string{"df -i"},
			Rationale: "inode exhaustion (a full-disk lookalike)",
		},
		{
			Name:      "log-check",
			Keywords:  []string{"log check", "recent logs", "logs", "syslog"},
			Commands:  []string{"journalctl -n 50 --no-pager 2>/dev/null || tail -n 50 /var/log/syslog 2>/dev/null || tail -n 50 /var/log/messages"},
			Rationale: "the most recent system log lines",
		},
		{
			Name:      "journal-errors",
			Keywords:  []string{"journal errors", "error logs", "errors", "log errors"},
			Commands:  []string{"journalctl -p err -n 50 --no-pager 2>/dev/null || grep -iE 'error|fail' /var/log/syslog 2>/dev/null | tail -n 50"},
			Rationale: "error-priority log entries only",
		},
		{
			Name:      "service-check",
			Keywords:  []string{"service check", "service status", "check service"},
			Commands:  []string{"systemctl status {{service}} --no-pager", "systemctl is-active {{service}}"},
			Rationale: "status and active-state of a named service",
			NeedsArg:  true,
		},
		{
			Name:      "service-logs",
			Keywords:  []string{"service logs", "unit logs", "service log"},
			Commands:  []string{"journalctl -u {{service}} -n 50 --no-pager"},
			Rationale: "recent logs for a named service unit",
			NeedsArg:  true,
		},
		{
			Name:      "failed-services",
			Keywords:  []string{"failed services", "failed units", "broken services"},
			Commands:  []string{"systemctl --failed --no-pager"},
			Rationale: "units in a failed state",
		},
		{
			Name:      "gpu-status",
			Keywords:  []string{"gpu status", "gpu", "nvidia", "cuda status"},
			Commands:  []string{"nvidia-smi"},
			Rationale: "GPU utilization, memory, and driver health",
		},
		{
			Name:      "process-check",
			Keywords:  []string{"process check", "processes", "top process", "cpu hogs"},
			Commands:  []string{"ps aux --sort=-%cpu | head -20"},
			Rationale: "the top processes by CPU",
		},
		{
			Name:      "memory-check",
			Keywords:  []string{"memory check", "memory usage", "memory", "ram", "mem"},
			Commands:  []string{"free -h 2>/dev/null || vm_stat", "ps aux --sort=-%mem | head -10"},
			Rationale: "memory pressure and the top processes by RSS",
		},
		{
			Name:      "cpu-check",
			Keywords:  []string{"cpu check", "cpu load", "cpu usage", "cpu"},
			Commands:  []string{"top -bn1 2>/dev/null | head -15 || top -l1 | head -15"},
			Rationale: "instantaneous CPU load snapshot",
		},
		{
			Name:      "uptime-check",
			Keywords:  []string{"uptime", "load average", "how long up"},
			Commands:  []string{"uptime"},
			Rationale: "uptime and load averages",
		},
		{
			Name:      "network-check",
			Keywords:  []string{"network check", "network", "connections", "sockets"},
			Commands:  []string{"ss -tulpn 2>/dev/null || netstat -tulpn 2>/dev/null", "ip -br addr 2>/dev/null || ifconfig"},
			Rationale: "active sockets and interface addresses",
		},
		{
			Name:      "listening-ports",
			Keywords:  []string{"listening ports", "open ports", "ports"},
			Commands:  []string{"ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null || lsof -iTCP -sTCP:LISTEN -P"},
			Rationale: "TCP sockets in LISTEN state",
		},
		{
			Name:      "dns-check",
			Keywords:  []string{"dns check", "dns", "resolver"},
			Commands:  []string{"cat /etc/resolv.conf", "getent hosts example.com 2>/dev/null || nslookup example.com"},
			Rationale: "resolver config and a sample lookup",
		},
		{
			Name:      "docker-status",
			Keywords:  []string{"docker status", "containers", "docker ps", "docker"},
			Commands:  []string{"docker ps -a 2>/dev/null", "docker stats --no-stream 2>/dev/null"},
			Rationale: "container inventory and live resource use",
		},
		{
			Name:      "scheduled-jobs",
			Keywords:  []string{"scheduled jobs", "timers", "cron jobs", "cron"},
			Commands:  []string{"systemctl list-timers --no-pager 2>/dev/null", "crontab -l 2>/dev/null"},
			Rationale: "systemd timers and user cron entries",
		},
		{
			Name:      "system-info",
			Keywords:  []string{"system info", "os version", "kernel", "machine info"},
			Commands:  []string{"uname -a", "cat /etc/os-release 2>/dev/null || sw_vers"},
			Rationale: "kernel and OS release identification",
		},
		{
			Name:      "who-check",
			Keywords:  []string{"who is logged in", "logged in users", "active sessions", "who"},
			Commands:  []string{"who", "w 2>/dev/null"},
			Rationale: "interactive sessions currently open",
		},
		{
			Name:      "temperature-check",
			Keywords:  []string{"temperature", "thermal", "temp"},
			Commands:  []string{"sensors 2>/dev/null || vcgencmd measure_temp 2>/dev/null || echo 'no sensor tooling'"},
			Rationale: "hardware temperature readings where available",
		},
		{
			Name:      "largest-files",
			Keywords:  []string{"largest files", "big files", "what is using space"},
			Commands:  []string{"du -ahx / 2>/dev/null | sort -rh | head -20"},
			Rationale: "the biggest files/dirs on the root filesystem",
		},
		{
			Name:      "package-updates",
			Keywords:  []string{"package updates", "upgradable", "outdated packages", "updates available"},
			Commands:  []string{"apt list --upgradable 2>/dev/null || yum check-update 2>/dev/null || dnf check-update 2>/dev/null || brew outdated 2>/dev/null"},
			Rationale: "pending package upgrades",
		},
		{
			Name:      "reboot-required",
			Keywords:  []string{"reboot required", "pending reboot", "needs reboot"},
			Commands:  []string{"[ -f /var/run/reboot-required ] && cat /var/run/reboot-required || echo 'no reboot flag'"},
			Rationale: "whether a kernel/library update is awaiting reboot",
		},
	}
}
