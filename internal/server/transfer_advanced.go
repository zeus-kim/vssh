package server

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	ChunkSizeSlow      = 256 * 1024      // 256KB for <10MB/s
	ChunkSizeMedium    = 1024 * 1024     // 1MB for 10-50MB/s
	ChunkSizeFast      = 4 * 1024 * 1024 // 4MB for >50MB/s
	ParallelStreams    = 8
	LargeFileThreshold = 100 * 1024 * 1024 // 100MB
)

// Compressible file extensions
var compressibleExts = map[string]bool{
	".txt": true, ".log": true, ".py": true, ".js": true,
	".json": true, ".md": true, ".xml": true, ".html": true,
	".css": true, ".go": true, ".rs": true, ".java": true,
	".c": true, ".h": true, ".cpp": true, ".hpp": true,
	".yaml": true, ".yml": true, ".toml": true, ".ini": true,
	".sh": true, ".bash": true, ".zsh": true, ".sql": true,
}

// TransferOptions configures transfer behavior
type TransferOptions struct {
	Compress       bool
	Resume         bool
	Parallel       int
	BandwidthLimit int64 // bytes per second, 0 = unlimited
	ChunkSize      int
}

// DefaultTransferOptions returns default options
func DefaultTransferOptions() *TransferOptions {
	return &TransferOptions{
		Compress:  true,
		Resume:    true,
		Parallel:  ParallelStreams,
		ChunkSize: ChunkSizeMedium,
	}
}

// ========== zlib Compression ==========

// shouldCompress checks if file should be compressed
func shouldCompress(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return compressibleExts[ext]
}

// compressData compresses data using zlib
func compressData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, err := w.Write(data)
	if err != nil {
		return nil, err
	}
	w.Close()
	return buf.Bytes(), nil
}

// decompressData decompresses zlib data
func decompressData(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// ========== Resume (Large File Transfer) ==========

// ========== Parallel Transfer ==========

// ========== Adaptive Chunking ==========

// ========== Pipeline Mode ==========

// ========== Multiplexed Put (mput) ==========

// ========== Server Handlers for Advanced Features ==========

// HandlePutZ handles compressed PUT
func HandlePutZ(conn net.Conn, cmd string) {
	if policyBlockUnscoped(conn, "PUTZ") {
		return
	}
	var compressedSize, originalSize int64
	var md5sum, path string
	fmt.Sscanf(cmd, "PUTZ %d %d %s %s", &compressedSize, &originalSize, &md5sum, &path)
	transferUser := effectiveTransferUser()

	// Expand ~ consistently with basic PUT.
	path = expandTransferPath(path, transferUser)

	// Check existing file
	if existing, err := os.Open(path); err == nil {
		h := md5.New()
		io.Copy(h, existing)
		existing.Close()
		if hex.EncodeToString(h.Sum(nil)) == md5sum {
			conn.Write([]byte("SKIP\n"))
			return
		}
	}

	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0755)
	chownToTransferUser(dir, transferUser)

	conn.Write([]byte("READY\n"))

	// Receive compressed data
	compressed := make([]byte, compressedSize)
	io.ReadFull(conn, compressed)

	// Decompress
	data, err := decompressData(compressed)
	if err != nil {
		conn.Write([]byte(fmt.Sprintf("ERROR decompress: %v\n", err)))
		return
	}

	// Verify MD5
	h := md5.Sum(data)
	if hex.EncodeToString(h[:]) != md5sum {
		conn.Write([]byte("ERROR md5 mismatch\n"))
		return
	}

	// Write file
	if err := os.WriteFile(path, data, 0644); err != nil {
		conn.Write([]byte(fmt.Sprintf("ERROR write: %v\n", err)))
		return
	}
	chownToTransferUser(path, transferUser)

	conn.Write([]byte(fmt.Sprintf("OK %d\n", originalSize)))
}

// HandleResume handles resume request
func HandleResume(conn net.Conn, cmd string) {
	if policyBlockUnscoped(conn, "RESUME") {
		return
	}
	var path string
	var size int64
	var md5sum string
	fmt.Sscanf(cmd, "RESUME %s %d %s", &path, &size, &md5sum)
	transferUser := effectiveTransferUser()

	// Expand ~ consistently with basic PUT.
	path = expandTransferPath(path, transferUser)

	tmpPath := path + ".tmp"

	// Check if complete file exists with matching hash
	if existing, err := os.Open(path); err == nil {
		h := md5.New()
		io.Copy(h, existing)
		existing.Close()
		if hex.EncodeToString(h.Sum(nil)) == md5sum {
			conn.Write([]byte("SKIP\n"))
			return
		}
	}

	// Check for partial file
	if stat, err := os.Stat(tmpPath); err == nil {
		offset := stat.Size()
		// Verify partial hash matches
		conn.Write([]byte(fmt.Sprintf("CONTINUE %d\n", offset)))

		// Append to temp file
		f, _ := os.OpenFile(tmpPath, os.O_APPEND|os.O_WRONLY, 0644)
		remaining := size - offset
		io.CopyN(f, conn, remaining)
		f.Close()
	} else {
		// Start fresh
		conn.Write([]byte("RESTART\n"))
		f, _ := os.Create(tmpPath)
		io.CopyN(f, conn, size)
		f.Close()
	}
	chownToTransferUser(tmpPath, transferUser)

	// Verify and rename
	data, _ := os.ReadFile(tmpPath)
	h := md5.Sum(data)
	if hex.EncodeToString(h[:]) == md5sum {
		os.Rename(tmpPath, path)
		chownToTransferUser(path, transferUser)
		conn.Write([]byte(fmt.Sprintf("OK %d\n", size)))
	} else {
		os.Remove(tmpPath)
		conn.Write([]byte("ERROR md5 mismatch\n"))
	}
}

// HandleMPut handles multiplexed put
func HandleMPut(conn net.Conn, cmd string) {
	if policyBlockUnscoped(conn, "MPUT") {
		return
	}
	var count int
	fmt.Sscanf(cmd, "MPUT %d", &count)

	conn.Write([]byte("READY\n"))

	reader := bufio.NewReader(conn)
	for i := 0; i < count; i++ {
		line, _ := reader.ReadString('\n')
		if strings.HasPrefix(line, "DONE") {
			break
		}

		var path string
		var size int64
		var md5sum string
		fmt.Sscanf(line, "FILE %s %d %s", &path, &size, &md5sum)
		transferUser := effectiveTransferUser()

		// Expand ~ consistently with basic PUT.
		path = expandTransferPath(path, transferUser)

		dir := filepath.Dir(path)
		os.MkdirAll(dir, 0755)
		chownToTransferUser(dir, transferUser)

		data := make([]byte, size)
		io.ReadFull(reader, data)

		h := md5.Sum(data)
		if hex.EncodeToString(h[:]) == md5sum {
			os.WriteFile(path, data, 0644)
			chownToTransferUser(path, transferUser)
			conn.Write([]byte("OK\n"))
		} else {
			conn.Write([]byte("ERROR md5 mismatch\n"))
		}
	}
}

// HandlePipeDown handles pipe download command
func HandlePipeDown(conn net.Conn, cmd string) {
	if policyBlockUnscoped(conn, "PIPE_DOWN") {
		return
	}
	command := strings.TrimPrefix(cmd, "PIPE_DOWN ")
	command = strings.TrimSpace(command)

	out, _ := execShell(command)
	conn.Write(out)
}

// ========== Parallel Download Server Handlers ==========

var parallelSessions = make(map[string]*ParallelSession)
var sessionMu sync.Mutex

type ParallelSession struct {
	Path     string
	Size     int64
	Hash     string
	NumConns int
	Created  time.Time
}

// HandleGetM handles parallel download initialization
func HandleGetM(conn net.Conn, cmd string) {
	if policyBlockUnscoped(conn, "GETM") {
		return
	}
	var path string
	var numConns int
	fmt.Sscanf(cmd, "GETM %s %d", &path, &numConns)

	// Expand ~
	if len(path) > 0 && path[0] == '~' {
		home, _ := os.UserHomeDir()
		path = home + path[1:]
	}

	f, err := os.Open(path)
	if err != nil {
		conn.Write([]byte(fmt.Sprintf("ERROR %v\n", err)))
		return
	}
	defer f.Close()

	stat, _ := f.Stat()
	size := stat.Size()

	// Calculate hash
	h := md5.New()
	io.Copy(h, f)
	hash := hex.EncodeToString(h.Sum(nil))

	// Limit connections
	if numConns > 8 {
		numConns = 8
	}
	if size < int64(numConns)*1024*1024 {
		numConns = 1 // Don't parallelize small files
	}

	// Create session
	sessionID := fmt.Sprintf("%x", time.Now().UnixNano())
	sessionMu.Lock()
	parallelSessions[sessionID] = &ParallelSession{
		Path:     path,
		Size:     size,
		Hash:     hash,
		NumConns: numConns,
		Created:  time.Now(),
	}
	sessionMu.Unlock()

	conn.Write([]byte(fmt.Sprintf("OK %s %d %s %d\n", sessionID, size, hash, numConns)))
}

// HandleGetC handles parallel download chunk request
func HandleGetC(conn net.Conn, cmd string) {
	if policyBlockUnscoped(conn, "GETC") {
		return
	}
	var sessionID string
	var chunkIdx int
	fmt.Sscanf(cmd, "GETC %s %d", &sessionID, &chunkIdx)

	sessionMu.Lock()
	session, ok := parallelSessions[sessionID]
	sessionMu.Unlock()

	if !ok {
		conn.Write([]byte("ERROR session not found\n"))
		return
	}

	chunkSize := session.Size / int64(session.NumConns)
	offset := int64(chunkIdx) * chunkSize
	length := chunkSize
	if chunkIdx == session.NumConns-1 {
		length = session.Size - offset
	}

	f, err := os.Open(session.Path)
	if err != nil {
		conn.Write([]byte(fmt.Sprintf("ERROR %v\n", err)))
		return
	}
	defer f.Close()

	f.Seek(offset, 0)
	conn.Write([]byte(fmt.Sprintf("OK %d %d\n", offset, length)))
	io.CopyN(conn, f, length)
}

// HandlePipeUp handles pipe upload
func HandlePipeUp(conn net.Conn, cmd string, reader *bufio.Reader) {
	if policyBlockUnscoped(conn, "PIPE_UP") {
		return
	}
	var path string
	var size int64
	var md5sum string
	fmt.Sscanf(cmd, "PIPE_UP %s %d %s", &path, &size, &md5sum)
	transferUser := effectiveTransferUser()

	// Expand ~ consistently with basic PUT.
	path = expandTransferPath(path, transferUser)

	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0755)
	chownToTransferUser(dir, transferUser)

	conn.Write([]byte("READY\n"))

	data := make([]byte, size)
	io.ReadFull(reader, data)

	// Verify
	h := md5.Sum(data)
	if hex.EncodeToString(h[:]) != md5sum {
		conn.Write([]byte("ERROR md5 mismatch\n"))
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		conn.Write([]byte(fmt.Sprintf("ERROR %v\n", err)))
		return
	}
	chownToTransferUser(path, transferUser)

	conn.Write([]byte("OK\n"))
}

// HandleRPCCommand handles RPC requests
func HandleRPCCommand(conn net.Conn, cmd string, reader *bufio.Reader) {
	// Format: RPC <method> <params_len>\n<params_json>
	var method string
	var paramsLen int
	fmt.Sscanf(cmd, "RPC %s %d", &method, &paramsLen)

	var params []byte
	if paramsLen > 0 {
		params = make([]byte, paramsLen)
		io.ReadFull(reader, params)
	}

	// policy_check needs the connection identity (the key's policy), which the
	// connectionless HandleRPC cannot see — handle it here.
	if method == "policy_check" {
		var p struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(params, &p)
		out, _ := json.Marshal(PolicyCheckRPC(conn, p.Command))
		conn.Write(out)
		conn.Write([]byte("\n"))
		return
	}
	// Authorize file/command-bearing RPC methods against the connection key's
	// caps + policy. Without this, the outer "rpc" capability alone would let
	// file_read/file_write/job_start/restart_service bypass the file/exec caps
	// and the per-key policy engine (privilege escalation).
	if rpcAuthDenied(conn, method, params) {
		return
	}
	result := HandleRPC(method, params)
	conn.Write(result)
	conn.Write([]byte("\n"))
}

// Cleanup old sessions periodically
func init() {
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			sessionMu.Lock()
			now := time.Now()
			for id, s := range parallelSessions {
				if now.Sub(s.Created) > 10*time.Minute {
					delete(parallelSessions, id)
				}
			}
			sessionMu.Unlock()
		}
	}()
}

// rpcStringParam extracts a string field from an RPC params JSON blob.
func rpcStringParam(params []byte, key string) string {
	var m map[string]interface{}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &m)
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// rpcAuthDenied enforces capability + policy on RPC methods that read/write
// files or run commands, closing the gap where the outer "rpc" capability let
// file_read/file_write/artifact_collect/job_start/restart_service bypass the
// file/exec caps and the per-key policy. Read-only typed methods (get_*, *_status,
// node_info, job_status/logs/cancel, list_services, docker_containers) remain
// under "rpc". Returns true (after writing/auditing the denial) when refused.
func rpcAuthDenied(conn net.Conn, method string, params []byte) bool {
	pub, _ := connIdentity(conn)
	caps, _ := KeyCapabilities(pub)
	writeErr := func(code, msg string) bool {
		out, _ := json.Marshal(RPCResponse{Success: false, Error: code + ": " + msg})
		conn.Write(out)
		conn.Write([]byte("\n"))
		return true
	}
	switch method {
	case "file_read", "file_write", "artifact_collect":
		if !capAllowed(caps, "file") {
			return writeErr("cap_denied", "RPC "+method+" requires the 'file' capability")
		}
		if policyPathDenied(conn, rpcStringParam(params, "path")) {
			return true // error already written + audited
		}
	case "job_start", "restart_service":
		if !capAllowed(caps, "exec") {
			return writeErr("cap_denied", "RPC "+method+" requires the 'exec' capability")
		}
		cmd := rpcStringParam(params, "command")
		if method == "restart_service" {
			cmd = "restart_service " + rpcStringParam(params, "service")
		}
		if res := enforceExecPolicy(conn, cmd); res != nil {
			return writeErr("policy_denied", res.Error)
		}
	}
	return false
}
