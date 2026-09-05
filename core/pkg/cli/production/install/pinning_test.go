package install

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"strings"
	"testing"
)

// Joining sends the invite token, which is a credential for every secret the
// cluster holds. Without a fingerprint the client used to set
// InsecureSkipVerify and check nothing, so the token went to whoever answered
// the address — and a machine in the path could take it and join instead.
func TestPinnedTLSConfig_refusesToJoinWithNothingToPin(t *testing.T) {
	for _, fingerprint := range []string{"", "   ", "\t\n"} {
		cfg, err := pinnedTLSConfig(fingerprint)
		if err == nil {
			t.Fatalf("pinnedTLSConfig(%q) built a client with nothing to verify the far end with", fingerprint)
		}
		if cfg != nil {
			t.Error("a config was returned alongside the error")
		}
		if !strings.Contains(err.Error(), "invite") {
			t.Errorf("the refusal does not say where a fingerprint comes from: %v", err)
		}
	}
}

func TestPinnedTLSConfig_refusesAFingerprintItCannotUse(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fingerprint string
	}{
		{"not hex", "zzzz"},
		{"too short", hex.EncodeToString(make([]byte, 16))},
		{"too long", hex.EncodeToString(make([]byte, 48))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pinnedTLSConfig(tc.fingerprint); err == nil {
				t.Errorf("pinnedTLSConfig(%q) was accepted", tc.fingerprint)
			}
		})
	}
}

func TestPinnedTLSConfig_verifiesTheCertificateTheInviteNamed(t *testing.T) {
	cert := []byte("a certificate, as bytes")
	sum := sha256.Sum256(cert)

	cfg, err := pinnedTLSConfig(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("pinnedTLSConfig: %v", err)
	}
	if cfg.VerifyPeerCertificate == nil {
		t.Fatal("no verification was installed")
	}

	if err := cfg.VerifyPeerCertificate([][]byte{cert}, nil); err != nil {
		t.Errorf("the certificate the invite named was refused: %v", err)
	}

	other := []byte("somebody else's certificate")
	err = cfg.VerifyPeerCertificate([][]byte{other}, nil)
	if err == nil {
		t.Fatal("a different certificate was accepted")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("the refusal does not say what happened: %v", err)
	}

	if err := cfg.VerifyPeerCertificate(nil, nil); err == nil {
		t.Error("a server that presented no certificate was accepted")
	}
}

// The chain check is off because a cluster node's certificate is issued for its
// own domain with no CA to chain to; the pin is the check. If the pin were ever
// dropped while InsecureSkipVerify stayed, the result would be no verification
// at all — which is the state this replaced.
func TestPinnedTLSConfig_skipsTheChainOnlyBecauseItPins(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	cfg, err := pinnedTLSConfig(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("pinnedTLSConfig: %v", err)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("the chain check is on, so the pin is not the only check and this comment is stale")
	}
	if cfg.VerifyPeerCertificate == nil {
		t.Fatal("the chain check is off and nothing replaces it")
	}
	var _ *tls.Config = cfg
}
