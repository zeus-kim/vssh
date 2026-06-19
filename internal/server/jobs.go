package server

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
)

type JobStatus string

const (
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobCanceled  JobStatus = "canceled"
)

type JobInfo struct {
	ID         string     `json:"id"`
	Command    string     `json:"command"`
	Status     JobStatus  `json:"status"`
	ExitCode   int        `json:"exit_code"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	DurationMs int64      `json:"duration_ms"`
	Error      string     `json:"error,omitempty"`
}

type JobLogs struct {
	JobInfo
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

type jobRecord struct {
	mu      sync.RWMutex
	info    JobInfo
	stdout  lockedBuffer
	stderr  lockedBuffer
	cancel  context.CancelFunc
	done    chan struct{}
	started time.Time
}

type lockedBuffer struct {
	mu  sync.RWMutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.buf.String()
}

var jobRegistry = struct {
	sync.RWMutex
	jobs map[string]*jobRecord
}{jobs: map[string]*jobRecord{}}

func rpcJobStart(command string) (*JobInfo, error) {
	if command == "" {
		return nil, errors.New("command is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UTC()
	job := &jobRecord{
		info: JobInfo{
			ID:        uuid.NewString(),
			Command:   command,
			Status:    JobRunning,
			ExitCode:  -1,
			StartedAt: now,
		},
		cancel:  cancel,
		done:    make(chan struct{}),
		started: now,
	}

	jobRegistry.Lock()
	jobRegistry.jobs[job.info.ID] = job
	jobRegistry.Unlock()

	go runJob(ctx, job)

	info := job.snapshot()
	return &info, nil
}

func rpcJobStatus(id string) (*JobInfo, error) {
	job, err := lookupJob(id)
	if err != nil {
		return nil, err
	}
	info := job.snapshot()
	return &info, nil
}

func rpcJobLogs(id string, tailBytes int) (*JobLogs, error) {
	job, err := lookupJob(id)
	if err != nil {
		return nil, err
	}
	info := job.snapshot()
	return &JobLogs{
		JobInfo: info,
		Stdout:  tailString(job.stdout.String(), tailBytes),
		Stderr:  tailString(job.stderr.String(), tailBytes),
	}, nil
}

func rpcJobCancel(id string) (*JobInfo, error) {
	job, err := lookupJob(id)
	if err != nil {
		return nil, err
	}
	job.mu.RLock()
	status := job.info.Status
	job.mu.RUnlock()
	if status == JobRunning {
		job.cancel()
	}
	info := job.snapshot()
	return &info, nil
}

func lookupJob(id string) (*jobRecord, error) {
	if id == "" {
		return nil, errors.New("id is required")
	}
	jobRegistry.RLock()
	job := jobRegistry.jobs[id]
	jobRegistry.RUnlock()
	if job == nil {
		return nil, errors.New("job not found")
	}
	return job, nil
}

func runJob(ctx context.Context, job *jobRecord) {
	defer close(job.done)

	cmd := shellCommandContext(ctx, job.info.Command)
	cmd.Stdout = &job.stdout
	cmd.Stderr = &job.stderr
	err := cmd.Run()

	now := time.Now().UTC()
	job.mu.Lock()
	defer job.mu.Unlock()
	job.info.EndedAt = &now
	job.info.DurationMs = now.Sub(job.started).Milliseconds()
	if ctx.Err() == context.Canceled {
		job.info.Status = JobCanceled
		job.info.Error = "canceled"
		return
	}
	if err == nil {
		job.info.Status = JobSucceeded
		job.info.ExitCode = 0
		return
	}
	job.info.Status = JobFailed
	if exitErr, ok := err.(*exec.ExitError); ok {
		job.info.ExitCode = exitErr.ExitCode()
	} else {
		job.info.Error = err.Error()
	}
}

func shellCommandContext(ctx context.Context, command string) *exec.Cmd {
	shell := "/bin/bash"
	if _, err := os.Stat("/bin/zsh"); err == nil {
		shell = "/bin/zsh"
	}
	if os.Getuid() == 0 {
		defaultUser := getDefaultUser()
		if defaultUser != "" && defaultUser != "root" {
			name, args := userExecParts(defaultUser, shell, command)
			return exec.CommandContext(ctx, name, args...)
		}
	}
	return exec.CommandContext(ctx, shell, "-c", command)
}

func (j *jobRecord) snapshot() JobInfo {
	j.mu.RLock()
	defer j.mu.RUnlock()
	info := j.info
	if info.Status == JobRunning {
		info.DurationMs = time.Since(j.started).Milliseconds()
	}
	return info
}

func tailString(value string, tailBytes int) string {
	if tailBytes <= 0 || len(value) <= tailBytes {
		return value
	}
	return value[len(value)-tailBytes:]
}
