package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func getRouteRequest(args map[string]interface{}) RouteRequest {
	return RouteRequest{
		RequiredCapabilities: getStringList(args, "required_capabilities", nil),
		PreferredTags:        getStringList(args, "preferred_tags", nil),
		AvoidHealth:          getStringList(args, "avoid_health", []string{"offline", "degraded"}),
		Target:               getString(args, "target"),
		IncludeHealth:        getBool(args, "include_health", false),
		HealthTimeoutSeconds: getFloat(args, "health_timeout_seconds", 1),
	}
}

func toolRouteSelect(args map[string]interface{}) map[string]interface{} {
	request := getRouteRequest(args)
	decision := selectRoute(request)
	return routeDecisionMap(decision)
}

func toolExecRouted(args map[string]interface{}) map[string]interface{} {
	startedAt := time.Now().UTC()
	command := getString(args, "command")
	if command == "" {
		return map[string]interface{}{
			"success": false,
			"tool":    "vssh_exec_routed",
			"error": map[string]interface{}{
				"code":    "missing_argument",
				"message": "command is required",
			},
		}
	}

	request := getRouteRequest(args)
	decision := selectRoute(request)
	if !decision.Success || decision.Selected == "" {
		endedAt := time.Now().UTC()
		return map[string]interface{}{
			"success":     false,
			"tool":        "vssh_exec_routed",
			"command":     command,
			"started_at":  startedAt.Format(time.RFC3339Nano),
			"ended_at":    endedAt.Format(time.RFC3339Nano),
			"duration_ms": endedAt.Sub(startedAt).Milliseconds(),
			"route":       decision,
			"error": map[string]interface{}{
				"code":      "route_not_found",
				"message":   decision.Reason,
				"retryable": true,
			},
		}
	}

	execResult := toolExec(decision.Selected, command, getFloat(args, "timeout_seconds", 30), getBool(args, "allow_dangerous", false))
	execResult["tool"] = "vssh_exec_routed"
	execResult["route"] = decision
	execResult["routed_target"] = decision.Selected
	return execResult
}

func selectRoute(request RouteRequest) RouteDecision {
	hosts, err := listHostRecords(hostRecordOptions{
		IncludeHealth: request.IncludeHealth,
		HealthTimeout: time.Duration(request.HealthTimeoutSeconds * float64(time.Second)),
	})
	if err != nil {
		return RouteDecision{
			Success: false,
			Reason:  fmt.Sprintf("failed to list hosts: %v", err),
			Request: request,
			Error: map[string]interface{}{
				"code":    "connector_error",
				"message": fmt.Sprintf("%v", err),
			},
		}
	}

	candidates := make([]RouteCandidate, 0, len(hosts))
	for _, host := range hosts {
		candidate := scoreRouteCandidate(host, request)
		if request.Target != "" && !strings.EqualFold(candidate.Name, request.Target) {
			candidate.Score -= 10000
			candidate.Reasons = append(candidate.Reasons, "not explicit target")
		}
		candidates = append(candidates, candidate)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].Name < candidates[j].Name
		}
		return candidates[i].Score > candidates[j].Score
	})

	if len(candidates) == 0 {
		return RouteDecision{
			Success:    false,
			Reason:     "no hosts available",
			Request:    request,
			Candidates: candidates,
		}
	}
	best := candidates[0]
	if len(best.Missing) > 0 || best.Score < 0 {
		return RouteDecision{
			Success:    false,
			Reason:     "no host satisfies route constraints",
			Request:    request,
			Candidates: candidates,
		}
	}

	candidates[0].Selected = true
	return RouteDecision{
		Success:    true,
		Selected:   best.Name,
		Reason:     strings.Join(best.Reasons, "; "),
		Request:    request,
		Candidates: candidates,
	}
}

func scoreRouteCandidate(host map[string]interface{}, request RouteRequest) RouteCandidate {
	name := fmt.Sprint(host["name"])
	caps := getStringSliceFromHost(host, "capabilities")
	tags := getStringSliceFromHost(host, "tags")
	health := getHealthStatus(host)
	score := 0
	reasons := []string{}
	missing := []string{}

	for _, capability := range request.RequiredCapabilities {
		if containsNormalized(caps, capability) {
			score += 100
			reasons = append(reasons, "has capability "+capability)
		} else {
			score -= 1000
			missing = append(missing, capability)
			reasons = append(reasons, "missing capability "+capability)
		}
	}

	for _, tag := range request.PreferredTags {
		if containsNormalized(tags, tag) {
			score += 10
			reasons = append(reasons, "has preferred tag "+tag)
		}
	}

	switch health {
	case "online":
		score += 20
		reasons = append(reasons, "health online")
	case "unknown":
		reasons = append(reasons, "health unknown")
	case "stale":
		score -= 10
		reasons = append(reasons, "health stale")
	case "degraded":
		score -= 50
		reasons = append(reasons, "health degraded")
	case "offline":
		score -= 1000
		reasons = append(reasons, "health offline")
	}

	for _, avoid := range request.AvoidHealth {
		if strings.EqualFold(health, avoid) {
			score -= 1000
			reasons = append(reasons, "avoids health "+avoid)
		}
	}

	return RouteCandidate{
		Name:         name,
		Score:        score,
		Reasons:      reasons,
		Missing:      missing,
		Health:       health,
		Tags:         tags,
		Capabilities: caps,
		Host:         host,
	}
}

func routeDecisionMap(decision RouteDecision) map[string]interface{} {
	payload := map[string]interface{}{
		"success":    decision.Success,
		"selected":   decision.Selected,
		"reason":     decision.Reason,
		"request":    decision.Request,
		"candidates": decision.Candidates,
	}
	if decision.Error != nil {
		payload["error"] = decision.Error
	}
	return payload
}

func getStringSliceFromHost(host map[string]interface{}, key string) []string {
	raw, ok := host[key]
	if !ok || raw == nil {
		return []string{}
	}
	switch values := raw.(type) {
	case []string:
		return normalizeList(values)
	case []interface{}:
		out := []string{}
		for _, value := range values {
			if s, ok := value.(string); ok {
				out = append(out, s)
			}
		}
		return normalizeList(out)
	default:
		return []string{}
	}
}

func getHealthStatus(host map[string]interface{}) string {
	raw, ok := host["health"].(map[string]interface{})
	if !ok {
		return "unknown"
	}
	status, ok := raw["status"].(string)
	if !ok || strings.TrimSpace(status) == "" {
		return "unknown"
	}
	return strings.ToLower(status)
}

func containsNormalized(values []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == want {
			return true
		}
	}
	return false
}
