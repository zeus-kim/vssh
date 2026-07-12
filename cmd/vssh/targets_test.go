package main

import (
	"reflect"
	"testing"

	"github.com/zeus-kim/vssh/internal/fleet"
)

func testFleet() *fleet.FleetMemory {
	return &fleet.FleetMemory{Nodes: map[string]fleet.NodeMemory{
		"d1": {Name: "d1", Role: "gpu", Services: []string{"ollama"}, Tags: []string{"prod"}},
		"g1": {Name: "g1", Role: "gpu", Services: []string{"training"}, Tags: []string{"prod"}},
		"s1": {Name: "s1", Role: "storage", Tags: []string{"prod"}},
		"v1": {Name: "v1", Role: "vm", Tags: []string{"dev"}},
	}}
}

func TestResolveLiteralHosts(t *testing.T) {
	got, err := resolveTargetsWith(testFleet(), "d1, x9 ,g1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"d1", "x9", "g1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v (x9 passes through as a literal host)", got, want)
	}
}

func TestResolveRoleSelector(t *testing.T) {
	got, err := resolveTargetsWith(testFleet(), "@role:gpu")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"d1", "g1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("@role:gpu = %v, want %v", got, want)
	}
}

func TestResolveBareSelectorUnionsRoleTagService(t *testing.T) {
	// "@prod" is a tag; "@gpu" a role; "@ollama" a service — all via bare form.
	if got, _ := resolveTargetsWith(testFleet(), "@prod"); !reflect.DeepEqual(got, []string{"d1", "g1", "s1"}) {
		t.Fatalf("@prod = %v, want [d1 g1 s1]", got)
	}
	if got, _ := resolveTargetsWith(testFleet(), "@ollama"); !reflect.DeepEqual(got, []string{"d1"}) {
		t.Fatalf("@ollama = %v, want [d1]", got)
	}
}

func TestResolveDedupAndOrder(t *testing.T) {
	// @gpu -> d1,g1 ; then d1 again must not duplicate; s1 appended after.
	got, err := resolveTargetsWith(testFleet(), "@gpu,d1,s1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"d1", "g1", "s1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dedup/order = %v, want %v", got, want)
	}
}

func TestResolveAll(t *testing.T) {
	got, _ := resolveTargetsWith(testFleet(), "@all")
	if want := []string{"d1", "g1", "s1", "v1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("@all = %v, want %v", got, want)
	}
}

func TestResolveUnknownSelectorErrors(t *testing.T) {
	if _, err := resolveTargetsWith(testFleet(), "@role:nope"); err == nil {
		t.Fatal("expected error for selector matching no nodes")
	}
	if _, err := resolveTargetsWith(testFleet(), ""); err == nil {
		t.Fatal("expected error for empty spec")
	}
}
