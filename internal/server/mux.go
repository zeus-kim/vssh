package server

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

func writeMuxJSON(conn net.Conn, r ExecCommandResult) {
	data, _ := json.Marshal(r)
	conn.Write(append(data, '\n'))
}

// HandleMux serves a multiplexed session: authenticate once, then execute many
// commands over the same connection (one newline-delimited JSON response each)
// until the client quits or goes idle. This amortizes the connect+auth cost across
// commands — the foundation for ssh-ControlMaster-style fast repeat execution.
func HandleMux(conn net.Conn, reader *bufio.Reader) {
	reader.ReadString('\n') // consume the "MUX" line
	conn.Write([]byte("MUX_OK\n"))
	for {
		conn.SetReadDeadline(time.Now().Add(300 * time.Second)) // idle timeout
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" || line == "QUIT" {
			return
		}
		if strings.HasPrefix(line, "EXEJ ") {
			enc := strings.TrimSpace(strings.TrimPrefix(line, "EXEJ "))
			raw, derr := base64.StdEncoding.DecodeString(enc)
			if derr != nil {
				writeMuxJSON(conn, ExecCommandResult{Success: false, ExitCode: -1, Error: "invalid base64 command", ErrorCode: ErrCodeBadResponse})
				continue
			}
			if d := enforceExecPolicy(conn, string(raw)); d != nil {
				writeMuxJSON(conn, *d)
				continue
			}
			result := ExecLocalStructured(string(raw))
			auditLog(conn, string(raw), result)
			writeMuxJSON(conn, result)
			continue
		}
		writeMuxJSON(conn, ExecCommandResult{Success: false, ExitCode: -1, Error: "unknown mux command", ErrorCode: "unsupported_method"})
	}
}

// RunMux runs a batch of commands over a single multiplexed connection and returns
// one result per command. One connect + one auth for the whole batch.
func RunMux(host string, port int, secret string, commands []string) ([]ExecCommandResult, error) {
	conn, reader, err := dialAuth(host, port, secret, 10*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	conn.Write([]byte("MUX\n"))
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	mresp, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(mresp, "MUX_OK") {
		return nil, fmt.Errorf("mux not supported by daemon")
	}

	results := make([]ExecCommandResult, 0, len(commands))
	for _, cmd := range commands {
		enc := base64.StdEncoding.EncodeToString([]byte(cmd))
		conn.Write([]byte("EXEJ " + enc + "\n"))
		conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		line, rerr := reader.ReadBytes('\n')
		if rerr != nil {
			return results, rerr
		}
		var r ExecCommandResult
		if jerr := json.Unmarshal(line, &r); jerr != nil {
			r = ExecCommandResult{Success: false, ExitCode: -1, Error: "bad mux response", ErrorCode: ErrCodeBadResponse}
		}
		if r.Command == "" {
			r.Command = cmd
		}
		results = append(results, r)
	}
	conn.Write([]byte("QUIT\n"))
	return results, nil
}
