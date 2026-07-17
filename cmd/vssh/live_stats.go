package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeus-kim/vssh/internal/config"
	"github.com/zeus-kim/vssh/internal/server"
	"github.com/zeus-kim/vssh/internal/ssh"
)

// liveStatsForPeers polls each peer's vssh daemon over RPC for the CURRENT
// load / memory / disk / uptime and returns fresh PeerStats keyed by node name.
// Peers whose daemon does not answer are omitted, so callers keep whatever
// (cached) stats they already had. This lets the dashboard override the
// possibly-stale node_monitor source with a live reading at display time.
func liveStatsForPeers(conn *ssh.Connector, timeout time.Duration) map[string]*config.PeerStats {
	peers := conn.ListPeers()
	out := make(map[string]*config.PeerStats, len(peers))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for _, p := range peers {
		name := strings.TrimSpace(p.NodeName)
		target := strings.TrimSpace(p.VpnIP)
		if name == "" || target == "" {
			continue
		}
		// Skip nodes already known offline — probing them only burns the full
		// timeout for no data (they keep their cached/empty stats).
		if p.Online != nil && !*p.Online {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(name, target string) {
			defer wg.Done()
			defer func() { <-sem }()
			if st := probeLiveStats(target, timeout); st != nil {
				mu.Lock()
				out[name] = st
				mu.Unlock()
			}
		}(name, target)
	}
	wg.Wait()
	return out
}

// probeLiveStats returns live PeerStats for one node via daemon RPC, or nil if
// the node's daemon answered none of the probes (treated as unreachable, so the
// caller keeps cached values). The four stat RPCs run concurrently so a node
// costs one round-trip, not four — important for far/relayed nodes.
func probeLiveStats(target string, timeout time.Duration) *config.PeerStats {
	type res struct {
		method string
		resp   *server.RPCResponse
		err    error
	}
	methods := []string{"get_load", "get_memory", "get_disk", "node_info"}
	ch := make(chan res, len(methods))
	for _, m := range methods {
		go func(m string) {
			r, e := callRPCNative(target, m, nil, timeout)
			ch <- res{m, r, e}
		}(m)
	}
	got := make(map[string]*server.RPCResponse, len(methods))
	for range methods {
		r := <-ch
		if r.err == nil {
			got[r.method] = r.resp
		}
	}
	if len(got) == 0 {
		return nil
	}
	st := &config.PeerStats{UpdatedAt: time.Now().Unix()}
	if r := got["get_load"]; r != nil {
		var li server.LoadInfo
		if decodeRPCResult(r, &li) == nil {
			st.LoadValue = li.Load1
			st.Load = fmt.Sprintf("%.2f", li.Load1)
		}
	}
	if r := got["get_memory"]; r != nil {
		var mi server.MemoryInfo
		if decodeRPCResult(r, &mi) == nil && mi.Total > 0 {
			st.MemPct = int(mi.Used * 100 / mi.Total)
		}
	}
	if r := got["get_disk"]; r != nil {
		var di []server.DiskInfo
		if decodeRPCResult(r, &di) == nil {
			if pct, ok := rootDiskPct(di); ok {
				st.DiskPct = pct
			}
		}
	}
	if r := got["node_info"]; r != nil {
		var ni server.NodeInfo
		if decodeRPCResult(r, &ni) == nil && ni.UptimeSeconds > 0 {
			st.Uptime = formatUptime(ni.UptimeSeconds)
		}
	}
	return st
}

// rootDiskPct returns the usage percent of the root ("/") filesystem, falling
// back to the first entry when "/" is absent.
func rootDiskPct(disks []server.DiskInfo) (int, bool) {
	if len(disks) == 0 {
		return 0, false
	}
	pick := disks[0]
	for _, d := range disks {
		if d.MountPoint == "/" {
			pick = d
			break
		}
	}
	s := strings.TrimSuffix(strings.TrimSpace(pick.UsePercent), "%")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// formatUptime renders seconds as "N days, H:MM" to match the dashboard style.
func formatUptime(sec int64) string {
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	return fmt.Sprintf("%d days, %d:%02d", d, h, m)
}
