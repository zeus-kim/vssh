package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeus-kim/vssh/internal/server"
)

type multiRPCResult struct {
	Target string              `json:"target"`
	Result *server.RPCResponse `json:"result,omitempty"`
	Error  string              `json:"error,omitempty"`
}

type multiFactsResult struct {
	Target string             `json:"target"`
	Result *server.ServerInfo `json:"result,omitempty"`
	Error  string             `json:"error,omitempty"`
}

func cmdRPC(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: vssh rpc <host[:port]> <method> [json-params]")
		os.Exit(1)
	}
	target := args[0]
	method := args[1]
	params := parseJSONParams(args[2:])
	resp, err := callRPCNative(target, method, params, 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	writeJSON(resp)
	// Honest exit code: a structured RPC failure (e.g. unsupported_method) must
	// not exit 0, or agents/scripts branching on $? treat it as success.
	if resp != nil && !resp.Success {
		os.Exit(1)
	}
}

func cmdRPCMany(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: vssh rpc-many <host1,host2> <method> [json-params]")
		os.Exit(1)
	}
	targets := splitTargets(args[0])
	method := args[1]
	params := parseJSONParams(args[2:])
	results := rpcMany(targets, method, params, 30*time.Second, defaultMaxParallelism())
	writeJSON(results)
}

func cmdFacts(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: vssh facts <host[:port]>")
		os.Exit(1)
	}
	host, port := parseHostPort(args[0])
	resolved := resolveReachableHost(host, port)
	info, err := server.GetInfo(resolved, port, getSecret(), 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	writeJSON(info)
}

func cmdFactsMany(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: vssh facts-many <host1,host2>")
		os.Exit(1)
	}
	results := factsMany(splitTargets(args[0]), 30*time.Second, defaultMaxParallelism())
	writeJSON(results)
}

func cmdJobStart(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: vssh job-start <host[:port]> <command>")
		os.Exit(1)
	}
	resp, err := callRPCNative(args[0], "job_start", map[string]interface{}{"command": strings.Join(args[1:], " ")}, 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	writeJSON(resp)
}

func cmdJobStatus(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: vssh job-status <host[:port]> <id>")
		os.Exit(1)
	}
	writeJobRPC(args[0], "job_status", args[1])
}

func cmdJobLogs(args []string) {
	if len(args) < 2 || len(args) > 3 {
		fmt.Fprintln(os.Stderr, "Usage: vssh job-logs <host[:port]> <id> [tail-bytes]")
		os.Exit(1)
	}
	params := map[string]interface{}{"id": args[1]}
	if len(args) == 3 {
		if n, err := strconv.Atoi(args[2]); err == nil {
			params["tail_bytes"] = n
		}
	}
	resp, err := callRPCNative(args[0], "job_logs", params, 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	writeJSON(resp)
}

func cmdJobCancel(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: vssh job-cancel <host[:port]> <id>")
		os.Exit(1)
	}
	writeJobRPC(args[0], "job_cancel", args[1])
}

func cmdArtifactCollect(args []string) {
	if len(args) < 2 || len(args) > 3 {
		fmt.Fprintln(os.Stderr, "Usage: vssh artifact-collect <host[:port]> <path> [max-bytes]")
		os.Exit(1)
	}
	params := map[string]interface{}{"path": args[1]}
	if len(args) == 3 {
		if n, err := strconv.Atoi(args[2]); err == nil {
			params["max_bytes"] = n
		}
	}
	resp, err := callRPCNative(args[0], "artifact_collect", params, 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	writeJSON(resp)
}

func writeJobRPC(target, method, id string) {
	resp, err := callRPCNative(target, method, map[string]interface{}{"id": id}, 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	writeJSON(resp)
}

func parseJSONParams(parts []string) map[string]interface{} {
	if len(parts) == 0 {
		return map[string]interface{}{}
	}
	raw := strings.Join(parts, " ")
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid JSON params: %v\n", err)
		os.Exit(1)
	}
	return params
}

func callRPCNative(target, method string, params map[string]interface{}, timeout time.Duration) (*server.RPCResponse, error) {
	host, port := parseHostPort(target)
	host = resolveReachableHost(host, port)
	resp, err := server.CallRPC(host, port, getSecret(), method, params, timeout)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// decodeRPCResult re-decodes an RPCResponse.Result (generic JSON) into a typed
// value. Returns an error when the RPC itself failed.
func decodeRPCResult(resp *server.RPCResponse, out interface{}) error {
	if resp == nil {
		return fmt.Errorf("nil rpc response")
	}
	if !resp.Success {
		if resp.Error != "" {
			return fmt.Errorf("%s", resp.Error)
		}
		return fmt.Errorf("rpc failed")
	}
	b, err := json.Marshal(resp.Data)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func rpcMany(targets []string, method string, params map[string]interface{}, timeout time.Duration, maxParallelism int) []multiRPCResult {
	results := make([]multiRPCResult, len(targets))
	sem := make(chan struct{}, normalizedMaxParallelism(maxParallelism, len(targets)))
	var wg sync.WaitGroup
	for i, target := range targets {
		i, target := i, target
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			resp, err := callRPCNative(target, method, params, timeout)
			results[i].Target = target
			if err != nil {
				results[i].Error = err.Error()
				return
			}
			results[i].Result = resp
		}()
	}
	wg.Wait()
	return results
}

func factsMany(targets []string, timeout time.Duration, maxParallelism int) []multiFactsResult {
	results := make([]multiFactsResult, len(targets))
	sem := make(chan struct{}, normalizedMaxParallelism(maxParallelism, len(targets)))
	var wg sync.WaitGroup
	for i, target := range targets {
		i, target := i, target
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			host, port := parseHostPort(target)
			host = resolveReachableHost(host, port)
			info, err := server.GetInfo(host, port, getSecret(), timeout)
			results[i].Target = target
			if err != nil {
				results[i].Error = err.Error()
				return
			}
			results[i].Result = info
		}()
	}
	wg.Wait()
	return results
}
