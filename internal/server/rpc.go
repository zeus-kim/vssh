package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// RPC methods available
var RPCMethods = map[string]string{
	"get_gpu":           "read",
	"get_disk":          "read",
	"get_memory":        "read",
	"get_load":          "read",
	"get_processes":     "read",
	"get_logs":          "read",
	"service_status":    "read",
	"list_services":     "read",
	"docker_containers": "read",
	"job_start":         "write",
	"job_status":        "read",
	"job_logs":          "read",
	"job_cancel":        "admin",
	"artifact_collect":  "read",
	"file_read":         "read",
	"file_write":        "write",
	"restart_service":   "admin",
	"node_info":         "read",
}

func commandOutputTimeout(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s timed out after %s", name, timeout)
	}
	return out, err
}

// RPCRequest represents an RPC request
type RPCRequest struct {
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params,omitempty"`
}

// RPCResponse represents an RPC response
type RPCResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// HandleRPC handles RPC command on server
func HandleRPC(method string, paramsJSON []byte) []byte {
	var params map[string]interface{}
	if len(paramsJSON) > 0 {
		json.Unmarshal(paramsJSON, &params)
	}
	if params == nil {
		params = make(map[string]interface{})
	}

	var result interface{}
	var err error

	switch method {
	case "get_gpu":
		result, err = rpcGetGPU()
	case "get_disk":
		result, err = rpcGetDisk()
	case "get_memory":
		result, err = rpcGetMemory()
	case "get_load":
		result, err = rpcGetLoad()
	case "get_processes":
		n := 10
		if v, ok := params["n"].(float64); ok {
			n = int(v)
		}
		sort := "cpu"
		if v, ok := params["sort"].(string); ok {
			sort = v
		}
		result, err = rpcGetProcesses(n, sort)
	case "get_logs":
		service := ""
		if v, ok := params["service"].(string); ok {
			service = v
		}
		lines := 50
		if v, ok := params["lines"].(float64); ok {
			lines = int(v)
		}
		result, err = rpcGetLogs(service, lines)
	case "service_status":
		service := ""
		if v, ok := params["service"].(string); ok {
			service = v
		}
		result, err = rpcServiceStatus(service)
	case "list_services":
		result, err = rpcListServices()
	case "docker_containers":
		result, err = rpcDockerContainers()
	case "job_start":
		command := ""
		if v, ok := params["command"].(string); ok {
			command = v
		}
		result, err = rpcJobStart(command)
	case "job_status":
		id := ""
		if v, ok := params["id"].(string); ok {
			id = v
		}
		result, err = rpcJobStatus(id)
	case "job_logs":
		id := ""
		if v, ok := params["id"].(string); ok {
			id = v
		}
		tailBytes := 0
		if v, ok := params["tail_bytes"].(float64); ok {
			tailBytes = int(v)
		}
		result, err = rpcJobLogs(id, tailBytes)
	case "job_cancel":
		id := ""
		if v, ok := params["id"].(string); ok {
			id = v
		}
		result, err = rpcJobCancel(id)
	case "artifact_collect":
		path := ""
		if v, ok := params["path"].(string); ok {
			path = v
		}
		maxBytes := int64(1024 * 1024)
		if v, ok := params["max_bytes"].(float64); ok {
			maxBytes = int64(v)
		}
		result, err = rpcArtifactCollect(path, maxBytes)
	case "file_read":
		path := ""
		if v, ok := params["path"].(string); ok {
			path = v
		}
		result, err = rpcFileRead(path)
	case "file_write":
		path := ""
		content := ""
		if v, ok := params["path"].(string); ok {
			path = v
		}
		if v, ok := params["content"].(string); ok {
			content = v
		}
		result, err = rpcFileWrite(path, content)
	case "restart_service":
		service := ""
		if v, ok := params["service"].(string); ok {
			service = v
		}
		result, err = rpcRestartService(service)
	case "node_info":
		result, err = rpcNodeInfo()
	default:
		err = fmt.Errorf("unknown method: %s", method)
	}

	resp := RPCResponse{Success: err == nil}
	if err != nil {
		resp.Error = err.Error()
	} else {
		resp.Data = result
	}

	data, _ := json.Marshal(resp)
	return data
}

type ArtifactInfo struct {
	Path       string                 `json:"path"`
	Type       string                 `json:"type"`
	SizeBytes  int64                  `json:"size_bytes"`
	Mode       string                 `json:"mode"`
	ModTime    string                 `json:"mod_time"`
	Truncated  bool                   `json:"truncated,omitempty"`
	Encoding   string                 `json:"encoding,omitempty"`
	Content    string                 `json:"content,omitempty"`
	Entries    []ArtifactDirEntry     `json:"entries,omitempty"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

type ArtifactDirEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Type      string `json:"type"`
	SizeBytes int64  `json:"size_bytes"`
	Mode      string `json:"mode"`
	ModTime   string `json:"mod_time"`
}

func rpcArtifactCollect(path string, maxBytes int64) (*ArtifactInfo, error) {
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if maxBytes <= 0 {
		maxBytes = 1024 * 1024
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	artifact := &ArtifactInfo{
		Path:      path,
		SizeBytes: info.Size(),
		Mode:      info.Mode().String(),
		ModTime:   info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if info.IsDir() {
		artifact.Type = "directory"
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for i, entry := range entries {
			if i >= 200 {
				artifact.Truncated = true
				break
			}
			childInfo, err := entry.Info()
			if err != nil {
				continue
			}
			entryType := "file"
			if childInfo.IsDir() {
				entryType = "directory"
			}
			childPath := filepath.Join(path, entry.Name())
			artifact.Entries = append(artifact.Entries, ArtifactDirEntry{
				Name:      entry.Name(),
				Path:      childPath,
				Type:      entryType,
				SizeBytes: childInfo.Size(),
				Mode:      childInfo.Mode().String(),
				ModTime:   childInfo.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
			})
		}
		return artifact, nil
	}

	artifact.Type = "file"
	limit := maxBytes
	if info.Size() < limit {
		limit = info.Size()
	}
	data := make([]byte, limit)
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	n, err := file.Read(data)
	if err != nil && n == 0 {
		return nil, err
	}
	artifact.Truncated = int64(n) < info.Size()
	artifact.Encoding = "base64"
	artifact.Content = base64.StdEncoding.EncodeToString(data[:n])
	return artifact, nil
}

// GPU info
type GPUInfo struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	MemoryTotal int64  `json:"memory_total_mb"`
	MemoryUsed  int64  `json:"memory_used_mb"`
	MemoryFree  int64  `json:"memory_free_mb"`
	Utilization int    `json:"utilization_percent"`
	Temperature int    `json:"temperature_c"`
}

func rpcGetGPU() ([]GPUInfo, error) {
	out, err := commandOutputTimeout(3*time.Second, "nvidia-smi",
		"--query-gpu=index,name,memory.total,memory.used,memory.free,utilization.gpu,temperature.gpu",
		"--format=csv,noheader,nounits")
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi not available")
	}

	var gpus []GPUInfo
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), ", ")
		if len(parts) >= 7 {
			idx, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
			memTotal, _ := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
			memUsed, _ := strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 64)
			memFree, _ := strconv.ParseInt(strings.TrimSpace(parts[4]), 10, 64)
			util, _ := strconv.Atoi(strings.TrimSpace(parts[5]))
			temp, _ := strconv.Atoi(strings.TrimSpace(parts[6]))

			gpus = append(gpus, GPUInfo{
				Index:       idx,
				Name:        strings.TrimSpace(parts[1]),
				MemoryTotal: memTotal,
				MemoryUsed:  memUsed,
				MemoryFree:  memFree,
				Utilization: util,
				Temperature: temp,
			})
		}
	}
	return gpus, nil
}

// Disk info
type DiskInfo struct {
	Filesystem string `json:"filesystem"`
	Size       string `json:"size"`
	Used       string `json:"used"`
	Avail      string `json:"avail"`
	UsePercent string `json:"use_percent"`
	MountPoint string `json:"mount_point"`
}

func rpcGetDisk() ([]DiskInfo, error) {
	out, err := commandOutputTimeout(3*time.Second, "df", "-kP")
	if err != nil {
		return nil, err
	}

	var disks []DiskInfo
	lines := strings.Split(string(out), "\n")
	for i, line := range lines {
		if i == 0 || line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 6 {
			disks = append(disks, DiskInfo{
				Filesystem: fields[0],
				Size:       fields[1] + "K",
				Used:       fields[2] + "K",
				Avail:      fields[3] + "K",
				UsePercent: fields[4],
				MountPoint: strings.Join(fields[5:], " "),
			})
		}
	}
	return disks, nil
}

// Memory info
type MemoryInfo struct {
	Total     int64 `json:"total_mb"`
	Used      int64 `json:"used_mb"`
	Free      int64 `json:"free_mb"`
	Available int64 `json:"available_mb"`
	Cached    int64 `json:"cached_mb"`
}

func rpcGetMemory() (*MemoryInfo, error) {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("vm_stat").Output()
		if err != nil {
			return nil, err
		}
		pageSize := int64(4096)
		info := &MemoryInfo{}
		var active, inactive, speculative, wired, compressor, free int64
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Mach Virtual Memory Statistics:") {
				fmt.Sscanf(line, "Mach Virtual Memory Statistics: (page size of %d bytes)", &pageSize)
				continue
			}
			line = strings.TrimSuffix(line, ".")
			var value int64
			switch {
			case strings.HasPrefix(line, "Pages free:"):
				fmt.Sscanf(line, "Pages free: %d", &value)
				free = value
			case strings.HasPrefix(line, "Pages active:"):
				fmt.Sscanf(line, "Pages active: %d", &value)
				active = value
			case strings.HasPrefix(line, "Pages inactive:"):
				fmt.Sscanf(line, "Pages inactive: %d", &value)
				inactive = value
			case strings.HasPrefix(line, "Pages speculative:"):
				fmt.Sscanf(line, "Pages speculative: %d", &value)
				speculative = value
			case strings.HasPrefix(line, "Pages wired down:"):
				fmt.Sscanf(line, "Pages wired down: %d", &value)
				wired = value
			case strings.HasPrefix(line, "Pages occupied by compressor:"):
				fmt.Sscanf(line, "Pages occupied by compressor: %d", &value)
				compressor = value
			}
		}
		totalPages := active + inactive + speculative + wired + compressor + free
		usedPages := active + wired + compressor
		availablePages := inactive + speculative + free
		info.Total = totalPages * pageSize / 1024 / 1024
		info.Used = usedPages * pageSize / 1024 / 1024
		info.Free = free * pageSize / 1024 / 1024
		info.Available = availablePages * pageSize / 1024 / 1024
		info.Cached = (inactive + speculative) * pageSize / 1024 / 1024
		return info, nil
	}

	// Linux - read /proc/meminfo
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return nil, err
	}

	info := &MemoryInfo{}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		val, _ := strconv.ParseInt(parts[1], 10, 64)
		val /= 1024 // KB to MB

		switch parts[0] {
		case "MemTotal:":
			info.Total = val
		case "MemFree:":
			info.Free = val
		case "MemAvailable:":
			info.Available = val
		case "Cached:":
			info.Cached = val
		}
	}
	info.Used = info.Total - info.Available
	return info, nil
}

// Load info
type LoadInfo struct {
	Load1  float64 `json:"load_1"`
	Load5  float64 `json:"load_5"`
	Load15 float64 `json:"load_15"`
	CPUs   int     `json:"cpus"`
}

func rpcGetLoad() (*LoadInfo, error) {
	info := &LoadInfo{CPUs: runtime.NumCPU()}

	if runtime.GOOS == "darwin" {
		out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
		if err == nil {
			// Format: { 1.23 4.56 7.89 }
			s := strings.Trim(string(out), "{ }\n")
			fmt.Sscanf(s, "%f %f %f", &info.Load1, &info.Load5, &info.Load15)
		}
	} else {
		data, _ := os.ReadFile("/proc/loadavg")
		fmt.Sscanf(string(data), "%f %f %f", &info.Load1, &info.Load5, &info.Load15)
	}
	return info, nil
}

// Process info
type ProcessInfo struct {
	PID     int     `json:"pid"`
	User    string  `json:"user"`
	CPU     float64 `json:"cpu_percent"`
	Memory  float64 `json:"mem_percent"`
	Command string  `json:"command"`
}

func rpcGetProcesses(n int, sortBy string) ([]ProcessInfo, error) {
	var out []byte
	var err error

	if runtime.GOOS == "darwin" {
		out, err = exec.Command("ps", "aux", "-r").Output()
	} else {
		sortFlag := "-pcpu"
		if sortBy == "mem" {
			sortFlag = "-pmem"
		}
		out, err = exec.Command("ps", "aux", "--sort="+sortFlag).Output()
	}
	if err != nil {
		return nil, err
	}

	var procs []ProcessInfo
	lines := strings.Split(string(out), "\n")
	for i, line := range lines {
		if i == 0 || line == "" || len(procs) >= n {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 11 {
			pid, _ := strconv.Atoi(fields[1])
			cpu, _ := strconv.ParseFloat(fields[2], 64)
			mem, _ := strconv.ParseFloat(fields[3], 64)
			cmd := strings.Join(fields[10:], " ")
			if len(cmd) > 80 {
				cmd = cmd[:80] + "..."
			}
			procs = append(procs, ProcessInfo{
				PID:     pid,
				User:    fields[0],
				CPU:     cpu,
				Memory:  mem,
				Command: cmd,
			})
		}
	}
	return procs, nil
}

func rpcGetLogs(service string, lines int) (string, error) {
	if service == "" {
		out, err := exec.Command("journalctl", "-n", strconv.Itoa(lines), "--no-pager").Output()
		if err != nil {
			out, _ = exec.Command("dmesg", "-T").Output()
			lineSlice := strings.Split(string(out), "\n")
			if len(lineSlice) > lines {
				lineSlice = lineSlice[len(lineSlice)-lines:]
			}
			return strings.Join(lineSlice, "\n"), nil
		}
		return string(out), nil
	}

	out, err := exec.Command("journalctl", "-u", service, "-n", strconv.Itoa(lines), "--no-pager").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func rpcServiceStatus(service string) (map[string]interface{}, error) {
	out, err := exec.Command("systemctl", "status", service, "--no-pager").Output()
	status := "unknown"
	if err == nil {
		if strings.Contains(string(out), "Active: active") {
			status = "active"
		} else if strings.Contains(string(out), "Active: inactive") {
			status = "inactive"
		} else if strings.Contains(string(out), "Active: failed") {
			status = "failed"
		}
	}
	return map[string]interface{}{
		"service": service,
		"status":  status,
		"output":  string(out),
	}, nil
}

func rpcListServices() ([]map[string]string, error) {
	out, err := exec.Command("systemctl", "list-units", "--type=service", "--state=running", "--no-pager", "--no-legend").Output()
	if err != nil {
		return nil, err
	}

	var services []map[string]string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			services = append(services, map[string]string{
				"name":   strings.TrimSuffix(fields[0], ".service"),
				"load":   fields[1],
				"active": fields[2],
				"sub":    fields[3],
			})
		}
	}
	return services, nil
}

func rpcDockerContainers() ([]map[string]string, error) {
	out, err := exec.Command("docker", "ps", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Status}}").Output()
	if err != nil {
		return nil, err
	}

	var containers []map[string]string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 4 {
			containers = append(containers, map[string]string{
				"id":     parts[0],
				"name":   parts[1],
				"image":  parts[2],
				"status": parts[3],
			})
		}
	}
	return containers, nil
}

func rpcFileRead(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path required")
	}
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[1:])
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func rpcFileWrite(path, content string) (bool, error) {
	if path == "" {
		return false, fmt.Errorf("path required")
	}
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[1:])
	}

	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0755)

	err := os.WriteFile(path, []byte(content), 0644)
	return err == nil, err
}

func rpcRestartService(service string) (bool, error) {
	if service == "" {
		return false, fmt.Errorf("service name required")
	}

	allowed := map[string]bool{
		"nginx": true, "docker": true, "postgresql": true,
		"redis": true, "mysql": true, "mongod": true,
	}
	if !allowed[service] {
		return false, fmt.Errorf("service not in whitelist")
	}

	err := exec.Command("systemctl", "restart", service).Run()
	return err == nil, err
}

// INFO command - server status for AI
type ServerInfo struct {
	Hostname string      `json:"hostname"`
	OS       string      `json:"os"`
	Arch     string      `json:"arch"`
	CPUs     int         `json:"cpus"`
	Memory   *MemoryInfo `json:"memory"`
	Load     *LoadInfo   `json:"load"`
	Disk     []DiskInfo  `json:"disk"`
	GPU      []GPUInfo   `json:"gpu,omitempty"`
}

func GetServerInfo() *ServerInfo {
	hostname, _ := os.Hostname()
	mem, _ := rpcGetMemory()
	load, _ := rpcGetLoad()
	disk, _ := rpcGetDisk()
	gpu, _ := rpcGetGPU()

	return &ServerInfo{
		Hostname: hostname,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		CPUs:     runtime.NumCPU(),
		Memory:   mem,
		Load:     load,
		Disk:     disk,
		GPU:      gpu,
	}
}

// HandleInfo returns server info as JSON
func HandleInfo() []byte {
	info := GetServerInfo()
	data, _ := json.MarshalIndent(info, "", "  ")
	return data
}

// DaemonVersion is set by main at startup so RPCs can report the running build.
var DaemonVersion string

// daemonStart approximates the daemon process start time (package init).
var daemonStart = time.Now()

// NodeInfo is a consolidated daemon/identity snapshot returned by node_info.
type NodeInfo struct {
	Version       string `json:"version"`
	GoVersion     string `json:"go_version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Hostname      string `json:"hostname"`
	PID           int    `json:"pid"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	RequireVAUTH  bool   `json:"require_vauth"`
	RequireTLS    bool   `json:"require_tls"`
	AuthModel     string `json:"auth_model"`
}

func rpcNodeInfo() (*NodeInfo, error) {
	host, _ := os.Hostname()
	return &NodeInfo{
		Version:       DaemonVersion,
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Hostname:      host,
		PID:           os.Getpid(),
		UptimeSeconds: int64(time.Since(daemonStart).Seconds()),
		RequireVAUTH:  envEnabled("VSSH_REQUIRE_VAUTH"),
		RequireTLS:    envEnabled("VSSH_REQUIRE_TLS"),
		AuthModel:     "key-only (Ed25519 VAUTH1)",
	}, nil
}

func envEnabled(name string) bool {
	v := strings.TrimSpace(os.Getenv(name))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}
