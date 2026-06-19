package server

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Phase B: tunneling. The FWD verb turns an authenticated vssh connection into
// a raw TCP pipe to a target the *daemon* can reach — the building block that
// replaces `ssh -L`. Wire format: right after auth the client sends
// "FWD <host> <port>\n"; the daemon dials the target, replies "FWD_OK\n"
// (or a typed JSON error: bad_request / unreachable / capability_denied) and
// then proxies bytes both ways until either side closes. Every tunnel is
// audited server-side as "FWD <host>:<port>".

func writeFwdErr(conn net.Conn, msg, code string) {
	payload, _ := json.Marshal(map[string]interface{}{
		"success":       false,
		"error":         msg,
		"error_code":    code,
		"proto_version": ProtoVersion,
	})
	conn.Write(payload)
	conn.Write([]byte("\n"))
}

// HandleForward serves one FWD session on the daemon side.
func HandleForward(conn net.Conn, reader *bufio.Reader, cmd string) {
	fields := strings.Fields(strings.TrimSpace(cmd))
	if len(fields) != 3 {
		writeFwdErr(conn, "usage: FWD <host> <port>", "bad_request")
		return
	}
	host, portStr := fields[1], fields[2]
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		writeFwdErr(conn, "invalid port: "+portStr, "bad_request")
		return
	}
	if policyFwdDenied(conn, host, port) {
		return
	}
	target, err := net.DialTimeout("tcp", net.JoinHostPort(host, portStr), 10*time.Second)
	if err != nil {
		writeFwdErr(conn, err.Error(), "unreachable")
		auditLog(conn, fmt.Sprintf("FWD %s:%d", host, port), ExecCommandResult{Success: false, ExitCode: -1, Error: err.Error()})
		return
	}
	defer target.Close()
	conn.Write([]byte("FWD_OK\n"))
	auditLog(conn, fmt.Sprintf("FWD %s:%d", host, port), ExecCommandResult{Success: true})

	done := make(chan struct{}, 2)
	go func() {
		// client -> target; the handshake reader may hold buffered bytes, so
		// copy from it, never from the raw conn.
		io.Copy(target, reader)
		if tc, ok := target.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		io.Copy(conn, target)
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}

// ForwardLocal implements the client half of `ssh -L`: listen on localAddr and
// pipe every accepted connection to remoteHost:remotePort through the daemon at
// host:port. When the daemon speaks FMUX (0.7.23+) every tunneled connection is
// a stream on ONE shared authenticated session (a single VAUTH1 handshake);
// against older daemons it falls back to one authenticated connection per
// tunneled connection.
func ForwardLocal(host string, port int, secret, localAddr, remoteHost string, remotePort int) error {
	ln, err := net.Listen("tcp", localAddr)
	if err != nil {
		return err
	}
	defer ln.Close()
	fc := newFmuxClient(host, port, secret)
	fmt.Printf("vssh fwd: %s -> %s:%d via %s:%d (Ctrl-C to stop)\n", localAddr, remoteHost, remotePort, host, port)
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go func(c net.Conn) {
			defer c.Close()
			conn, reader, terr := fwdOpenTunnel(fc, host, port, secret, remoteHost, strconv.Itoa(remotePort))
			if terr != nil {
				fmt.Fprintf(os.Stderr, "fwd: %v\n", terr)
				return
			}
			defer conn.Close()
			bidiPipe(c, c, conn, reader)
		}(c)
	}
}

// fwdOpenTunnel opens one tunneled TCP connection to remoteHost:remotePort —
// preferring a stream on the shared FMUX session, falling back to a dedicated
// authenticated FWD connection when the daemon predates FMUX.
func fwdOpenTunnel(fc *fmuxClient, host string, port int, secret, remoteHost, remotePort string) (net.Conn, *bufio.Reader, error) {
	stream, sbr, supported, err := fc.open()
	if err == nil {
		fmt.Fprintf(stream, "FWD %s %s\n", remoteHost, remotePort)
		stream.SetReadDeadline(time.Now().Add(15 * time.Second))
		line, rerr := sbr.ReadString('\n')
		stream.SetReadDeadline(time.Time{})
		if rerr != nil || !strings.HasPrefix(line, "FWD_OK") {
			stream.Close()
			return nil, nil, fmt.Errorf("tunnel refused: %s", strings.TrimSpace(line))
		}
		return stream, sbr, nil
	}
	if supported {
		return nil, nil, err // transient FMUX/session failure — surface it
	}
	// Legacy daemon: one authenticated connection per tunneled connection.
	conn, reader, derr := dialAuth(host, port, secret, 10*time.Second)
	if derr != nil {
		return nil, nil, derr
	}
	fmt.Fprintf(conn, "FWD %s %s\n", remoteHost, remotePort)
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	line, rerr := reader.ReadString('\n')
	if rerr != nil || !strings.HasPrefix(line, "FWD_OK") {
		conn.Close()
		return nil, nil, fmt.Errorf("tunnel refused: %s", strings.TrimSpace(line))
	}
	conn.SetReadDeadline(time.Time{})
	return conn, reader, nil
}

// halfPipe copies src into dst, then half-closes dst's write side so the peer
// observes EOF. Used by every tunnel proxy loop.
func halfPipe(dst net.Conn, src io.Reader) {
	io.Copy(dst, src)
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := dst.(closeWriter); ok {
		cw.CloseWrite()
	}
}

// bidiPipe shuttles bytes both ways between a and b until both close. ar/br are
// the readers to consume from (a buffered reader may hold handshake bytes).
func bidiPipe(a net.Conn, ar io.Reader, b net.Conn, br io.Reader) {
	done := make(chan struct{}, 2)
	go func() { halfPipe(b, ar); done <- struct{}{} }()
	go func() { halfPipe(a, br); done <- struct{}{} }()
	<-done
	<-done
}

func randID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// --- Reverse forwarding (ssh -R): the daemon binds a port and pushes each
// accepted connection back to the client, which connects to its local target. ---

var (
	revMu      sync.Mutex
	revPending = map[string]net.Conn{}
)

// HandleReverseForward serves the control side of a reverse tunnel. Wire:
// client sends "RFWD <bindaddr> <bindport>"; daemon listens, replies RFWD_OK,
// and for every accepted connection writes "ACCEPT <id>" back over the control
// connection. The client then opens a fresh authenticated connection carrying
// "RDATA <id>" (HandleReverseData) which the daemon pairs with the pending
// accept. The listener lives as long as the control connection.
func HandleReverseForward(conn net.Conn, reader *bufio.Reader, cmd string) {
	if policyBlockUnscoped(conn, "RFWD") {
		return
	}
	fields := strings.Fields(strings.TrimSpace(cmd))
	if len(fields) != 3 {
		writeFwdErr(conn, "usage: RFWD <bindaddr> <bindport>", "bad_request")
		return
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(fields[1], fields[2]))
	if err != nil {
		writeFwdErr(conn, err.Error(), "unreachable")
		return
	}
	defer ln.Close()
	conn.Write([]byte("RFWD_OK\n"))
	auditLog(conn, fmt.Sprintf("RFWD %s:%s", fields[1], fields[2]), ExecCommandResult{Success: true})

	// Tear the listener down when the control connection closes.
	go func() {
		buf := make([]byte, 1)
		for {
			if _, e := reader.Read(buf); e != nil {
				ln.Close()
				return
			}
		}
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		id := randID()
		revMu.Lock()
		revPending[id] = c
		revMu.Unlock()
		if _, werr := conn.Write([]byte("ACCEPT " + id + "\n")); werr != nil {
			revMu.Lock()
			delete(revPending, id)
			revMu.Unlock()
			c.Close()
			return
		}
		// Reap an accept the client never claims (e.g. its local dial failed).
		go func(id string, c net.Conn) {
			time.Sleep(15 * time.Second)
			revMu.Lock()
			if revPending[id] == c {
				delete(revPending, id)
				c.Close()
			}
			revMu.Unlock()
		}(id, c)
	}
}

// HandleReverseData pairs a client data connection with a pending reverse accept
// and proxies between them. Wire: "RDATA <id>" -> "RDATA_OK" then raw bytes.
func HandleReverseData(conn net.Conn, reader *bufio.Reader, cmd string) {
	if policyBlockUnscoped(conn, "RDAT") {
		return
	}
	fields := strings.Fields(strings.TrimSpace(cmd))
	if len(fields) != 2 {
		writeFwdErr(conn, "usage: RDATA <id>", "bad_request")
		return
	}
	revMu.Lock()
	ac := revPending[fields[1]]
	delete(revPending, fields[1])
	revMu.Unlock()
	if ac == nil {
		writeFwdErr(conn, "no such pending connection", "bad_request")
		return
	}
	defer ac.Close()
	conn.Write([]byte("RDATA_OK\n"))
	bidiPipe(conn, reader, ac, ac)
}

// ForwardRemote implements the client half of `ssh -R`. Against an FMUX-capable
// (0.7.23+) daemon the whole reverse tunnel rides ONE authenticated session: a
// control stream carries "RFWD <bind> <port>" and every accepted connection
// comes back as a daemon-initiated "RCONN" stream (no per-connection auth, no
// id pairing). The session is re-established automatically if it breaks.
// Pre-FMUX daemons fall back to the legacy RFWD/RDATA per-accept path.
func ForwardRemote(host string, port int, secret, bindAddr, bindPort, localHost string, localPort int) error {
	fc := newFmuxClient(host, port, secret)
	printed := false
	for {
		supported, fatal, err := forwardRemoteFmuxOnce(fc, bindAddr, bindPort, localHost, localPort, &printed)
		if !supported {
			return forwardRemoteLegacy(host, port, secret, bindAddr, bindPort, localHost, localPort)
		}
		if fatal {
			return err
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "fwd -R: session lost (%v), retrying in 2s\n", err)
			time.Sleep(2 * time.Second)
		}
	}
}

// forwardRemoteFmuxOnce runs one reverse-tunnel session lifetime. fatal=true
// means the daemon actively refused the bind (no point retrying).
func forwardRemoteFmuxOnce(fc *fmuxClient, bindAddr, bindPort, localHost string, localPort int, printed *bool) (supported, fatal bool, err error) {
	ctl, cbr, supported, err := fc.open()
	if err != nil {
		return supported, false, err
	}
	defer ctl.Close()
	fmt.Fprintf(ctl, "RFWD %s %s\n", bindAddr, bindPort)
	ctl.SetReadDeadline(time.Now().Add(15 * time.Second))
	line, rerr := cbr.ReadString('\n')
	ctl.SetReadDeadline(time.Time{})
	if rerr != nil {
		return true, false, rerr
	}
	if !strings.HasPrefix(line, "RFWD_OK") {
		// A 0.7.23 daemon speaks FMUX but not the RFWD stream header — it
		// answers bad_request. Use the legacy RFWD/RDATA path there.
		if strings.Contains(line, "unknown fmux stream header") {
			return false, false, fmt.Errorf("daemon fmux predates RFWD streams")
		}
		return true, true, fmt.Errorf("reverse tunnel refused: %s", strings.TrimSpace(line))
	}
	if !*printed {
		fmt.Printf("vssh fwd -R: %s:%s -> %s:%d over one fmux session (Ctrl-C to stop)\n", bindAddr, bindPort, localHost, localPort)
		*printed = true
	}
	sess := fc.currentSession()
	if sess == nil {
		return true, false, fmt.Errorf("fmux session vanished")
	}
	// Control-stream EOF (daemon listener died) must unblock AcceptStream.
	go func() {
		io.Copy(io.Discard, cbr)
		sess.Close()
	}()
	for {
		st, aerr := sess.AcceptStream()
		if aerr != nil {
			return true, false, aerr
		}
		go func(st net.Conn) {
			defer st.Close()
			sbr := bufio.NewReader(st)
			st.SetReadDeadline(time.Now().Add(15 * time.Second))
			hdr, herr := sbr.ReadString('\n')
			st.SetReadDeadline(time.Time{})
			if herr != nil || !strings.HasPrefix(hdr, "RCONN") {
				return
			}
			local, lerr := net.DialTimeout("tcp", net.JoinHostPort(localHost, strconv.Itoa(localPort)), 10*time.Second)
			if lerr != nil {
				fmt.Fprintf(os.Stderr, "fwd -R: local dial: %v\n", lerr)
				return
			}
			defer local.Close()
			bidiPipe(st, sbr, local, local)
		}(st)
	}
}

// forwardRemoteLegacy is the pre-FMUX path: RFWD control connection + one
// authenticated RDATA connection per accepted connection.
func forwardRemoteLegacy(host string, port int, secret, bindAddr, bindPort, localHost string, localPort int) error {
	conn, reader, err := dialAuth(host, port, secret, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	fmt.Fprintf(conn, "RFWD %s %s\n", bindAddr, bindPort)
	line, rerr := reader.ReadString('\n')
	if rerr != nil || !strings.HasPrefix(line, "RFWD_OK") {
		return fmt.Errorf("reverse tunnel refused: %s", strings.TrimSpace(line))
	}
	fmt.Printf("vssh fwd -R: %s:%s on %s:%d -> %s:%d (Ctrl-C to stop)\n", bindAddr, bindPort, host, port, localHost, localPort)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ACCEPT ") {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(line, "ACCEPT "))
		go func(id string) {
			dconn, dreader, derr := dialAuth(host, port, secret, 10*time.Second)
			if derr != nil {
				fmt.Fprintf(os.Stderr, "fwd -R: %v\n", derr)
				return
			}
			defer dconn.Close()
			fmt.Fprintf(dconn, "RDATA %s\n", id)
			ok, oerr := dreader.ReadString('\n')
			if oerr != nil || !strings.HasPrefix(ok, "RDATA_OK") {
				return
			}
			local, lerr := net.DialTimeout("tcp", net.JoinHostPort(localHost, strconv.Itoa(localPort)), 10*time.Second)
			if lerr != nil {
				fmt.Fprintf(os.Stderr, "fwd -R: local dial: %v\n", lerr)
				return
			}
			defer local.Close()
			bidiPipe(dconn, dreader, local, local)
		}(id)
	}
}

// --- Dynamic forwarding (ssh -D): a local SOCKS5 proxy. CONNECT goes through
// the daemon (FMUX stream when available, else one FWD connection each) and
// UDP ASSOCIATE relays datagrams through an FMUX "UDP" stream — full SOCKS5
// TCP+UDP against 0.7.23+ daemons. ---

// ForwardSocks runs a SOCKS5 proxy on localAddr; every CONNECT/ASSOCIATE is
// forwarded to its destination through the daemon at host:port.
func ForwardSocks(host string, port int, secret, localAddr string) error {
	ln, err := net.Listen("tcp", localAddr)
	if err != nil {
		return err
	}
	defer ln.Close()
	fc := newFmuxClient(host, port, secret)
	fmt.Printf("vssh fwd -D: SOCKS5 (tcp+udp) on %s via %s:%d (Ctrl-C to stop)\n", localAddr, host, port)
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go socksServe(c, fc, host, port, secret)
	}
}

func socksServe(c net.Conn, fc *fmuxClient, host string, port int, secret string) {
	defer c.Close()
	br := bufio.NewReader(c)
	// Greeting: VER, NMETHODS, METHODS...
	ver, err := br.ReadByte()
	if err != nil || ver != 0x05 {
		return
	}
	nm, err := br.ReadByte()
	if err != nil {
		return
	}
	if _, err := io.CopyN(io.Discard, br, int64(nm)); err != nil {
		return
	}
	c.Write([]byte{0x05, 0x00}) // no auth
	// Request: VER, CMD, RSV, ATYP
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return
	}
	var dst string
	switch hdr[3] {
	case 0x01:
		b := make([]byte, 4)
		io.ReadFull(br, b)
		dst = net.IP(b).String()
	case 0x03:
		l, _ := br.ReadByte()
		b := make([]byte, int(l))
		io.ReadFull(br, b)
		dst = string(b)
	case 0x04:
		b := make([]byte, 16)
		io.ReadFull(br, b)
		dst = net.IP(b).String()
	default:
		c.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(br, pb); err != nil {
		return
	}
	dport := int(pb[0])<<8 | int(pb[1])

	switch hdr[1] {
	case 0x01: // CONNECT
		conn, reader, terr := fwdOpenTunnel(fc, host, port, secret, dst, strconv.Itoa(dport))
		if terr != nil {
			c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // connection refused
			return
		}
		defer conn.Close()
		c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // success
		bidiPipe(c, br, conn, reader)
	case 0x03: // UDP ASSOCIATE
		socksUDPAssociate(c, br, fc)
	default:
		c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // command not supported
	}
}

// socksUDPAssociate serves one SOCKS5 UDP association: a local UDP socket for
// the app, relayed through an FMUX "UDP" stream so the datagrams egress from
// the daemon. Requires an FMUX-capable (0.7.23+) daemon.
func socksUDPAssociate(c net.Conn, br *bufio.Reader, fc *fmuxClient) {
	stream, sbr, _, err := fc.open()
	if err != nil {
		c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // not supported (no FMUX daemon)
		return
	}
	defer stream.Close()
	stream.Write([]byte("UDP\n"))
	stream.SetReadDeadline(time.Now().Add(15 * time.Second))
	line, rerr := sbr.ReadString('\n')
	stream.SetReadDeadline(time.Time{})
	if rerr != nil || !strings.HasPrefix(line, "UDP_OK") {
		c.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	lip := net.IPv4(127, 0, 0, 1)
	if ta, ok := c.LocalAddr().(*net.TCPAddr); ok && ta.IP != nil && !ta.IP.IsUnspecified() {
		lip = ta.IP
	}
	us, uerr := net.ListenUDP("udp", &net.UDPAddr{IP: lip})
	if uerr != nil {
		c.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer us.Close()
	// Reply with the UDP relay address the app must send to.
	ua := us.LocalAddr().(*net.UDPAddr)
	rep := []byte{0x05, 0x00, 0x00}
	if v4 := ua.IP.To4(); v4 != nil {
		rep = append(rep, 0x01)
		rep = append(rep, v4...)
	} else {
		rep = append(rep, 0x04)
		rep = append(rep, ua.IP.To16()...)
	}
	rep = append(rep, byte(ua.Port>>8), byte(ua.Port&0xff))
	c.Write(rep)

	var peer atomic.Value // *net.UDPAddr: the app's UDP source address
	go func() {           // app -> daemon
		buf := make([]byte, 65535)
		for {
			n, a, rerr := us.ReadFromUDP(buf)
			if rerr != nil {
				return
			}
			peer.Store(a)
			// SOCKS5 UDP request: RSV(2) FRAG(1) ATYP ADDR PORT DATA
			if n < 5 || buf[2] != 0x00 {
				continue // fragmentation unsupported
			}
			payload := buf[3:n]
			frame := make([]byte, 2+len(payload))
			binary.BigEndian.PutUint16(frame[:2], uint16(len(payload)))
			copy(frame[2:], payload)
			if _, werr := stream.Write(frame); werr != nil {
				return
			}
		}
	}()
	go func() { // daemon -> app
		for {
			lb := make([]byte, 2)
			if _, e := io.ReadFull(sbr, lb); e != nil {
				us.Close()
				return
			}
			l := int(binary.BigEndian.Uint16(lb))
			payload := make([]byte, l)
			if _, e := io.ReadFull(sbr, payload); e != nil {
				us.Close()
				return
			}
			pa, _ := peer.Load().(*net.UDPAddr)
			if pa == nil {
				continue
			}
			pkt := append([]byte{0x00, 0x00, 0x00}, payload...)
			us.WriteToUDP(pkt, pa)
		}
	}()
	// The association lives exactly as long as the TCP control connection.
	io.Copy(io.Discard, br)
}
