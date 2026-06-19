package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/zeus-kim/vssh/internal/server"
)

func cmdPut(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: vssh put <local-file> <host[:port]:remote-path>")
		os.Exit(1)
	}

	localPath := args[0]
	remote := args[1]

	// Parse host:path
	idx := strings.LastIndex(remote, ":")
	if idx == -1 {
		fmt.Fprintln(os.Stderr, "Error: remote path must be host:path format")
		os.Exit(1)
	}

	hostPart := remote[:idx]
	remotePath := remote[idx+1:]
	host, port := parseHostPort(hostPart)
	secret := getSecret()
	host = resolveReachableHost(host, port)

	if err := server.SendFile(host, port, secret, localPath, remotePath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func cmdGet(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: vssh get <host[:port]:remote-path> <local-file>")
		os.Exit(1)
	}

	remote := args[0]
	localPath := args[1]

	// Parse host:path
	idx := strings.LastIndex(remote, ":")
	if idx == -1 {
		fmt.Fprintln(os.Stderr, "Error: remote path must be host:path format")
		os.Exit(1)
	}

	hostPart := remote[:idx]
	remotePath := remote[idx+1:]
	host, port := parseHostPort(hostPart)
	secret := getSecret()
	host = resolveReachableHost(host, port)

	if err := server.RecvFile(host, port, secret, remotePath, localPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// shellQuote single-quotes a string for safe embedding in a remote /bin/sh command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type deployBinaryResult struct {
	Success      bool   `json:"success"`
	Host         string `json:"host"`
	RemotePath   string `json:"remote_path"`
	Service      string `json:"service,omitempty"`
	Phase        string `json:"phase"` // upload | install | verify | ok
	VerifyOutput string `json:"verify_output,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	Error        string `json:"error,omitempty"`
}

// cmdDeployBinary is the first-class "ship a binary to a node" verb: it uploads
// atomically with checksum verification (P1.1), installs it into a privileged path
// via an atomic, ETXTBSY-safe rename, optionally restarts a service, and verifies —
// the exact dance deploy_fleet.sh hand-rolls, collapsed into one auditable call.
func cmdDeployBinary(args []string) {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: vssh deploy-binary <local-binary> <host[:port]> <remote-path> [--service <name>] [--mode 0755] [--verify <cmd>]")
		os.Exit(1)
	}
	local := args[0]
	host, port := parseHostPort(args[1])
	remotePath := args[2]
	service, mode, verify := "", "0755", ""
	for i := 3; i < len(args); i++ {
		switch args[i] {
		case "--service":
			if i+1 < len(args) {
				service = args[i+1]
				i++
			}
		case "--mode":
			if i+1 < len(args) {
				mode = args[i+1]
				i++
			}
		case "--verify":
			if i+1 < len(args) {
				verify = args[i+1]
				i++
			}
		}
	}
	secret := getSecret()
	resolved := resolveReachableHost(host, port)

	base := remotePath
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	stage := fmt.Sprintf("/tmp/vssh-deploy-%d-%s", os.Getpid(), base)

	fail := func(phase, code, msg string) {
		writeJSON(deployBinaryResult{Success: false, Host: host, RemotePath: remotePath, Service: service, Phase: phase, ErrorCode: code, Error: msg})
		os.Exit(1)
	}

	// 1) Atomic, checksum-verified upload to a staging path.
	if err := server.SendFile(resolved, port, secret, local, stage); err != nil {
		fail("upload", "upload_failed", err.Error())
	}

	// 2) Privileged atomic install (+ optional service restart). Runs as the node's
	// transfer user with passwordless sudo (same as the manual deploy path).
	install := fmt.Sprintf(
		"sudo cp %s %s.new && sudo chmod %s %s.new && sudo mv -f %s.new %s && rm -f %s",
		shellQuote(stage), shellQuote(remotePath), mode, shellQuote(remotePath),
		shellQuote(remotePath), shellQuote(remotePath), shellQuote(stage),
	)
	if service != "" {
		install += fmt.Sprintf(
			" && (sudo systemctl restart %s 2>/dev/null || sudo launchctl kickstart -k gui/$(id -u)/%s) && sleep 2",
			shellQuote(service), shellQuote(service),
		)
	}
	r, err := server.ExecCommandStructured(resolved, port, secret, install)
	if err != nil {
		fail("install", r.ErrorCode, err.Error())
	}
	if !r.Success {
		fail("install", r.ErrorCode, strings.TrimSpace(r.Stdout+r.Stderr+r.Error))
	}

	// 3) Verify.
	verifyCmd := verify
	if verifyCmd == "" {
		verifyCmd = remotePath + " --version"
	}
	v, _ := server.ExecCommandStructured(resolved, port, secret, verifyCmd)
	writeJSON(deployBinaryResult{
		Success: true, Host: host, RemotePath: remotePath, Service: service,
		Phase: "ok", VerifyOutput: strings.TrimSpace(v.Stdout),
	})
}

// cmdFwd implements the ssh -L/-R/-D replacements over the native daemon. Each
// tunneled connection is individually authenticated (VAUTH1 preferred) and
// server-side audited under the new `forward` capability.
//
//	vssh fwd <host> -L [bind:]<lport>:<rhost>:<rport>   local forward
//	vssh fwd <host> -R [bind:]<rport>:<lhost>:<lport>   reverse forward
//	vssh fwd <host> -D [bind:]<port>                    dynamic (SOCKS5)
func cmdFwd(args []string) {
	usage := "Usage: vssh fwd <host[:port]> {-L|-R [bind:]<a>:<h>:<p> | -D [bind:]<port>}"
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	host, port := parseHostPort(args[0])
	mode := args[1]
	secret := getSecret()
	host = resolveReachableHost(host, port)

	switch mode {
	case "-L", "-R":
		parts := strings.Split(args[2], ":")
		bind := "127.0.0.1"
		var aPort, h, hPort string
		switch len(parts) {
		case 3:
			aPort, h, hPort = parts[0], parts[1], parts[2]
		case 4:
			bind, aPort, h, hPort = parts[0], parts[1], parts[2], parts[3]
		default:
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(1)
		}
		hp, err := strconv.Atoi(hPort)
		if err != nil || hp <= 0 || hp > 65535 {
			fmt.Fprintf(os.Stderr, "invalid port: %s\n", hPort)
			os.Exit(1)
		}
		if mode == "-L" {
			err = server.ForwardLocal(host, port, secret, net.JoinHostPort(bind, aPort), h, hp)
		} else {
			// -R: bind is the daemon-side bind addr; a=rport on the node, h:hp = local target.
			err = server.ForwardRemote(host, port, secret, bind, aPort, h, hp)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "-D":
		parts := strings.Split(args[2], ":")
		bind, lport := "127.0.0.1", ""
		switch len(parts) {
		case 1:
			lport = parts[0]
		case 2:
			bind, lport = parts[0], parts[1]
		default:
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(1)
		}
		if err := server.ForwardSocks(host, port, secret, net.JoinHostPort(bind, lport)); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
}
