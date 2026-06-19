package main

import (
	"testing"
	"time"
)

func TestPickReachablePrefersLowestIndex(t *testing.T) {
	hosts := []string{"a", "b", "c"}
	got, ok := pickReachable(hosts, func(h string) bool { return true })
	if !ok || got != "a" {
		t.Fatalf("pickReachable = %q,%v; want a,true", got, ok)
	}
}

func TestPickReachableSkipsDeadPreferred(t *testing.T) {
	hosts := []string{"dead", "b", "c"}
	got, ok := pickReachable(hosts, func(h string) bool { return h != "dead" })
	if !ok || got != "b" {
		t.Fatalf("pickReachable = %q,%v; want b,true", got, ok)
	}
}

func TestPickReachableNoneUp(t *testing.T) {
	hosts := []string{"a", "b"}
	if got, ok := pickReachable(hosts, func(h string) bool { return false }); ok {
		t.Fatalf("pickReachable = %q,%v; want \"\",false", got, ok)
	}
}

// The regression guard: a fast, reachable, highest-preference host must be
// returned without waiting on a slow dead lower-priority probe.
func TestPickReachableDoesNotWaitOnSlowLowerPriority(t *testing.T) {
	hosts := []string{"fast", "slowdead"}
	const slow = 800 * time.Millisecond
	start := time.Now()
	got, ok := pickReachable(hosts, func(h string) bool {
		if h == "slowdead" {
			time.Sleep(slow)
			return false
		}
		return true
	})
	elapsed := time.Since(start)
	if !ok || got != "fast" {
		t.Fatalf("pickReachable = %q,%v; want fast,true", got, ok)
	}
	if elapsed >= slow {
		t.Fatalf("waited %v on slow lower-priority probe; should return promptly", elapsed)
	}
}

// When the preferred host is the slow one, we must wait for it (it could win),
// but still return the reachable fallback once the preferred resolves down.
func TestPickReachableWaitsForSlowPreferred(t *testing.T) {
	hosts := []string{"slowdead", "fastup"}
	got, ok := pickReachable(hosts, func(h string) bool {
		if h == "slowdead" {
			time.Sleep(50 * time.Millisecond)
			return false
		}
		return true
	})
	if !ok || got != "fastup" {
		t.Fatalf("pickReachable = %q,%v; want fastup,true", got, ok)
	}
}
