package server

import "testing"

func TestFleetStateSignVerifyAndTamper(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	nodes := []FleetNode{
		{Name: "g1", Online: true, Caps: []string{"exec", "rpc"}},
		{Name: "d1", IP: "10.0.0.1", OS: "linux", Caps: []string{"exec"}},
	}
	fs := BuildAndSignFleetState(nodes)
	if fs.Signature == "" || fs.GeneratedBy == "" {
		t.Fatal("state not signed")
	}
	if fs.Nodes[0].Name != "d1" {
		t.Fatalf("nodes not sorted: %s first", fs.Nodes[0].Name)
	}
	if !VerifyFleetState(fs) {
		t.Fatal("valid signature rejected")
	}
	// tamper a copy
	bad := fs
	bad.Nodes = append([]FleetNode(nil), fs.Nodes...)
	bad.Nodes[0].IP = "10.0.0.99"
	if VerifyFleetState(bad) {
		t.Fatal("tampered state passed verification")
	}
	// roundtrip
	if err := WriteFleetState(fs); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFleetState()
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyFleetState(got) {
		t.Fatal("roundtrip verify failed")
	}
	if FleetStateAgeSeconds(got) < 0 {
		t.Fatal("bad age")
	}
}
