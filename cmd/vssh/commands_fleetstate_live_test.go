package main

import (
	"net"
	"testing"
	"time"
)

func TestDialAlive(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !dialAlive(ln.Addr().String(), time.Second) {
		t.Fatal("open listener should be alive")
	}
	// a port we just closed should be dead
	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln2.Addr().String()
	ln2.Close()
	if dialAlive(addr, 300*time.Millisecond) {
		t.Fatal("closed port should be dead")
	}
}
