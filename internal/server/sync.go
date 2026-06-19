package server

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	ChunkSize   = 1024 * 1024 // 1MB chunks
	SyncTimeout = 3600        // 1 hour max for large files
)

// HandleSync handles SYNC command on server side
func HandleSync(conn net.Conn, cmd string) {
	if policyBlockUnscoped(conn, "SYNC") {
		return
	}
	// Parse: SYNC <size> <md5> <path>
	parts := strings.SplitN(cmd, " ", 4)
	if len(parts) < 4 {
		conn.Write([]byte("ERROR invalid command\n"))
		return
	}

	size, _ := strconv.ParseInt(parts[1], 10, 64)
	expectedMD5 := parts[2]
	path := strings.TrimSpace(parts[3])

	// Expand ~
	if len(path) > 0 && path[0] == '~' {
		home, _ := os.UserHomeDir()
		path = home + path[1:]
	}

	// Check if file exists with same MD5
	if existing, err := os.Open(path); err == nil {
		hash := md5.New()
		io.Copy(hash, existing)
		existing.Close()
		if hex.EncodeToString(hash.Sum(nil)) == expectedMD5 {
			conn.Write([]byte("SKIP\n"))
			return
		}
	}

	// Create directory
	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0755)

	// Create temp file
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		conn.Write([]byte(fmt.Sprintf("ERROR %v\n", err)))
		return
	}

	conn.Write([]byte("READY\n"))

	// Receive data
	hash := md5.New()
	writer := io.MultiWriter(f, hash)

	received := int64(0)
	buf := make([]byte, ChunkSize)
	conn.SetReadDeadline(time.Now().Add(time.Duration(SyncTimeout) * time.Second))

	for received < size {
		toRead := size - received
		if toRead > int64(ChunkSize) {
			toRead = int64(ChunkSize)
		}
		n, err := conn.Read(buf[:toRead])
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
			conn.Write([]byte(fmt.Sprintf("ERROR read: %v\n", err)))
			return
		}
		writer.Write(buf[:n])
		received += int64(n)

		// Reset deadline on progress
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	}

	f.Close()

	// Verify MD5
	gotMD5 := hex.EncodeToString(hash.Sum(nil))
	if gotMD5 != expectedMD5 {
		os.Remove(tmpPath)
		conn.Write([]byte(fmt.Sprintf("ERROR md5 mismatch: expected %s, got %s\n", expectedMD5, gotMD5)))
		return
	}

	// Rename temp to final
	os.Remove(path)
	if err := os.Rename(tmpPath, path); err != nil {
		conn.Write([]byte(fmt.Sprintf("ERROR rename: %v\n", err)))
		return
	}

	conn.Write([]byte(fmt.Sprintf("OK %d %s\n", received, gotMD5)))
}
