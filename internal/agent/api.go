package agent

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

func (a *Agent) startHTTP(ctx context.Context) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", a.handleStatus)
	mux.HandleFunc("/health", a.handleHealth)
	mux.HandleFunc("/metrics", a.handleMetrics)

	server := &http.Server{
		Addr:    a.cfg.HTTPAddr,
		Handler: mux,
	}

	go func() {
		log.Printf("[vssh] HTTP API listening on %s", a.cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[vssh] HTTP error: %v", err)
		}
	}()

	return server
}

func (a *Agent) handleStatus(w http.ResponseWriter, r *http.Request) {
	stats := a.GetStats()

	type nodeStatus struct {
		Name         string  `json:"name"`
		Attempts     int     `json:"attempts"`
		Successes    int     `json:"successes"`
		Failures     int     `json:"failures"`
		SuccessRate  float64 `json:"success_rate"`
		AvgLatencyMs float64 `json:"avg_latency_ms"`
		Timeouts     int     `json:"timeouts"`
		BestPath     string  `json:"best_path"`
		LastSuccess  string  `json:"last_success,omitempty"`
	}

	result := make([]nodeStatus, 0, len(stats))
	for _, s := range stats {
		rate := 0.0
		if s.Attempts > 0 {
			rate = float64(s.Successes) / float64(s.Attempts)
		}
		ns := nodeStatus{
			Name:         s.Name,
			Attempts:     s.Attempts,
			Successes:    s.Successes,
			Failures:     s.Failures,
			SuccessRate:  rate,
			AvgLatencyMs: s.AvgLatencyMs,
			Timeouts:     s.Timeouts,
			BestPath:     s.BestPath,
		}
		if !s.LastSuccess.IsZero() {
			ns.LastSuccess = s.LastSuccess.Format(time.RFC3339)
		}
		result = append(result, ns)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"node":  a.node,
		"nodes": result,
	})
}

func (a *Agent) handleHealth(w http.ResponseWriter, r *http.Request) {
	healthy := a.IsHealthy()
	lastRun := a.LastRun()

	status := "ok"
	if !healthy {
		status = "degraded"
	}
	if time.Since(lastRun) > 2*a.cfg.Interval {
		status = "stale"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   status,
		"healthy":  healthy,
		"last_run": lastRun.Format(time.RFC3339),
	})
}

func (a *Agent) handleMetrics(w http.ResponseWriter, r *http.Request) {
	stats := a.GetStats()

	var totalAttempts, totalSuccesses, totalFailures, totalTimeouts int

	nodeMetrics := make(map[string]float64)
	for name, s := range stats {
		totalAttempts += s.Attempts
		totalSuccesses += s.Successes
		totalFailures += s.Failures
		totalTimeouts += s.Timeouts

		if s.Attempts > 0 {
			nodeMetrics[name] = float64(s.Successes) / float64(s.Attempts)
		}
	}

	overallRate := 0.0
	if totalAttempts > 0 {
		overallRate = float64(totalSuccesses) / float64(totalAttempts)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_attempts":       totalAttempts,
		"total_successes":      totalSuccesses,
		"total_failures":       totalFailures,
		"total_timeouts":       totalTimeouts,
		"overall_success_rate": overallRate,
		"node_success_rates":   nodeMetrics,
	})
}
