package fleet

import (
	"reflect"
	"testing"
)

func TestParseSignals(t *testing.T) {
	out := `os=Linux
arch=x86_64
distro=ubuntu
gpu=NVIDIA GeForce RTX 4090
gpu=NVIDIA GeForce RTX 4090
unit=vsshd
unit=ollama
port=22
port=11434
containers=3
disk_gb=916
`
	s := ParseSignals(out)
	if s.OS != "Linux" || s.Arch != "x86_64" || s.Distro != "ubuntu" {
		t.Fatalf("platform: %+v", s)
	}
	if len(s.GPUs) != 2 {
		t.Fatalf("gpus: %v", s.GPUs)
	}
	if !reflect.DeepEqual(s.Units, []string{"vsshd", "ollama"}) {
		t.Fatalf("units: %v", s.Units)
	}
	if !reflect.DeepEqual(s.Ports, []int{22, 11434}) {
		t.Fatalf("ports: %v", s.Ports)
	}
	if s.Containers != 3 || s.DiskGB != 916 {
		t.Fatalf("containers=%d disk=%d", s.Containers, s.DiskGB)
	}
}

func TestInferGPUNode(t *testing.T) {
	m := Infer(Signals{
		OS: "Linux", Arch: "x86_64", Distro: "ubuntu",
		GPUs:  []string{"NVIDIA GeForce RTX 4090", "NVIDIA GeForce RTX 4090"},
		Units: []string{"ollama", "docker", "vsshd"},
		Ports: []int{22, 11434},
	})
	if m.Role != "gpu" {
		t.Fatalf("role = %q, want gpu", m.Role)
	}
	if !hasStr(m.Services, "ollama") || !hasStr(m.Services, "docker") {
		t.Fatalf("services = %v", m.Services)
	}
	for _, want := range []string{"gpu", "multi-gpu", "amd64", "linux", "rtx4090"} {
		if !hasStr(m.Tags, want) {
			t.Fatalf("tags %v missing %q", m.Tags, want)
		}
	}
}

// A mail box is recognized by its units/ports, not its hostname.
func TestInferMailNode(t *testing.T) {
	m := Infer(Signals{OS: "Linux", Arch: "x86_64", Units: []string{"mox"}, Ports: []int{25, 587, 993}})
	if m.Role != "mail" {
		t.Fatalf("role = %q, want mail", m.Role)
	}
	if !hasStr(m.Services, "mail") {
		t.Fatalf("services = %v", m.Services)
	}
}

// A plain box idling on :25 (the default local MTA on macOS/Linux) must NOT be
// mistaken for the fleet's mail server — that would poison the @mail selector.
func TestInferLocalMTAIsNotMailServer(t *testing.T) {
	m := Infer(Signals{OS: "Darwin", Arch: "arm64", Ports: []int{22, 25}})
	if m.Role == "mail" {
		t.Fatalf("lone :25 promoted to mail role: %+v", m)
	}
	// Submission + IMAP alongside :25 IS a real mail server, even with no unit list.
	m2 := Infer(Signals{OS: "Linux", Ports: []int{25, 587}})
	if m2.Role != "mail" {
		t.Fatalf("25+587 role = %q, want mail", m2.Role)
	}
}

// GPU wins over every other signal: a GPU box that also serves mail is a GPU box.
func TestInferGPUOutranksOthers(t *testing.T) {
	m := Infer(Signals{GPUs: []string{"NVIDIA A100"}, Units: []string{"postfix"}, DiskGB: 5000})
	if m.Role != "gpu" {
		t.Fatalf("role = %q, want gpu (GPU outranks mail/storage)", m.Role)
	}
	if !hasStr(m.Tags, "a100") {
		t.Fatalf("tags = %v, want a100 slug", m.Tags)
	}
}

func TestInferStorageAndPlainVM(t *testing.T) {
	// A NAS: small system root but a large data volume (BigDiskGB) → storage.
	if m := Infer(Signals{OS: "Linux", DiskGB: 12, BigDiskGB: 12000}); m.Role != "storage" {
		t.Fatalf("big-volume NAS role = %q, want storage", m.Role)
	}
	// A file server is storage by its service even without a huge disk.
	if m := Infer(Signals{OS: "Linux", Ports: []int{445}}); m.Role != "storage" || !hasStr(m.Services, "samba") {
		t.Fatalf("samba host = role %q services %v, want storage+samba", m.Role, m.Services)
	}
	// A plain box with a modest disk stays vm.
	if m := Infer(Signals{OS: "Linux", Arch: "aarch64", DiskGB: 500, BigDiskGB: 500, Units: []string{"vsshd"}}); m.Role != "vm" {
		t.Fatalf("plain role = %q, want vm", m.Role)
	}
}

func hasStr(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
