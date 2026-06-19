package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"time"
)

func effectiveTransferUser() *user.User {
	if os.Getuid() != 0 {
		return nil
	}
	defaultUser := getDefaultUser()
	if defaultUser == "" || defaultUser == "root" {
		return nil
	}
	u, err := user.Lookup(defaultUser)
	if err != nil {
		return nil
	}
	return u
}

func expandTransferPath(path string, transferUser *user.User) string {
	if len(path) == 0 || path[0] != '~' {
		return path
	}
	if transferUser != nil && transferUser.HomeDir != "" {
		return transferUser.HomeDir + path[1:]
	}
	home, _ := os.UserHomeDir()
	return home + path[1:]
}

func chownToTransferUser(path string, transferUser *user.User) {
	if transferUser == nil {
		return
	}
	uid, uidErr := strconv.Atoi(transferUser.Uid)
	gid, gidErr := strconv.Atoi(transferUser.Gid)
	if uidErr != nil || gidErr != nil {
		return
	}
	_ = os.Chown(path, uid, gid)
}

func execShell(cmdStr string) ([]byte, error) {
	shell := "/bin/bash"
	if _, err := os.Stat("/bin/zsh"); err == nil {
		shell = "/bin/zsh"
	}

	// If running as root, switch to default user
	if os.Getuid() == 0 {
		defaultUser := getDefaultUser()
		if defaultUser != "" && defaultUser != "root" {
			// Use sudo -u to run as the default user (works without password when root)
			name, args := userExecParts(defaultUser, shell, cmdStr)
			cmd := exec.Command(name, args...)
			out, err := cmd.CombinedOutput()
			// Prepend user info for debugging
			prefix := []byte(fmt.Sprintf("[wire-user:%s] ", defaultUser))
			return append(prefix, out...), err
		}
	}

	cmd := exec.Command(shell, "-c", cmdStr)
	return cmd.CombinedOutput()
}

func writeExecJSON(conn net.Conn, result ExecCommandResult) {
	data, _ := json.Marshal(result)
	conn.Write(data)
	conn.Write([]byte("\n"))
}

func execShellStructured(cmdStr string) ExecCommandResult {
	start := time.Now()
	result := ExecCommandResult{
		Command:  cmdStr,
		ExitCode: -1,
	}

	shell := "/bin/bash"
	if _, err := os.Stat("/bin/zsh"); err == nil {
		shell = "/bin/zsh"
	}

	var cmd *exec.Cmd
	if os.Getuid() == 0 {
		defaultUser := getDefaultUser()
		if defaultUser != "" && defaultUser != "root" {
			name, args := userExecParts(defaultUser, shell, cmdStr)
			cmd = exec.Command(name, args...)
		}
	}
	if cmd == nil {
		cmd = exec.Command(shell, "-c", cmdStr)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	result.DurationMs = time.Since(start).Milliseconds()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	if err == nil {
		result.Success = true
		result.ExitCode = 0
		return result
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.Error = err.Error()
	}
	return result
}

var loginPathCache sync.Map // user -> login $PATH (computed once)

// loginPATHFor returns the user's full login-shell PATH, computed once via a login
// shell and cached. This lets us run commands in a FAST non-login shell while still
// seeing the PATH a login shell would (nvm, cargo, ~/.local/bin, …) — a login shell
// per command costs ~350ms (it re-sources the whole profile); this pays that once.
func loginPATHFor(user, shell string) string {
	if v, ok := loginPathCache.Load(user); ok {
		return v.(string)
	}
	out, err := exec.Command("sudo", "-u", user, shell, "-lc", `printf %s "$PATH"`).Output()
	p := ""
	if err == nil {
		p = strings.TrimSpace(string(out))
	}
	loginPathCache.Store(user, p)
	return p
}

func serverShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// userExecParts builds the argv to run cmdStr as the node's non-root user. It uses a
// non-login, non-interactive shell (`-c`) — fast and newline-safe (the old `-lc`
// login shell both stripped embedded newlines in interactive mode and cost ~350ms of
// profile sourcing) — and injects the cached login PATH so command resolution is
// unchanged. Returns (name, args) suitable for exec.Command / exec.CommandContext.
func userExecParts(defaultUser, shell, cmdStr string) (string, []string) {
	full := cmdStr
	if p := loginPATHFor(defaultUser, shell); p != "" {
		full = "export PATH=" + serverShellQuote(p) + "\n" + cmdStr
	}
	return "sudo", []string{"-u", defaultUser, shell, "-c", full}
}

// getDefaultUser returns the primary non-root user
func getDefaultUser() string {
	// Check environment variable first
	if u := os.Getenv("WIRE_DEFAULT_USER"); u != "" {
		return u
	}

	// Try to find first valid user in /home
	entries, err := os.ReadDir("/home")
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				name := e.Name()
				// Skip common system directories
				if name == "lost+found" {
					continue
				}
				// Verify user exists in system
				if _, err := user.Lookup(name); err == nil {
					return name
				}
			}
		}
	}

	// Synology: check /volume1/homes
	entries, err = os.ReadDir("/volume1/homes")
	if err == nil {
		for _, e := range entries {
			if e.IsDir() && e.Name() != "admin" {
				name := e.Name()
				if _, err := user.Lookup(name); err == nil {
					return name
				}
			}
		}
	}

	return ""
}

// ExecLocal executes command locally
func ExecLocal(command string) (string, error) {
	out, err := execShell(command)
	return string(out), err
}

// ExecLocalStructured executes command locally and returns stdout/stderr/exit.
func ExecLocalStructured(command string) ExecCommandResult {
	return execShellStructured(command)
}
