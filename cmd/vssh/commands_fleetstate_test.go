package main

import (
	"testing"

	"github.com/zeus-kim/vssh/internal/server"
)

func TestToInt64(t *testing.T) {
	if toInt64(float64(12)) != 12 || toInt64(int(7)) != 7 || toInt64(int64(9)) != 9 || toInt64("x") != 0 {
		t.Fatal("toInt64 conversion wrong")
	}
}

func TestFleetStateResult(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fs := server.BuildAndSignFleetState([]server.FleetNode{{Name: "d1"}, {Name: "g1"}})
	r := fleetStateResult(fs, false)
	if r["success"] != true || r["verified"] != true {
		t.Fatalf("unexpected result: %#v", r)
	}
	if r["node_count"].(int) != 2 {
		t.Fatalf("node_count=%v", r["node_count"])
	}
}
