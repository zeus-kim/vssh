package fleet

import (
	"sort"
	"strconv"
	"strings"
)

// Discovery infers what a node IS from what it actually RUNS — GPUs, running
// units, listening ports, containers, disk — instead of asking an operator to
// hand-maintain fleet_memory.json. The probe emits `key=value` lines; parsing
// and inference are pure functions so the rules are testable without a fleet.

// ProbeCommand gathers usage-pattern signals from a node. Everything is
// best-effort and guarded, so a missing tool just yields fewer signals rather
// than a failed probe. Output is newline-separated `key=value`.
const ProbeCommand = `
echo "os=$(uname -s)"
echo "arch=$(uname -m)"
if [ -r /etc/os-release ]; then . /etc/os-release 2>/dev/null; echo "distro=${ID:-}"; fi
if command -v nvidia-smi >/dev/null 2>&1; then
  nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | while read -r g; do echo "gpu=$g"; done
fi
if command -v systemctl >/dev/null 2>&1; then
  systemctl list-units --type=service --state=running --no-legend --no-pager 2>/dev/null \
    | awk '{sub(/\.service$/,"",$1); print "unit="$1}'
fi
(ss -tlnH 2>/dev/null || netstat -tln 2>/dev/null) \
  | awk '{print $4}' | sed 's/.*[:.]//' | grep -E '^[0-9]+$' | sort -un | awk '{print "port="$1}'
if command -v docker >/dev/null 2>&1; then echo "containers=$(docker ps -q 2>/dev/null | wc -l | tr -d ' ')"; fi
echo "disk_gb=$(df -k / 2>/dev/null | tail -1 | awk '{printf "%d", $2/1048576}')"
# Largest single real filesystem — a NAS/storage box has its bulk on a data
# volume (/volume1, /mnt/...), not on a small root, so root size alone misses it.
echo "bigdisk_gb=$(df -k -x tmpfs -x devtmpfs -x overlay -x squashfs 2>/dev/null | awk 'NR>1{if($2>m)m=$2}END{printf "%d", m/1048576}')"
`

// Signals is the parsed result of ProbeCommand.
type Signals struct {
	OS         string
	Arch       string
	Distro     string
	GPUs       []string // GPU model names, one per card
	Units      []string // running systemd service units
	Ports      []int    // listening TCP ports
	Containers int
	DiskGB     int // root filesystem size
	BigDiskGB  int // largest single real filesystem (data volume on a NAS)
}

// ParseSignals reads the probe's key=value output.
func ParseSignals(out string) Signals {
	var s Signals
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || v == "" {
			continue
		}
		switch k {
		case "os":
			s.OS = v
		case "arch":
			s.Arch = v
		case "distro":
			s.Distro = v
		case "gpu":
			s.GPUs = append(s.GPUs, v)
		case "unit":
			s.Units = append(s.Units, v)
		case "port":
			if n, err := strconv.Atoi(v); err == nil {
				s.Ports = append(s.Ports, n)
			}
		case "containers":
			s.Containers, _ = strconv.Atoi(v)
		case "disk_gb":
			s.DiskGB, _ = strconv.Atoi(v)
		case "bigdisk_gb":
			s.BigDiskGB, _ = strconv.Atoi(v)
		}
	}
	return s
}

// serviceRules maps an observed signal to a canonical service name. A node is
// credited with a service if any of its unit names contain the match, or it
// listens on one of the ports.
var serviceRules = []struct {
	name  string
	units []string
	ports []int
}{
	{"ollama", []string{"ollama"}, []int{11434}},
	{"docker", []string{"docker", "containerd"}, nil},
	{"nginx", []string{"nginx"}, nil},
	{"caddy", []string{"caddy"}, nil},
	{"postgres", []string{"postgres"}, []int{5432}},
	{"mysql", []string{"mysql", "mariadb"}, []int{3306}},
	{"redis", []string{"redis"}, []int{6379}},
	{"mail", []string{"mox", "postfix", "dovecot", "exim"}, []int{25, 587, 993}},
	{"samba", []string{"smbd", "samba"}, []int{445}},
	{"nfs", []string{"nfs-server", "nfsd", "nfs-kernel"}, []int{2049}},
	{"minio", []string{"minio"}, []int{9000}},
	{"prometheus", []string{"prometheus"}, []int{9090}},
	{"node_exporter", []string{"node_exporter", "node-exporter"}, []int{9100}},
	{"grafana", []string{"grafana"}, []int{3000}},
	{"tailscale", []string{"tailscaled", "tailscale"}, nil},
	{"vsshd", []string{"vsshd"}, nil},
	{"k3s", []string{"k3s"}, nil},
	{"squid", []string{"squid"}, []int{3128}},
	{"haproxy", []string{"haproxy"}, nil},
	{"ssh", []string{"sshd", "ssh"}, []int{22}},
}

// Infer turns raw signals into a node's role, services, and tags. Rules are
// ordered most-specific-first; role falls back to "vm" for a plain box.
func Infer(sig Signals) NodeMemory {
	var m NodeMemory

	// Services: whatever the box is actually running.
	unitBlob := strings.ToLower(strings.Join(sig.Units, " "))
	portSet := map[int]bool{}
	for _, p := range sig.Ports {
		portSet[p] = true
	}
	var services []string
	for _, r := range serviceRules {
		hit := false
		for _, u := range r.units {
			if strings.Contains(unitBlob, u) {
				hit = true
				break
			}
		}
		if !hit {
			for _, p := range r.ports {
				if portSet[p] {
					hit = true
					break
				}
			}
		}
		if hit {
			services = append(services, r.name)
		}
	}
	sort.Strings(services)
	m.Services = services

	has := func(name string) bool {
		for _, s := range services {
			if s == name {
				return true
			}
		}
		return false
	}

	// A real mail server runs a mail daemon or exposes the submission/IMAP ports —
	// not just a local MTA idling on :25 (macOS/Linux ship one by default), which
	// would otherwise mislabel an ordinary box as the fleet's mail node.
	mailUnit := false
	for _, u := range []string{"mox", "postfix", "dovecot", "exim"} {
		if strings.Contains(unitBlob, u) {
			mailUnit = true
			break
		}
	}
	mailPorts := 0
	for _, p := range []int{25, 587, 993} {
		if portSet[p] {
			mailPorts++
		}
	}
	mailServer := mailUnit || mailPorts >= 2

	// A storage box either serves files (samba/nfs/minio) or carries a large data
	// volume — checked via the LARGEST filesystem, not root, so a NAS with a small
	// system partition and huge /volume isn't missed.
	biggest := sig.DiskGB
	if sig.BigDiskGB > biggest {
		biggest = sig.BigDiskGB
	}
	storageBox := has("samba") || has("nfs") || has("minio") || biggest >= 4000

	// Role: what the box is FOR, judged by its dominant workload.
	switch {
	case len(sig.GPUs) > 0:
		m.Role = "gpu"
	case mailServer:
		m.Role = "mail"
	case storageBox:
		m.Role = "storage"
	case has("squid") || has("haproxy"):
		m.Role = "network"
	default:
		m.Role = "vm"
	}

	// Tags: platform facts plus notable capabilities, for @tag: selectors.
	var tags []string
	if sig.OS != "" {
		tags = append(tags, strings.ToLower(sig.OS))
	}
	switch sig.Arch {
	case "x86_64", "amd64":
		tags = append(tags, "amd64")
	case "aarch64", "arm64":
		tags = append(tags, "arm64")
	case "":
	default:
		tags = append(tags, strings.ToLower(sig.Arch))
	}
	if sig.Distro != "" {
		tags = append(tags, strings.ToLower(sig.Distro))
	}
	if len(sig.GPUs) > 0 {
		tags = append(tags, "gpu")
		if n := len(sig.GPUs); n > 1 {
			tags = append(tags, "multi-gpu")
		}
		if slug := gpuSlug(sig.GPUs[0]); slug != "" {
			tags = append(tags, slug)
		}
	}
	if sig.Containers > 0 {
		tags = append(tags, "containers")
	}
	sort.Strings(tags)
	m.Tags = tags
	return m
}

// gpuSlug reduces a GPU model name to a short, selector-friendly tag
// ("NVIDIA GeForce RTX 4090" → "rtx4090").
func gpuSlug(name string) string {
	n := strings.ToLower(name)
	for _, fam := range []string{"rtx", "gtx", "tesla", "quadro"} {
		if i := strings.Index(n, fam); i >= 0 {
			rest := strings.Fields(n[i:])
			if len(rest) >= 2 {
				return strings.ReplaceAll(rest[0]+rest[1], " ", "")
			}
		}
	}
	for _, model := range []string{"a100", "h100", "l40", "a6000", "v100"} {
		if strings.Contains(n, model) {
			return model
		}
	}
	return ""
}
