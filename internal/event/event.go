package event

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	TypeAgentLifecycle  = "agent_lifecycle"
	TypePeerStateChange = "peer_state_change"
	TypeProbeResult     = "probe_result"
)

type LifecyclePayload struct {
	Action  string `json:"action"`
	PID     int    `json:"pid,omitempty"`
	Version string `json:"version,omitempty"`
}

type ProbeResultPayload struct {
	Target    string `json:"target"`
	Path      string `json:"path"`
	LatencyMs int64  `json:"latency_ms"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}

type Event struct {
	ID      string          `json:"id"`
	TS      int64           `json:"ts"`
	Node    string          `json:"node"`
	Agent   string          `json:"agent"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	Version int             `json:"version"`
}

func NewEvent(node, agent, eventType string, payload any) (*Event, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}

	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		raw = data
	}

	return &Event{
		ID:      id.String(),
		TS:      time.Now().UnixMilli(),
		Node:    node,
		Agent:   agent,
		Type:    eventType,
		Payload: raw,
		Version: 1,
	}, nil
}

func DefaultLocalLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), ".vssh", "events.log")
	}
	return filepath.Join(home, ".vssh", "events.log")
}

type EventLog struct {
	path string
	mu   sync.Mutex
	seen map[string]struct{}
}

func NewEventLog(path string) *EventLog {
	if path == "" {
		path = DefaultLocalLogPath()
	}
	return &EventLog{
		path: path,
		seen: make(map[string]struct{}),
	}
}

func (l *EventLog) Append(event *Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, dup := l.seen[event.ID]; dup {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(l.path), 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}

	l.seen[event.ID] = struct{}{}

	if len(l.seen) > 10000 {
		l.seen = make(map[string]struct{})
	}

	return nil
}

func (l *EventLog) ReadAll() ([]*Event, error) {
	return l.ReadSince(0)
}

func (l *EventLog) ReadSince(since int64) ([]*Event, error) {
	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var events []*Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		if e.TS >= since {
			events = append(events, &e)
		}
	}

	return events, scanner.Err()
}
