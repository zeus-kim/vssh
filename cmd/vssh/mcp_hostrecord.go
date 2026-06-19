package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/zeus-kim/vssh/internal/config"
	"github.com/zeus-kim/vssh/internal/ssh"
)

type hostRecordOptions struct {
	IncludeHealth bool
	HealthTimeout time.Duration
}

func listHostRecords(opts hostRecordOptions) ([]map[string]interface{}, error) {
	connector, err := ssh.NewConnector("default")
	if err != nil {
		return nil, err
	}

	peers := connector.ListPeers()
	hostList := []map[string]interface{}{}
	for _, p := range peers {
		if p.NodeName == "" {
			continue
		}
		hostList = append(hostList, buildHostRecordWithOptions(p, opts))
	}
	sort.Slice(hostList, func(i, j int) bool {
		return fmt.Sprint(hostList[i]["name"]) < fmt.Sprint(hostList[j]["name"])
	})
	return hostList, nil
}

func buildHostRecord(p config.Peer) map[string]interface{} {
	return buildHostRecordWithOptions(p, hostRecordOptions{})
}

func buildHostRecordWithOptions(p config.Peer, opts hostRecordOptions) map[string]interface{} {
	tags := normalizeList(append(append([]string{}, p.Tags...), p.Roles...))
	capabilities := normalizeList(p.Capabilities)
	hints := builtInHostHints(p.NodeName)
	tags = mergeLists(tags, hints.Tags)
	capabilities = mergeLists(capabilities, hints.Capabilities)
	capabilities = mergeLists(capabilities, inferCapabilities(p, tags))
	osName := strings.ToLower(strings.TrimSpace(p.OS))
	if osName != "" {
		tags = mergeLists(tags, []string{osName})
	}

	record := map[string]interface{}{
		"name":         p.NodeName,
		"node_id":      p.NodeID,
		"addresses":    buildAddressRecord(p),
		"vpn_ip":       p.VpnIP,
		"public_ip":    p.PublicIP,
		"lan_ip":       p.LanIP,
		"port":         p.Port,
		"user":         p.User,
		"os":           p.OS,
		"arch":         p.Arch,
		"tags":         tags,
		"capabilities": capabilities,
		"health":       buildHealthRecord(p),
		"stats":        p.Stats,
		"metadata":     p.Metadata,
	}
	if endpoint := monitorEndpoint(p); endpoint != "" {
		record["monitor"] = map[string]interface{}{
			"url": endpoint,
		}
	}
	if opts.IncludeHealth {
		record["health"] = mergeLiveHealth(record["health"].(map[string]interface{}), fetchLiveMonitorHealth(p, opts.HealthTimeout))
	}
	return record
}

type hostHints struct {
	Tags         []string
	Capabilities []string
}

func builtInHostHints(name string) hostHints {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "d1":
		return hostHints{Tags: []string{"linux", "gpu", "ai-workload"}, Capabilities: []string{"cuda", "docker", "ollama"}}
	case "d2":
		return hostHints{Tags: []string{"linux", "gpu", "docker-host"}, Capabilities: []string{"cuda", "docker", "ollama"}}
	case "g1", "g2", "g3", "g4":
		return hostHints{Tags: []string{"linux", "gpu", "docker-host"}, Capabilities: []string{"cuda", "docker", "ollama"}}
	case "v1", "v2", "v3", "v4", "v5", "c1", "c2":
		return hostHints{Tags: []string{"linux", "vps"}, Capabilities: []string{"server"}}
	case "macmini", "m1":
		return hostHints{Tags: []string{"macos", "client"}, Capabilities: []string{"local-shell"}}
	case "s2":
		return hostHints{Tags: []string{"synology", "nas"}, Capabilities: []string{"storage"}}
	default:
		return hostHints{}
	}
}

func buildAddressRecord(p config.Peer) map[string]interface{} {
	addresses := map[string]interface{}{}
	if p.VpnIP != "" {
		addresses["vpn"] = p.VpnIP
	}
	if p.LanIP != "" {
		addresses["lan"] = p.LanIP
	}
	if p.PublicIP != "" {
		addresses["public"] = p.PublicIP
	}
	return addresses
}

func buildHealthRecord(p config.Peer) map[string]interface{} {
	now := time.Now().Unix()
	status := "unknown"
	reason := "no live health signal"
	var lastSeenUnix int64
	var lastSeen string

	if p.Online != nil {
		if *p.Online {
			status = "online"
			reason = "provider reported online"
		} else {
			status = "offline"
			reason = "provider reported offline"
		}
	}

	if p.LastSeen != nil {
		switch v := p.LastSeen.(type) {
		case float64:
			lastSeenUnix = int64(v)
		case int64:
			lastSeenUnix = v
		case int:
			lastSeenUnix = int64(v)
		case string:
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				lastSeenUnix = t.Unix()
				lastSeen = t.UTC().Format(time.RFC3339)
			}
		}
		if lastSeenUnix > 0 {
			age := now - lastSeenUnix
			if age < 0 {
				age = 0
			}
			if lastSeen == "" {
				lastSeen = time.Unix(lastSeenUnix, 0).UTC().Format(time.RFC3339)
			}
			if age < 60 {
				status = "online"
				reason = "last_seen is fresh"
			} else if status == "unknown" {
				status = "stale"
				reason = "last_seen is stale"
			}
		}
	}

	if p.Stats != nil && (p.Stats.DiskPct >= 90 || p.Stats.MemPct >= 95) && status == "online" {
		status = "degraded"
		reason = "resource pressure detected"
	}

	record := map[string]interface{}{
		"status": status,
		"reason": reason,
	}
	if lastSeen != "" {
		record["last_seen"] = lastSeen
		record["last_seen_age_sec"] = now - lastSeenUnix
	}
	if p.Online != nil {
		record["provider_online"] = *p.Online
	}
	return record
}

func monitorEndpoint(p config.Peer) string {
	if p.MonitorURL != "" {
		return strings.TrimRight(p.MonitorURL, "/")
	}
	port := p.MonitorPort
	if port == 0 {
		return ""
	}
	host := p.VpnIP
	if host == "" {
		host = p.LanIP
	}
	if host == "" {
		host = p.PublicIP
	}
	if host == "" {
		return ""
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

func fetchLiveMonitorHealth(p config.Peer, timeout time.Duration) map[string]interface{} {
	endpoint := monitorEndpoint(p)
	if endpoint == "" {
		return map[string]interface{}{
			"live_checked": false,
			"source":       "node_monitor",
			"error": map[string]interface{}{
				"code":    "monitor_not_configured",
				"message": "monitor_url or monitor_port is not configured",
			},
		}
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	client := &http.Client{Timeout: timeout}
	url := endpoint + "/api/node/status"
	started := time.Now()
	resp, err := client.Get(url)
	duration := time.Since(started).Milliseconds()
	if err != nil {
		return map[string]interface{}{
			"live_checked": true,
			"source":       "node_monitor",
			"endpoint":     url,
			"duration_ms":  duration,
			"status":       "offline",
			"reason":       "monitor endpoint unreachable",
			"error": map[string]interface{}{
				"code":      "monitor_unreachable",
				"message":   err.Error(),
				"retryable": true,
			},
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return map[string]interface{}{
			"live_checked": true,
			"source":       "node_monitor",
			"endpoint":     url,
			"duration_ms":  duration,
			"status":       "degraded",
			"reason":       fmt.Sprintf("monitor endpoint returned HTTP %d", resp.StatusCode),
			"http_status":  resp.StatusCode,
		}
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return map[string]interface{}{
			"live_checked": true,
			"source":       "node_monitor",
			"endpoint":     url,
			"duration_ms":  duration,
			"status":       "degraded",
			"reason":       "monitor payload is not valid JSON",
			"error": map[string]interface{}{
				"code":      "monitor_invalid_json",
				"message":   err.Error(),
				"retryable": true,
			},
		}
	}

	status := "online"
	reason := "node monitor responded"
	cpuPct := numberFromMap(payload, "cpu_pct")
	memPct := numberFromMap(payload, "mem_pct")
	diskPct := numberFromMap(payload, "disk_pct")
	if cpuPct >= 95 || memPct >= 95 || diskPct >= 95 {
		status = "degraded"
		reason = "critical resource pressure from node monitor"
	} else if cpuPct >= 85 || memPct >= 90 || diskPct >= 90 {
		status = "degraded"
		reason = "resource pressure from node monitor"
	}

	result := map[string]interface{}{
		"live_checked": true,
		"source":       "node_monitor",
		"endpoint":     url,
		"duration_ms":  duration,
		"status":       status,
		"reason":       reason,
		"node":         payload["node"],
		"ip":           payload["ip"],
		"os":           payload["os"],
		"arch":         payload["arch"],
		"cpu_pct":      cpuPct,
		"mem_pct":      memPct,
		"disk_pct":     diskPct,
		"load":         payload["load"],
		"uptime":       payload["uptime"],
		"ts":           payload["ts"],
	}
	if gpu, ok := payload["gpu"]; ok {
		result["gpu"] = gpu
	}
	if docker, ok := payload["docker"]; ok {
		result["docker"] = docker
	}
	if alerts, ok := payload["alerts"]; ok {
		result["alerts"] = alerts
	}
	return result
}

func mergeLiveHealth(base map[string]interface{}, live map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range live {
		out[key] = value
	}
	return out
}

func numberFromMap(payload map[string]interface{}, key string) float64 {
	switch value := payload[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		n, _ := value.Float64()
		return n
	default:
		return 0
	}
}

func inferCapabilities(p config.Peer, tags []string) []string {
	values := append([]string{}, tags...)
	name := strings.ToLower(p.NodeName)
	osName := strings.ToLower(p.OS)
	if name != "" {
		values = append(values, name)
	}
	if osName != "" {
		values = append(values, osName)
	}

	var caps []string
	for _, value := range values {
		switch {
		case value == "gpu" || value == "cuda" || value == "nvidia" || strings.Contains(value, "rtx"):
			caps = append(caps, "gpu", "cuda")
		case value == "ollama" || strings.Contains(value, "llm"):
			caps = append(caps, "ollama")
		case value == "browser" || value == "chrome" || value == "safari":
			caps = append(caps, "browser")
		case value == "controller":
			caps = append(caps, "controller")
		case value == "mail" || value == "smtp" || value == "mox":
			caps = append(caps, "mail")
		case value == "docker":
			caps = append(caps, "docker")
		case value == "darwin" || value == "mac" || strings.Contains(value, "mac"):
			caps = append(caps, "macos")
		case value == "linux" || value == "ubuntu" || value == "debian":
			caps = append(caps, "linux")
		}
	}
	return normalizeList(caps)
}

func normalizeList(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func mergeLists(a, b []string) []string {
	return normalizeList(append(append([]string{}, a...), b...))
}
