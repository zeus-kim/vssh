package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeus-kim/vssh/internal/server"
)

func cmdServer(args []string) {
	port := getPort()
	for i := 0; i < len(args); i++ {
		if args[i] == "-p" && i+1 < len(args) {
			if p, err := strconv.Atoi(args[i+1]); err == nil {
				port = p
			}
			i++
		}
	}

	// vssh authenticates clients with per-node Ed25519 keys (VAUTH1). The legacy
	// shared secret was removed (P4), so the daemon no longer needs one to start;
	// authorization is governed by ~/.vssh/authorized_keys (or /etc/vssh).
	server.DaemonVersion = version
	srv := server.NewServer(port, "")
	fmt.Printf("vsshd starting on :%d\n", port)
	if err := srv.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func cmdShell(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: vssh shell <host[:port]>")
		os.Exit(1)
	}

	host, port := parseHostPort(args[0])
	secret := getSecret()
	host = resolveReachableHost(host, port)

	if err := server.Connect(host, port, secret); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func cmdRun(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: vssh run <host[:port]> <command>")
		os.Exit(1)
	}

	host, port := parseHostPort(args[0])
	command := strings.Join(args[1:], " ")
	secret := getSecret()
	host = resolveReachableHost(host, port)

	result, err := server.ExecCommandStructured(host, port, secret, command)
	if err != nil {
		if result.ErrorCode != "" {
			fmt.Fprintf(os.Stderr, "Error [%s]: %v\n", result.ErrorCode, err)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
	fmt.Print(result.Stdout)
	fmt.Fprint(os.Stderr, result.Stderr)
	if !result.Success {
		if result.Error != "" {
			if result.ErrorCode != "" {
				fmt.Fprintf(os.Stderr, "[%s] %s\n", result.ErrorCode, result.Error)
			} else {
				fmt.Fprintln(os.Stderr, result.Error)
			}
		}
		if result.ExitCode > 0 {
			os.Exit(result.ExitCode)
		}
		os.Exit(1)
	}
}

type multiExecResult struct {
	Target string                    `json:"target"`
	Result *server.ExecCommandResult `json:"result,omitempty"`
	Error  string                    `json:"error,omitempty"`
}

func cmdRunBatch(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: vssh run-batch <host[:port]>   (commands on stdin, one per line)")
		os.Exit(1)
	}
	host, port := parseHostPort(args[0])
	host = resolveReachableHost(host, port)
	var commands []string
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			commands = append(commands, sc.Text())
		}
	}
	if len(commands) == 0 {
		fmt.Fprintln(os.Stderr, "no commands provided on stdin")
		os.Exit(1)
	}
	results, err := server.RunMux(host, port, getSecret(), commands)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	writeJSON(results)
}

func cmdRunMany(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: vssh run-many <host1,host2> <command>")
		os.Exit(1)
	}
	targets := splitTargets(args[0])
	command := strings.Join(args[1:], " ")
	results := runMany(targets, command, 30*time.Second, defaultMaxParallelism())
	writeJSON(results)
}

// cmdRunAsync runs a command as a daemon job and waits up to --wait seconds for it
// to finish. If it completes in time the full result is returned inline (so short
// commands feel synchronous); otherwise it returns the job id for the caller to
// poll. This dodges fixed call-timeout caps (e.g. an MCP client's ~60s ceiling)
// without abandoning or double-running the command — it runs exactly once, as a job.
func cmdRunAsync(args []string) {
	wait := 20
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if (args[i] == "--wait" || args[i] == "-w") && i+1 < len(args) {
			if n, err := strconv.Atoi(args[i+1]); err == nil {
				wait = n
			}
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: vssh run-async <host[:port]> <command> [--wait <seconds>]")
		os.Exit(1)
	}
	target := rest[0]
	command := strings.Join(rest[1:], " ")

	resp, err := callRPCNative(target, "job_start", map[string]interface{}{"command": command}, 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	var info server.JobInfo
	if err := decodeRPCResult(resp, &info); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	jobID := info.ID

	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(400 * time.Millisecond)
		sresp, err := callRPCNative(target, "job_status", map[string]interface{}{"id": jobID}, 15*time.Second)
		if err != nil {
			continue
		}
		var st server.JobInfo
		if decodeRPCResult(sresp, &st) != nil {
			continue
		}
		if st.Status == server.JobRunning {
			continue
		}
		// Finished within the wait window — collect logs and return inline.
		var logs server.JobLogs
		if lresp, lerr := callRPCNative(target, "job_logs", map[string]interface{}{"id": jobID}, 15*time.Second); lerr == nil {
			_ = decodeRPCResult(lresp, &logs)
		}
		writeJSON(map[string]interface{}{
			"promoted":    false,
			"success":     st.Status == server.JobSucceeded,
			"job_id":      jobID,
			"status":      string(st.Status),
			"exit_code":   st.ExitCode,
			"duration_ms": st.DurationMs,
			"stdout":      logs.Stdout,
			"stderr":      logs.Stderr,
		})
		if st.Status != server.JobSucceeded {
			if st.ExitCode > 0 {
				os.Exit(st.ExitCode)
			}
			os.Exit(1)
		}
		return
	}

	// Still running past the wait window — promote to a job id the caller polls.
	writeJSON(map[string]interface{}{
		"promoted": true,
		"success":  true,
		"job_id":   jobID,
		"status":   "running",
		"host":     target,
		"poll":     fmt.Sprintf("vssh job-status %s %s | vssh job-logs %s %s", target, jobID, target, jobID),
	})
}

func runMany(targets []string, command string, timeout time.Duration, maxParallelism int) []multiExecResult {
	results := make([]multiExecResult, len(targets))
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
			result, err := server.ExecCommandStructuredTimeout(host, port, getSecret(), command, timeout)
			results[i].Target = target
			if err != nil {
				results[i].Error = err.Error()
				return
			}
			results[i].Result = &result
		}()
	}
	wg.Wait()
	return results
}

func cmdExec(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: vssh exec <host[:port]> <command...>")
		os.Exit(1)
	}
	cmdRun(args)
}

func cmdBench(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: vssh bench <host[:port]> [count]")
		os.Exit(1)
	}

	host, port := parseHostPort(args[0])
	count := 20
	if len(args) >= 2 {
		if n, err := strconv.Atoi(args[1]); err == nil && n > 0 {
			count = n
		}
	}
	if count > 500 {
		count = 500
	}

	secret := getSecret()
	resolved := resolveReachableHost(host, port)
	latencies := make([]time.Duration, 0, count)
	failures := 0

	for i := 0; i < count; i++ {
		start := time.Now()
		result, err := server.ExecCommandStructuredTimeout(resolved, port, secret, ":", 5*time.Second)
		elapsed := time.Since(start)
		if err != nil || !result.Success {
			failures++
			continue
		}
		latencies = append(latencies, elapsed)
	}

	if len(latencies) == 0 {
		fmt.Printf("vssh bench %s (%s:%d): all %d attempts failed\n", host, resolved, port, count)
		os.Exit(1)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	var total time.Duration
	for _, latency := range latencies {
		total += latency
	}
	avg := total / time.Duration(len(latencies))
	p50 := percentileLatency(latencies, 50)
	p95 := percentileLatency(latencies, 95)

	fmt.Printf("vssh bench %s (%s:%d)\n", host, resolved, port)
	fmt.Printf("  attempts: %d ok, %d failed\n", len(latencies), failures)
	fmt.Printf("  min:      %s\n", latencies[0].Round(time.Microsecond))
	fmt.Printf("  p50:      %s\n", p50.Round(time.Microsecond))
	fmt.Printf("  avg:      %s\n", avg.Round(time.Microsecond))
	fmt.Printf("  p95:      %s\n", p95.Round(time.Microsecond))
	fmt.Printf("  max:      %s\n", latencies[len(latencies)-1].Round(time.Microsecond))
}

func percentileLatency(values []time.Duration, percentile int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	if percentile <= 0 {
		return values[0]
	}
	if percentile >= 100 {
		return values[len(values)-1]
	}
	idx := (len(values)*percentile + 99) / 100
	if idx <= 0 {
		idx = 1
	}
	if idx > len(values) {
		idx = len(values)
	}
	return values[idx-1]
}
