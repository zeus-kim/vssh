package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zeus-kim/vssh/internal/event"
	"gopkg.in/yaml.v3"
)

// defaultConfigPath returns the default config path (~/.vssh/vssh.yaml)
func defaultConfigPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".vssh", "vssh.yaml")
}

// Config holds agent configuration
type Config struct {
	Interval     time.Duration `yaml:"interval"`
	EventLogPath string        `yaml:"event_log_path"`
	HTTPAddr     string        `yaml:"http_addr"`
}

// NodeConfig holds per-node IP mapping
type NodeConfig struct {
	Name      string `yaml:"name"`
	WireIP    string `yaml:"wire_ip"`
	LanIP     string `yaml:"lan_ip"`
	PublicIP  string `yaml:"public_ip"`
	Tailscale string `yaml:"tailscale"`
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		Interval:     10 * time.Second,
		EventLogPath: event.DefaultLocalLogPath(),
		HTTPAddr:     "127.0.0.1:18701",
	}
}

// LoadConfig loads config from file or returns default
func LoadConfig(path string) *Config {
	cfg := DefaultConfig()
	if path == "" {
		path = defaultConfigPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	var fileCfg struct {
		Interval     string `yaml:"interval"`
		EventLogPath string `yaml:"event_log_path"`
		HTTPAddr     string `yaml:"http_addr"`
	}
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return cfg
	}
	if fileCfg.Interval != "" {
		if d, err := time.ParseDuration(fileCfg.Interval); err == nil {
			cfg.Interval = d
		}
	}
	if fileCfg.EventLogPath != "" {
		cfg.EventLogPath = fileCfg.EventLogPath
	}
	if fileCfg.HTTPAddr != "" {
		cfg.HTTPAddr = fileCfg.HTTPAddr
	}
	return cfg
}

// loadNodes loads node IP mappings from config
func loadNodes() []NodeConfig {
	data, err := os.ReadFile(defaultConfigPath())
	if err != nil {
		return nil
	}
	var cfg struct {
		Nodes []NodeConfig `yaml:"nodes"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return cfg.Nodes
}

// NodeStats tracks per-node connection statistics
type NodeStats struct {
	Name         string
	Attempts     int
	Successes    int
	Failures     int
	LastAttempt  time.Time
	LastSuccess  time.Time
	AvgLatencyMs float64
	Timeouts     int
	BestPath     string // wire, lan, public, tailscale
}

// Agent runs the vssh monitoring loop
type Agent struct {
	cfg         *Config
	eventLog    *event.EventLog
	node        string
	mu          sync.RWMutex
	stats       map[string]*NodeStats
	healthy     bool
	lastRun     time.Time
	httpServer  *http.Server
	unreachable map[string]bool // hints from peer_state_change events
	knownNodes  []NodeConfig
}

// New creates a new agent
func New(cfg *Config) *Agent {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	hostname, _ := os.Hostname()
	return &Agent{
		cfg:         cfg,
		eventLog:    event.NewEventLog(cfg.EventLogPath),
		node:        hostname,
		stats:       make(map[string]*NodeStats),
		healthy:     true,
		unreachable: make(map[string]bool),
		knownNodes:  loadNodes(),
	}
}

// Run starts the agent loop
func (a *Agent) Run(ctx context.Context) error {
	log.Printf("[vssh] starting on %s, interval=%v", a.node, a.cfg.Interval)

	a.httpServer = a.startHTTP(ctx)

	a.publish(event.TypeAgentLifecycle, event.LifecyclePayload{
		Action: "start",
		PID:    os.Getpid(),
	})

	ticker := time.NewTicker(a.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.publish(event.TypeAgentLifecycle, event.LifecyclePayload{Action: "stop"})
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if a.httpServer != nil {
				a.httpServer.Shutdown(shutdownCtx)
			}
			return ctx.Err()
		case <-ticker.C:
			a.tick()
		}
	}
}

func (a *Agent) tick() {
	a.mu.Lock()
	a.lastRun = time.Now()
	a.mu.Unlock()

	// Collect -> Evaluate -> Act
	a.collectState()
	problems := a.evaluate()
	a.act(problems) // actions logged internally, no event

	a.mu.Lock()
	a.healthy = len(problems) == 0
	a.mu.Unlock()
}

// collectState probes known nodes
func (a *Agent) collectState() {
	// Read hints from event log
	a.readEventHints()

	// Probe known nodes via TCP ping
	for _, node := range a.knownNodes {
		if node.Name == a.node {
			continue // skip self
		}

		// Check if hinted as unreachable
		a.mu.RLock()
		unreachable := a.unreachable[node.Name]
		a.mu.RUnlock()

		start := time.Now()
		reachable := false
		bestPath := ""
		var errMsg string

		// Try paths in order: wire -> lan -> public -> tailscale
		if !unreachable && node.WireIP != "" && a.canReach(node.WireIP, 22) {
			reachable = true
			bestPath = "wire"
		} else if node.LanIP != "" && a.canReach(node.LanIP, 22) {
			reachable = true
			bestPath = "lan"
		} else if node.PublicIP != "" && a.canReach(node.PublicIP, 22) {
			reachable = true
			bestPath = "public"
		} else if node.Tailscale != "" && a.canReach(node.Tailscale, 22) {
			reachable = true
			bestPath = "tailscale"
		} else {
			errMsg = "all paths failed"
		}

		latencyMs := time.Since(start).Milliseconds()

		// Publish probe_result event
		a.publish(event.TypeProbeResult, event.ProbeResultPayload{
			Target:    node.Name,
			Path:      bestPath,
			LatencyMs: latencyMs,
			Success:   reachable,
			Error:     errMsg,
		})

		a.mu.Lock()
		s, ok := a.stats[node.Name]
		if !ok {
			s = &NodeStats{Name: node.Name}
			a.stats[node.Name] = s
		}
		s.Attempts++
		s.LastAttempt = time.Now()
		if reachable {
			s.Successes++
			s.LastSuccess = time.Now()
			s.BestPath = bestPath
			s.AvgLatencyMs = (s.AvgLatencyMs*float64(s.Successes-1) + float64(latencyMs)) / float64(s.Successes)
		} else {
			s.Failures++
			s.Timeouts++
		}
		a.mu.Unlock()
	}
}

// readEventHints reads peer_state_change events for hints
func (a *Agent) readEventHints() {
	since := time.Now().Add(-5 * time.Minute).UnixMilli()
	events, err := a.eventLog.ReadSince(since)
	if err != nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Clear old hints
	a.unreachable = make(map[string]bool)

	for _, ev := range events {
		if ev.Type == event.TypePeerStateChange {
			// Unmarshal payload
			var payload map[string]interface{}
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				continue
			}
			// If stale or not connected, mark as unreachable via wire
			if stale, _ := payload["stale"].(bool); stale {
				if pubkey, ok := payload["public_key"].(string); ok {
					a.unreachable[pubkey] = true
				}
			}
			if connected, ok := payload["connected"].(bool); !ok || !connected {
				if pubkey, ok := payload["public_key"].(string); ok {
					a.unreachable[pubkey] = true
				}
			}
		}
	}
}

// evaluate detects problematic nodes
func (a *Agent) evaluate() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var problems []string
	for name, s := range a.stats {
		if s.Attempts > 5 {
			successRate := float64(s.Successes) / float64(s.Attempts)
			if successRate < 0.5 {
				problems = append(problems, name)
			}
		}
		if s.Timeouts > 3 {
			problems = append(problems, name)
		}
	}
	return problems
}

// act takes remediation actions
func (a *Agent) act(problems []string) []string {
	var actions []string
	for _, node := range problems {
		// Try alternative path
		newPath := a.findBestPath(node)
		if newPath != "" {
			a.mu.Lock()
			if s, ok := a.stats[node]; ok {
				s.BestPath = newPath
			}
			a.mu.Unlock()
			actions = append(actions, "switch_path:"+node+":"+newPath)
		}
	}
	return actions
}

// findBestPath determines best connection path for a node
// Fallback order: Wire VPN -> LAN -> Public -> Tailscale
func (a *Agent) findBestPath(nodeName string) string {
	// Find node config
	var nodeConf *NodeConfig
	for i := range a.knownNodes {
		if a.knownNodes[i].Name == nodeName {
			nodeConf = &a.knownNodes[i]
			break
		}
	}
	if nodeConf == nil {
		return "public"
	}

	// Check if hinted as unreachable via wire
	a.mu.RLock()
	wireUnreachable := a.unreachable[nodeName]
	a.mu.RUnlock()

	// Try paths in order
	if !wireUnreachable && nodeConf.WireIP != "" && a.canReach(nodeConf.WireIP, 22) {
		return "wire"
	}
	if nodeConf.LanIP != "" && a.canReach(nodeConf.LanIP, 22) {
		return "lan"
	}
	if nodeConf.PublicIP != "" && a.canReach(nodeConf.PublicIP, 22) {
		return "public"
	}
	if nodeConf.Tailscale != "" && a.canReach(nodeConf.Tailscale, 22) {
		return "tailscale"
	}
	return "public"
}

func (a *Agent) canReach(ip string, port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// RecordAttempt records a connection attempt
func (a *Agent) RecordAttempt(node string, success bool, latencyMs float64, timeout bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	s, ok := a.stats[node]
	if !ok {
		s = &NodeStats{Name: node}
		a.stats[node] = s
	}

	s.Attempts++
	s.LastAttempt = time.Now()

	if success {
		s.Successes++
		s.LastSuccess = time.Now()
		// Running average
		s.AvgLatencyMs = (s.AvgLatencyMs*float64(s.Successes-1) + latencyMs) / float64(s.Successes)
	} else {
		s.Failures++
		if timeout {
			s.Timeouts++
		}
	}

	// Publish probe result
	var errMsg string
	if !success {
		if timeout {
			errMsg = "timeout"
		} else {
			errMsg = "connection failed"
		}
	}
	a.publish(event.TypeProbeResult, event.ProbeResultPayload{
		Target:    node,
		Path:      s.BestPath,
		LatencyMs: int64(latencyMs),
		Success:   success,
		Error:     errMsg,
	})
}

func (a *Agent) publish(eventType string, payload interface{}) {
	ev, err := event.NewEvent(a.node, "vssh", eventType, payload)
	if err != nil {
		log.Printf("[vssh] event create error: %v", err)
		return
	}
	if err := a.eventLog.Append(ev); err != nil {
		log.Printf("[vssh] event log error: %v", err)
	}
}

// GetStats returns current node stats
func (a *Agent) GetStats() map[string]*NodeStats {
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := make(map[string]*NodeStats, len(a.stats))
	for k, v := range a.stats {
		cp := *v
		result[k] = &cp
	}
	return result
}

// IsHealthy returns agent health
func (a *Agent) IsHealthy() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.healthy
}

// LastRun returns last run time
func (a *Agent) LastRun() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastRun
}
