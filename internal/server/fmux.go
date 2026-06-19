package server

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// Phase B continued: FMUX carries many tunneled streams over ONE authenticated
// daemon connection (yamux), removing the per-connection VAUTH1 round trip
// that -L/-D tunnels paid until 0.7.22. Wire: after auth the client sends
// "FMUX\n"; the daemon replies "FMUX_OK\n" and the raw connection becomes a
// yamux session (daemon = yamux server). Every stream then opens with one
// header line:
//
//	"FWD <host> <port>\n" — TCP tunnel: the daemon dials the target, replies
//	  "FWD_OK\n" (or a typed JSON error) and splices bytes both ways.
//	"UDP\n" — UDP-associate relay (SOCKS5 UDP): the daemon replies "UDP_OK\n"
//	  and then both sides exchange length-prefixed datagram frames.
//
// UDP frame format (both directions): 2-byte big-endian payload length, then
// payload = SOCKS5-style address (ATYP[1] ADDR[…] PORT[2]) + datagram bytes.
// Client→daemon frames carry the destination; daemon→client frames carry the
// datagram's source.
//
// The session is gated (once) by the "forward" capability and every stream is
// audited against the control connection's authenticated identity.

// bufferedConn lets yamux consume bytes the auth handshake reader buffered.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

// HandleForwardMux serves the daemon side of an FMUX session.
func HandleForwardMux(conn net.Conn, reader *bufio.Reader) {
	if policyBlockUnscoped(conn, "FMUX") {
		return
	}
	reader.ReadString('\n') // consume the "FMUX" line
	conn.Write([]byte("FMUX_OK\n"))
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.LogOutput = io.Discard
	sess, err := yamux.Server(&bufferedConn{Conn: conn, r: reader}, cfg)
	if err != nil {
		return
	}
	defer sess.Close()
	for {
		stream, aerr := sess.AcceptStream()
		if aerr != nil {
			return
		}
		go fmuxServeStream(conn, sess, stream)
	}
}

func fmuxServeStream(ctl net.Conn, sess *yamux.Session, stream *yamux.Stream) {
	defer stream.Close()
	br := bufio.NewReader(stream)
	stream.SetReadDeadline(time.Now().Add(15 * time.Second))
	hdr, err := br.ReadString('\n')
	stream.SetReadDeadline(time.Time{})
	if err != nil {
		return
	}
	fields := strings.Fields(strings.TrimSpace(hdr))
	if len(fields) == 0 {
		writeFwdErr(stream, "empty fmux stream header", "bad_request")
		return
	}
	switch fields[0] {
	case "FWD":
		if len(fields) != 3 {
			writeFwdErr(stream, "usage: FWD <host> <port>", "bad_request")
			return
		}
		host, portStr := fields[1], fields[2]
		port, perr := strconv.Atoi(portStr)
		if perr != nil || port <= 0 || port > 65535 {
			writeFwdErr(stream, "invalid port: "+portStr, "bad_request")
			return
		}
		target, derr := net.DialTimeout("tcp", net.JoinHostPort(host, portStr), 10*time.Second)
		if derr != nil {
			writeFwdErr(stream, derr.Error(), "unreachable")
			auditLog(ctl, fmt.Sprintf("FWD %s:%d (fmux)", host, port), ExecCommandResult{Success: false, ExitCode: -1, Error: derr.Error()})
			return
		}
		defer target.Close()
		stream.Write([]byte("FWD_OK\n"))
		auditLog(ctl, fmt.Sprintf("FWD %s:%d (fmux)", host, port), ExecCommandResult{Success: true})
		bidiPipe(stream, br, target, target)
	case "UDP":
		us, uerr := net.ListenUDP("udp", nil)
		if uerr != nil {
			writeFwdErr(stream, uerr.Error(), "unreachable")
			return
		}
		defer us.Close()
		stream.Write([]byte("UDP_OK\n"))
		auditLog(ctl, "UDPA (fmux)", ExecCommandResult{Success: true})
		go func() { // target replies → client
			buf := make([]byte, 65535)
			for {
				n, addr, rerr := us.ReadFromUDP(buf)
				if rerr != nil {
					return
				}
				if _, werr := stream.Write(packUDPFrame(addr, buf[:n])); werr != nil {
					return
				}
			}
		}()
		for { // client datagrams → target
			addr, data, ferr := readUDPFrame(br)
			if ferr != nil {
				return
			}
			us.WriteToUDP(data, addr)
		}
	case "RFWD":
		if len(fields) != 3 {
			writeFwdErr(stream, "usage: RFWD <bindaddr> <bindport>", "bad_request")
			return
		}
		ln, lerr := net.Listen("tcp", net.JoinHostPort(fields[1], fields[2]))
		if lerr != nil {
			writeFwdErr(stream, lerr.Error(), "unreachable")
			return
		}
		defer ln.Close()
		stream.Write([]byte("RFWD_OK\n"))
		auditLog(ctl, fmt.Sprintf("RFWD %s:%s (fmux)", fields[1], fields[2]), ExecCommandResult{Success: true})
		go func() { // listener lives exactly as long as the control stream
			io.Copy(io.Discard, br)
			ln.Close()
		}()
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				// Push the accepted connection back as a fresh reverse stream —
				// no per-connection auth, no id pairing, no reaper.
				defer c.Close()
				st, oerr := sess.OpenStream()
				if oerr != nil {
					return
				}
				defer st.Close()
				st.Write([]byte("RCONN\n"))
				bidiPipe(st, st, c, c)
			}(c)
		}
	default:
		writeFwdErr(stream, "unknown fmux stream header", "bad_request")
	}
}

// packUDPFrame frames one datagram together with its peer address.
func packUDPFrame(addr *net.UDPAddr, data []byte) []byte {
	var ab []byte
	if v4 := addr.IP.To4(); v4 != nil {
		ab = append([]byte{0x01}, v4...)
	} else {
		ab = append([]byte{0x04}, addr.IP.To16()...)
	}
	ab = append(ab, byte(addr.Port>>8), byte(addr.Port&0xff))
	payload := append(ab, data...)
	frame := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(frame[:2], uint16(len(payload)))
	copy(frame[2:], payload)
	return frame
}

// readUDPFrame parses one frame; a domain ATYP resolves via the local resolver
// (that is the point: the *daemon's* resolver, like SOCKS5 by-hostname).
func readUDPFrame(br *bufio.Reader) (*net.UDPAddr, []byte, error) {
	lb := make([]byte, 2)
	if _, err := io.ReadFull(br, lb); err != nil {
		return nil, nil, err
	}
	l := int(binary.BigEndian.Uint16(lb))
	if l < 7 {
		return nil, nil, fmt.Errorf("short udp frame")
	}
	payload := make([]byte, l)
	if _, err := io.ReadFull(br, payload); err != nil {
		return nil, nil, err
	}
	var ip net.IP
	var host string
	idx := 1
	switch payload[0] {
	case 0x01:
		if l < 1+4+2 {
			return nil, nil, fmt.Errorf("bad ipv4 frame")
		}
		ip = net.IP(payload[1:5])
		idx = 5
	case 0x04:
		if l < 1+16+2 {
			return nil, nil, fmt.Errorf("bad ipv6 frame")
		}
		ip = net.IP(payload[1:17])
		idx = 17
	case 0x03:
		dl := int(payload[1])
		if l < 2+dl+2 {
			return nil, nil, fmt.Errorf("bad domain frame")
		}
		host = string(payload[2 : 2+dl])
		idx = 2 + dl
	default:
		return nil, nil, fmt.Errorf("bad atyp 0x%02x", payload[0])
	}
	port := int(payload[idx])<<8 | int(payload[idx+1])
	data := payload[idx+2:]
	if host != "" {
		ua, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return nil, nil, err
		}
		return ua, data, nil
	}
	return &net.UDPAddr{IP: ip, Port: port}, data, nil
}

// --- client side ---

// fmuxClient lazily maintains one authenticated FMUX session to a daemon (with
// transparent re-dial after a break), so every tunneled connection shares a
// single VAUTH1 handshake instead of paying one per connection.
type fmuxClient struct {
	host   string
	port   int
	secret string
	mu     sync.Mutex
	sess   *yamux.Session
	dead   bool // daemon predates FMUX — stop trying, use legacy FWD
}

func newFmuxClient(host string, port int, secret string) *fmuxClient {
	return &fmuxClient{host: host, port: port, secret: secret}
}

// open returns a fresh stream over the shared session, (re)establishing the
// session as needed. supported=false means the daemon does not speak FMUX and
// the caller should fall back to one-connection-per-tunnel FWD.
func (f *fmuxClient) open() (net.Conn, *bufio.Reader, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead {
		return nil, nil, false, fmt.Errorf("fmux unsupported by daemon")
	}
	if f.sess == nil || f.sess.IsClosed() {
		conn, reader, err := dialAuth(f.host, f.port, f.secret, 10*time.Second)
		if err != nil {
			return nil, nil, true, err
		}
		conn.Write([]byte("FMUX\n"))
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		line, rerr := reader.ReadString('\n')
		conn.SetReadDeadline(time.Time{})
		if rerr != nil || !strings.HasPrefix(line, "FMUX_OK") {
			conn.Close()
			f.dead = true
			return nil, nil, false, fmt.Errorf("daemon has no FMUX (pre-0.7.23): %s", strings.TrimSpace(line))
		}
		cfg := yamux.DefaultConfig()
		cfg.EnableKeepAlive = true
		cfg.LogOutput = io.Discard
		sess, serr := yamux.Client(&bufferedConn{Conn: conn, r: reader}, cfg)
		if serr != nil {
			conn.Close()
			return nil, nil, true, serr
		}
		f.sess = sess
	}
	stream, err := f.sess.OpenStream()
	if err != nil {
		f.sess.Close()
		f.sess = nil
		return nil, nil, true, err
	}
	return stream, bufio.NewReader(stream), true, nil
}

// currentSession returns the live session (nil if none) so reverse forwarding
// can accept daemon-initiated streams on it.
func (f *fmuxClient) currentSession() *yamux.Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sess
}
