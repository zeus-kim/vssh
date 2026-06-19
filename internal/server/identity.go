package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Phase A security core: per-node Ed25519 identity + challenge–response auth.
// This replaces the shared-HMAC trust model (one secret for the whole fleet, and
// replayable within its 30s window) with asymmetric per-node keys and a server-issued
// nonce that the client must sign — no shared secret, no replay.
// The legacy shared-secret/HMAC path was fully removed (v0.7.39): the daemon
// now accepts ONLY VAUTH1 and rejects any other auth line.

func vsshConfigDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".vssh")
	}
	return "/etc/vssh"
}

var (
	identityOnce sync.Once
	cachedPriv   ed25519.PrivateKey
	cachedPubB64 string
)

// LoadOrCreateIdentity returns this host's Ed25519 identity (private key + base64
// public key), generating and persisting it on first use under ~/.vssh/vssh_id.
func LoadOrCreateIdentity() (ed25519.PrivateKey, string) {
	identityOnce.Do(func() {
		dir := vsshConfigDir()
		keyPath := filepath.Join(dir, "vssh_id")
		if data, err := os.ReadFile(keyPath); err == nil {
			if raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data))); derr == nil && len(raw) == ed25519.PrivateKeySize {
				cachedPriv = ed25519.PrivateKey(raw)
				cachedPubB64 = base64.StdEncoding.EncodeToString(cachedPriv.Public().(ed25519.PublicKey))
				return
			}
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return
		}
		os.MkdirAll(dir, 0700)
		os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(priv)), 0600)
		os.WriteFile(keyPath+".pub", []byte(base64.StdEncoding.EncodeToString(pub)), 0644)
		cachedPriv = priv
		cachedPubB64 = base64.StdEncoding.EncodeToString(pub)
	})
	return cachedPriv, cachedPubB64
}

// NewNonce returns a fresh 32-byte random challenge.
func NewNonce() []byte {
	n := make([]byte, 32)
	rand.Read(n)
	return n
}

// SignChallenge signs a nonce with the private key, returning base64.
// vauthDomain is a signature domain-separation prefix (F4): the challenge is
// signed over vauthDomain||nonce, not the bare nonce, so a VAUTH1 signature
// cannot be cross-used as a signature in any other context that signs raw bytes.
const vauthDomain = "VSSH-VAUTH1\x00"

func vauthChallengeBytes(nonce []byte) []byte {
	b := make([]byte, 0, len(vauthDomain)+len(nonce))
	b = append(b, vauthDomain...)
	b = append(b, nonce...)
	return b
}

func SignChallenge(priv ed25519.PrivateKey, nonce []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, vauthChallengeBytes(nonce)))
}

// VerifyChallenge checks a base64 signature of nonce against a base64 public key.
func VerifyChallenge(pubB64 string, nonce []byte, sigB64 string) bool {
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	ppub := ed25519.PublicKey(pub)
	// Accept the domain-separated signature (current) OR the legacy bare-nonce
	// signature (pre-0.7.47 peers) so the rollout is non-breaking: every verifier
	// accepts both before any signer switches to the tagged form.
	return ed25519.Verify(ppub, vauthChallengeBytes(nonce), sig) || ed25519.Verify(ppub, nonce, sig)
}

// IsAuthorizedKey reports whether a base64 public key is trusted, i.e. listed in
// authorized_keys under ~/.vssh or /etc/vssh.
func IsAuthorizedKey(pubB64 string) bool {
	_, ok := KeyCapabilities(pubB64)
	return ok
}

// KeyCapabilities reports whether a base64 public key is authorized and which
// capability set its authorized_keys line grants. Line format:
//
//	<pubB64> [caps=exec,file,rpc,shell] [comment...]
//
// A line without a caps= tag (or with caps=all) grants every capability, so
// existing keys keep working unchanged. A nil map with ok=true means
// unrestricted; a non-nil map restricts the connection to the listed verbs.
func KeyCapabilities(pubB64 string) (map[string]bool, bool) {
	pubB64 = strings.TrimSpace(pubB64)
	if pubB64 == "" {
		return nil, false
	}
	paths := []string{filepath.Join(vsshConfigDir(), "authorized_keys"), "/etc/vssh/authorized_keys"}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 0 || fields[0] != pubB64 {
				continue
			}
			for _, f := range fields[1:] {
				if strings.HasPrefix(f, "caps=") {
					caps := map[string]bool{}
					for _, c := range strings.Split(strings.TrimPrefix(f, "caps="), ",") {
						if c = strings.ToLower(strings.TrimSpace(c)); c != "" {
							caps[c] = true
						}
					}
					if caps["all"] || len(caps) == 0 {
						return nil, true
					}
					return caps, true
				}
			}
			return nil, true
		}
	}
	return nil, false
}

// KeyName returns the human comment on an authorized key's line (the fields
// that are not the pubkey and not the caps= tag), or "" when the key is
// unknown or uncommented. Used to attribute audit records to a named key.
func KeyName(pubB64 string) string {
	pubB64 = strings.TrimSpace(pubB64)
	if pubB64 == "" {
		return ""
	}
	paths := []string{filepath.Join(vsshConfigDir(), "authorized_keys"), "/etc/vssh/authorized_keys"}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 0 || fields[0] != pubB64 {
				continue
			}
			var comment []string
			for _, f := range fields[1:] {
				if strings.HasPrefix(f, "caps=") || strings.HasPrefix(f, "policy=") {
					continue
				}
				comment = append(comment, f)
			}
			return strings.Join(comment, " ")
		}
	}
	return ""
}

// KeyPolicy returns the policy name attached to a key's authorized_keys line
// (the policy=<name> tag) and whether the tag is present. A present tag whose
// policy file is missing/invalid must fail closed at enforcement time (docs §6.4),
// so callers distinguish "no tag" (hasTag=false, current behavior) from "tagged".
func KeyPolicy(pubB64 string) (string, bool) {
	pubB64 = strings.TrimSpace(pubB64)
	if pubB64 == "" {
		return "", false
	}
	paths := []string{filepath.Join(vsshConfigDir(), "authorized_keys"), "/etc/vssh/authorized_keys"}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 0 || fields[0] != pubB64 {
				continue
			}
			for _, f := range fields[1:] {
				if strings.HasPrefix(f, "policy=") {
					return strings.TrimSpace(strings.TrimPrefix(f, "policy=")), true
				}
			}
			return "", false
		}
	}
	return "", false
}

// ConfigNodeIP returns the canonical configured IP for a node name from
// ~/.vssh/config or /etc/vssh/config ("name=ip" lines). Used to look up the
// node's TRUSTED pinned daemon key independent of live (possibly buggy) address
// resolution — the basis of host-identity verification (docs: d2 misroute fix).
func ConfigNodeIP(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return ""
	}
	paths := []string{filepath.Join(vsshConfigDir(), "config"), "/etc/vssh/config"}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
				continue
			}
			kv := strings.SplitN(line, "=", 2)
			if strings.TrimSpace(strings.ToLower(kv[0])) == name {
				return strings.TrimSpace(kv[1])
			}
		}
	}
	return ""
}

// NodeKey returns a node's authoritative daemon TLS key from the trusted
// name->key registry (~/.vssh/node_keys or /etc/vssh/node_keys), built by
// scripts/build_node_registry.sh via loopback handshakes (cannot be misrouted).
// "" = unknown node (host-identity verification then skips, fail-open by absence
// but safe — only KNOWN nodes are enforced). This is the correct source: the
// daemon's real TLS key, independent of the HOME-dependent vssh_id path.
func NodeKey(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return ""
	}
	for _, p := range []string{filepath.Join(vsshConfigDir(), "node_keys"), "/etc/vssh/node_keys"} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 && strings.ToLower(f[0]) == name {
				return f[1]
			}
		}
	}
	return ""
}

// IdentityKeyPath returns the path to this host's persisted Ed25519 identity.
func IdentityKeyPath() string { return filepath.Join(vsshConfigDir(), "vssh_id") }

// CurrentIdentityPub returns the base64 public key of the persisted identity, or
// "" if none exists. It reads disk (no cache) so it reflects rotations.
func CurrentIdentityPub() string {
	data, err := os.ReadFile(IdentityKeyPath())
	if err != nil {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return ""
	}
	priv := ed25519.PrivateKey(raw)
	return base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
}

// RotateIdentity backs up any existing identity and writes a freshly generated
// Ed25519 key, returning the new base64 public key and the backup path ("" when
// there was no prior key). The running daemon keeps the OLD key until restarted.
func RotateIdentity() (pubB64 string, backup string, err error) {
	dir := vsshConfigDir()
	keyPath := IdentityKeyPath()
	if data, rerr := os.ReadFile(keyPath); rerr == nil {
		backup = keyPath + ".bak." + time.Now().UTC().Format("20060102T150405Z")
		if werr := os.WriteFile(backup, data, 0600); werr != nil {
			return "", "", werr
		}
		if pubOld, perr := os.ReadFile(keyPath + ".pub"); perr == nil {
			_ = os.WriteFile(backup+".pub", pubOld, 0644)
		}
	}
	pub, priv, gerr := ed25519.GenerateKey(rand.Reader)
	if gerr != nil {
		return "", backup, gerr
	}
	if merr := os.MkdirAll(dir, 0700); merr != nil {
		return "", backup, merr
	}
	if werr := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(priv)), 0600); werr != nil {
		return "", backup, werr
	}
	_ = os.WriteFile(keyPath+".pub", []byte(base64.StdEncoding.EncodeToString(pub)), 0644)
	return base64.StdEncoding.EncodeToString(pub), backup, nil
}
