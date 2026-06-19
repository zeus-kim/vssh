package server

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Connect connects to vssh server
func Connect(host string, port int, secret string) error {
	// Connect + authenticate (VAUTH1 preferred, legacy HMAC fallback).
	conn, reader, err := dialAuth(host, port, secret, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connection failed: %v", err)
	}
	defer conn.Close()

	// Set terminal to raw mode
	oldState, err := makeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set raw mode: %v", err)
	}
	defer restoreTerminal(int(os.Stdin.Fd()), oldState)

	// Send initial window size
	sendWinsize(conn)

	// Handle SIGWINCH
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			sendWinsize(conn)
		}
	}()

	// Bidirectional copy
	done := make(chan struct{}, 2)

	// Server -> stdout (read via the handshake reader: it may hold buffered bytes)
	go func() {
		io.Copy(os.Stdout, reader)
		done <- struct{}{}
	}()

	// Stdin -> server
	go func() {
		io.Copy(conn, os.Stdin)
		done <- struct{}{}
	}()

	<-done
	return nil
}

func sendWinsize(conn net.Conn) {
	rows, cols := getWinsize()
	if rows > 0 && cols > 0 {
		conn.Write([]byte(fmt.Sprintf("\x1b[8;%d;%dt", rows, cols)))
	}
}
