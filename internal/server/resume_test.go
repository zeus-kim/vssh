package server

import (
	"crypto/tls"
	"net"
	"testing"
)

// TestTLSSessionResumption proves that the server issues resumption tickets and
// the process-wide client cache reuses them: the 2nd dial to the same address
// resumes (skipping the full asymmetric handshake), while the 1st is a full
// handshake. Regression guard for anyone tempted to re-disable tickets.
func TestTLSSessionResumption(t *testing.T) {
	srvCfg, err := ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			tc := tls.Server(c, srvCfg)
			if tc.Handshake() == nil {
				// A post-handshake write lets Go flush the TLS 1.3
				// NewSessionTicket the client needs to cache.
				tc.Write([]byte("x"))
			}
			tc.Close()
		}
	}()

	dial := func() (bool, error) {
		cfg, cerr := ClientTLSConfig("")
		if cerr != nil {
			return false, cerr
		}
		c, derr := tls.Dial("tcp", ln.Addr().String(), cfg)
		if derr != nil {
			return false, derr
		}
		defer c.Close()
		// Read the server byte so the NewSessionTicket message is processed and
		// stored in clientSessionCache before we inspect resumption.
		buf := make([]byte, 1)
		c.Read(buf)
		return c.ConnectionState().DidResume, nil
	}

	if resumed, derr := dial(); derr != nil || resumed {
		t.Fatalf("first dial: resumed=%v err=%v (want a full handshake)", resumed, derr)
	}
	resumed, derr := dial()
	if derr != nil {
		t.Fatalf("second dial: %v", derr)
	}
	if !resumed {
		t.Fatal("second dial did not resume — server tickets or client cache not working")
	}
}
