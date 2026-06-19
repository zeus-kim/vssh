package server

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
)

// TestVAUTHDomainSeparation: signatures are domain-separated, and the verifier
// accepts both the new tagged signature and the legacy bare-nonce one (so a
// fleet mid-rollout never locks out). A tampered signature is rejected.
func TestVAUTHDomainSeparation(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	nonce := NewNonce()

	// new signer -> tagged signature
	tagged := SignChallenge(priv, nonce)
	if !VerifyChallenge(pubB64, nonce, tagged) {
		t.Error("tagged signature must verify")
	}
	// legacy bare-nonce signature must still verify (dual-accept)
	bare := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, nonce))
	if !VerifyChallenge(pubB64, nonce, bare) {
		t.Error("legacy bare-nonce signature must still verify during rollout")
	}
	// the tagged signature must actually be over the domain-prefixed payload
	if ed25519.Verify(pub, nonce, mustB64(t, tagged)) {
		t.Error("tagged signature should NOT verify against the bare nonce (no domain separation!)")
	}
	// tampered nonce rejected
	bad := append([]byte(nil), nonce...)
	bad[0] ^= 0xff
	if VerifyChallenge(pubB64, bad, tagged) {
		t.Error("signature must not verify against a different nonce")
	}
}

func mustB64(t *testing.T, s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
