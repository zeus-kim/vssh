package server

// VTLS1 (P0 transport migration, docs/SECURITY_TRANSPORT_MIGRATION.md):
// the existing line protocol now runs inside TLS 1.3. Identity is the same
// Ed25519 key as VAUTH1 (~/.vssh/vssh_id), packaged as a self-signed X.509
// certificate. Trust is raw-public-key pinning, NOT PKI: the daemon checks a
// client cert key against authorized_keys, the client checks the daemon key
// against ~/.vssh/known_hosts (TOFU on first contact). The cert is only a
// container the TLS stack requires.

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ALPN protocol tag for the vssh native protocol inside TLS.
const vsshALPN = "vssh/1"

var (
	tlsCertOnce sync.Once
	tlsCert     tls.Certificate
	tlsCertErr  error
)

// IdentityCertificate returns a self-signed X.509 certificate over this
// host's Ed25519 vssh identity (generated in memory, cached for the process
// lifetime). Validity is long and irrelevant: peers pin the raw key.
func IdentityCertificate() (tls.Certificate, error) {
	tlsCertOnce.Do(func() {
		priv, pubB64 := LoadOrCreateIdentity()
		if priv == nil || pubB64 == "" {
			tlsCertErr = errors.New("no vssh identity")
			return
		}
		host, _ := os.Hostname()
		tmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(time.Now().UnixNano()),
			Subject:               pkix.Name{CommonName: host},
			NotBefore:             time.Now().Add(-1 * time.Hour),
			NotAfter:              time.Now().Add(20 * 365 * 24 * time.Hour),
			KeyUsage:              x509.KeyUsageDigitalSignature,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
			BasicConstraintsValid: true,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, priv.Public(), priv)
		if err != nil {
			tlsCertErr = err
			return
		}
		tlsCert = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
	})
	return tlsCert, tlsCertErr
}

// PeerPubB64 extracts the base64 Ed25519 public key from a verified TLS
// connection's peer certificate, or "" when the peer sent no certificate.
func PeerPubB64(cs tls.ConnectionState) string {
	if len(cs.PeerCertificates) == 0 {
		return ""
	}
	pub, ok := cs.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
	if !ok {
		return ""
	}
	return base64.StdEncoding.EncodeToString(pub)
}

// ServerTLSConfig is the daemon-side config: TLS 1.3 only, ALPN vssh/1, with
// session-resumption tickets ENABLED so a client making repeated connections
// (an AI agent's tool loop, a fleet fan-out) skips the full asymmetric
// handshake on the 2nd+ connection. Resumption never weakens authorization:
// every connection still runs a fresh in-band VAUTH1 challenge–response
// (per-node Ed25519, server nonce, no replay); the TLS ticket only elides the
// key-exchange/cert cost, not identity. A client certificate is requested but
// not required (identity may still come from the VAUTH1 line, confidential
// inside the channel); when a cert IS presented it must be a parseable Ed25519
// cert cross-checked against the VAUTH1 key by handleConnection. Go rotates the
// ticket key automatically and TLS 1.3 keeps forward secrecy (PSK + ECDHE).
func ServerTLSConfig() (*tls.Config, error) {
	cert, err := IdentityCertificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{vsshALPN},
		ClientAuth:   tls.RequestClientCert,
	}, nil
}

// clientSessionCache is process-wide so a long-lived client (the `vssh mcp`
// server, or one CLI process fanning out across the fleet) reuses TLS tickets
// across dials. Go keys entries by ServerName, falling back to the dial address
// when ServerName is empty (our case) — so a ticket is only ever offered back
// to the same host:port it came from. Empty/one-shot CLI processes simply never
// get a cache hit and pay the full handshake, exactly as before.
var clientSessionCache = tls.NewLRUClientSessionCache(256)

// ClientTLSConfig builds the dialing config. pinnedPubB64, when non-empty,
// hard-pins the daemon's Ed25519 key (known_hosts hit); when empty the
// caller must TOFU-record the key from the connection state after the
// handshake. Certificate chain validation is intentionally disabled — trust
// is the raw key pin, exactly like authorized_keys.
func ClientTLSConfig(pinnedPubB64 string) (*tls.Config, error) {
	cert, err := IdentityCertificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		MinVersion:         tls.VersionTLS13,
		NextProtos:         []string{vsshALPN},
		ClientSessionCache: clientSessionCache, // resume across dials (skip full handshake)
		InsecureSkipVerify: true,               // trust = raw-key pin below, not WebPKI
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("vtls: daemon sent no certificate")
			}
			c, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("vtls: bad daemon certificate: %w", err)
			}
			pub, ok := c.PublicKey.(ed25519.PublicKey)
			if !ok {
				return errors.New("vtls: daemon certificate is not Ed25519")
			}
			if pinnedPubB64 != "" && base64.StdEncoding.EncodeToString(pub) != pinnedPubB64 {
				return errors.New("vtls: daemon key mismatch (known_hosts)")
			}
			return nil
		},
	}, nil
}

// --- known_hosts: <host> <pubB64> per line under ~/.vssh/known_hosts ---

func knownHostsPath() string {
	return filepath.Join(vsshConfigDir(), "known_hosts")
}

// KnownHostPub returns the pinned daemon key for host, or "". Only lines
// whose second field decodes to a raw 32-byte Ed25519 key are vssh pins;
// foreign formats sharing the file (e.g. OpenSSH-style "host ssh-ed25519
// <blob>" lines written by other tools) are ignored rather than misread.
func KnownHostPub(host string) string {
	data, err := os.ReadFile(knownHostsPath())
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] != host || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if raw, derr := base64.StdEncoding.DecodeString(fields[1]); derr == nil && len(raw) == ed25519.PublicKeySize {
			return fields[1]
		}
	}
	return ""
}

// RecordKnownHost TOFU-appends a host→key pin (no-op if already pinned).
func RecordKnownHost(host, pubB64 string) error {
	if host == "" || pubB64 == "" {
		return errors.New("empty host or key")
	}
	if KnownHostPub(host) != "" {
		return nil
	}
	if err := os.MkdirAll(vsshConfigDir(), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(knownHostsPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(host + " " + pubB64 + "\n")
	return err
}
