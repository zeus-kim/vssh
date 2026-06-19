package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config mutation helpers — the substrate for AI-driven configuration. They edit
// the LOCAL config dir only (~/.vssh or /etc/vssh); fleet-wide propagation is a
// separate, explicit step (scripts/*). Callers gate these behind an opt-in.

// ConfigDir is the active vssh config directory.
func ConfigDir() string { return vsshConfigDir() }

func authorizedKeysPath() string { return filepath.Join(vsshConfigDir(), "authorized_keys") }

// AuthorizeKey appends an authorized_keys line for pubB64 if absent (idempotent).
func AuthorizeKey(pubB64, caps, comment string) error {
	pubB64 = strings.TrimSpace(pubB64)
	if pubB64 == "" {
		return fmt.Errorf("empty pubkey")
	}
	if _, ok := KeyCapabilities(pubB64); ok {
		return nil // already authorized
	}
	line := pubB64
	if c := strings.TrimSpace(caps); c != "" {
		line += " caps=" + c
	}
	if cm := strings.TrimSpace(comment); cm != "" {
		line += " " + cm
	}
	_ = os.MkdirAll(vsshConfigDir(), 0700)
	f, err := os.OpenFile(authorizedKeysPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

// RevokeKey removes authorized_keys lines whose first field == pubB64 (backs up
// the file). Returns whether anything was removed.
func RevokeKey(pubB64 string) (bool, error) {
	pubB64 = strings.TrimSpace(pubB64)
	path := authorizedKeysPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var kept []string
	removed := false
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == pubB64 {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		return false, nil
	}
	_ = os.WriteFile(path+".bak", data, 0600)
	return true, os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0600)
}

func upsertKeyed(path, key, newline string, keyOf func(string) string) error {
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	var out []string
	replaced := false
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if keyOf(line) == key {
				if !replaced {
					out = append(out, newline)
					replaced = true
				}
				continue
			}
			out = append(out, line)
		}
	}
	if !replaced {
		out = append(out, newline)
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0600)
}

// SetNodeConfig sets/replaces a "name=ip" line in the config file.
func SetNodeConfig(name, ip string) error {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return fmt.Errorf("empty name")
	}
	return upsertKeyed(filepath.Join(vsshConfigDir(), "config"), name, name+"="+strings.TrimSpace(ip), func(line string) string {
		if i := strings.Index(line, "="); i >= 0 {
			return strings.TrimSpace(strings.ToLower(line[:i]))
		}
		return ""
	})
}

// PinNode sets/replaces a "name pubkey" line in node_keys (host-identity pin).
func PinNode(name, pubB64 string) error {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return fmt.Errorf("empty name")
	}
	return upsertKeyed(filepath.Join(vsshConfigDir(), "node_keys"), name, name+" "+strings.TrimSpace(pubB64), func(line string) string {
		if f := strings.Fields(line); len(f) > 0 {
			return strings.ToLower(f[0])
		}
		return ""
	})
}
