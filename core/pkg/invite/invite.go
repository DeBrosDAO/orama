// Package invite encodes everything a node needs to join a cluster into one
// string the operator copies once.
//
// A join used to need three values transcribed by hand — the gateway URL, a
// 64-character hex token and a 64-character certificate fingerprint — from the
// output of one command into the arguments of another, on a different machine.
// Two of those are indistinguishable strings of hex, so getting them the wrong
// way round produced a TLS failure that read as a network problem. And the
// fingerprint was optional, so the usual outcome of finding it awkward was to
// leave it out and fall back to trust-on-first-use.
//
// One value cannot be transcribed the wrong way round, and it cannot be
// partially copied: it is either valid or it does not decode.
package invite

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

// prefix marks an encoded invite and names its version, so a later format can
// be recognised rather than guessed at.
const prefix = "orama1_"

// Invite is what a joining node is told.
type Invite struct {
	// JoinURL is the gateway to join through.
	JoinURL string `json:"u"`
	// Token is the single-use secret that authorises the join.
	Token string `json:"t"`
	// CAFingerprint is the SHA-256 of the gateway's TLS certificate, which the
	// joining node pins instead of trusting the first certificate it is shown.
	CAFingerprint string `json:"f,omitempty"`
}

// Encode renders an invite as one copyable string.
func Encode(inv Invite) (string, error) {
	if inv.Token == "" {
		return "", fmt.Errorf("an invite needs a token")
	}
	if inv.JoinURL == "" {
		return "", fmt.Errorf("an invite needs a join URL")
	}

	body, err := json.Marshal(inv)
	if err != nil {
		return "", fmt.Errorf("encode invite: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(body), nil
}

// Decode reads an invite string.
//
// A bare token is accepted and returned with no URL and no fingerprint. That
// is what every token issued before this format looks like, and a cluster
// mid-upgrade hands them out; the caller supplies --join and --ca-fingerprint
// for those, as it always did.
func Decode(s string) (Invite, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Invite{}, fmt.Errorf("empty invite")
	}

	if !strings.HasPrefix(s, prefix) {
		if !isBareToken(s) {
			return Invite{}, fmt.Errorf("not an invite: expected a string starting %q, or a 64-character hex token", prefix)
		}
		return Invite{Token: s}, nil
	}

	body, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, prefix))
	if err != nil {
		return Invite{}, fmt.Errorf("invite is not valid base64: %w", err)
	}

	var inv Invite
	if err := json.Unmarshal(body, &inv); err != nil {
		return Invite{}, fmt.Errorf("invite is not valid: %w", err)
	}
	if inv.Token == "" {
		return Invite{}, fmt.Errorf("invite carries no token")
	}
	if inv.JoinURL == "" {
		return Invite{}, fmt.Errorf("invite carries no join URL")
	}
	return inv, nil
}

// tokenLength is the hex length of a 32-byte token.
const tokenLength = 64

// isBareToken reports whether s looks like a pre-format invite token.
func isBareToken(s string) bool {
	if len(s) != tokenLength {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// fingerprintTimeout bounds the TLS handshake used to read a certificate.
const fingerprintTimeout = 5 * time.Second

// Fingerprint returns the SHA-256 of the leaf certificate host serves.
//
// host may carry a scheme and a port; both are normalised away, and 443 is
// assumed. An empty string means the certificate could not be read, which the
// caller reports rather than silently issuing an invite with no pinning.
func Fingerprint(host string) (string, error) {
	target := normalizeHostPort(host)

	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: fingerprintTimeout},
		"tcp", target,
		&tls.Config{MinVersion: tls.VersionTLS12},
	)
	if err != nil {
		return "", fmt.Errorf("could not read the TLS certificate of %s: %w", target, err)
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("%s presented no certificate", target)
	}

	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), nil
}

// normalizeHostPort turns a gateway URL or bare host into host:port.
func normalizeHostPort(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimSuffix(host, "/")
	if idx := strings.IndexByte(host, '/'); idx >= 0 {
		host = host[:idx]
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "443")
	}
	return host
}
