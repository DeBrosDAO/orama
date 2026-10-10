//go:build e2e_fleet

package smoke

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const handshakeBudget = 15 * time.Second

func gatewayHost(t *testing.T) (host, addr string) {
	t.Helper()
	u, err := url.Parse(harness.Fleet(t).State.GatewayURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname(), net.JoinHostPort(u.Hostname(), "443")
}

func handshake(t *testing.T, cfg *tls.Config, addr string) (*tls.ConnectionState, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), handshakeBudget)
	defer cancel()
	conn, err := (&tls.Dialer{Config: cfg}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	st := conn.(*tls.Conn).ConnectionState()
	return &st, nil
}

// TestTLS_chainsToPinnedRoots proves the public name serves a certificate for
// itself that verifies against the run's pinned staging roots only, and
// negotiates HTTP/1.1 when that is all the client offers.
func TestTLS_chainsToPinnedRoots(t *testing.T) {
	t.Parallel()
	host, addr := gatewayHost(t)
	cfg := pinnedConfig(t, host)
	cfg.NextProtos = []string{"http/1.1"}
	st, err := handshake(t, cfg, addr)
	if err != nil {
		t.Fatalf("TLS to %s with the pinned roots failed: %v", addr, err)
	}
	leaf := st.PeerCertificates[0]
	if err := leaf.VerifyHostname(host); err != nil {
		t.Fatalf("certificate does not cover %s: %v", host, err)
	}
	if st.NegotiatedProtocol != "" && st.NegotiatedProtocol != "http/1.1" {
		t.Fatalf("negotiated %q when only http/1.1 was offered", st.NegotiatedProtocol)
	}
	if time.Until(leaf.NotAfter) < 24*time.Hour {
		t.Fatalf("certificate expires %s: renewal is not working", leaf.NotAfter)
	}
}

// pinnedConfig is the trust every client of the run uses.
func pinnedConfig(t *testing.T, host string) *tls.Config {
	t.Helper()
	pool, err := gw.LoadCAPool(harness.Fleet(t).State.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{RootCAs: pool, ServerName: host, MinVersion: tls.VersionTLS12}
}

// requireReachable proves the gateway handshakes with the pinned roots, so a
// negative test's refusal is the one it asserts and not a network failure.
func requireReachable(t *testing.T, host, addr string) {
	t.Helper()
	if _, err := handshake(t, pinnedConfig(t, host), addr); err != nil {
		t.Fatalf("the gateway at %s does not handshake even with the pinned roots: %v", addr, err)
	}
}

// TestTLS_untrustedWithoutPinnedRoots proves the pin means what the run says
// it does. A provisioned fleet uses Let's Encrypt staging, which no system trust
// store accepts, so a client trusting only the system roots must refuse the
// certificate as signed by an unknown authority: the pin is load-bearing. A
// target whose pinned roots are themselves public roots (stagenet serves Let's
// Encrypt production, which RootWallet's apps trust through the system store)
// must instead handshake with the system roots alone.
func TestTLS_untrustedWithoutPinnedRoots(t *testing.T) {
	t.Parallel()
	host, addr := gatewayHost(t)
	requireReachable(t, host, addr)
	_, err := handshake(t, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}, addr)
	if !harness.Fleet(t).State.StagingCerts() {
		if err != nil {
			t.Fatalf("this target serves production Let's Encrypt certificates, so the system roots must accept the certificate: %v", err)
		}
		return
	}
	var verr *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &verr) || !errors.As(err, &unknown) {
		t.Fatalf("with the system roots, want x509.UnknownAuthorityError, got %v", err)
	}
}

// alertProtocolVersion is TLS alert 70, protocol_version (RFC 8446 6.2).
const alertProtocolVersion = tls.AlertError(70)

// TestTLS_refusesLegacyVersions: the gateway answers a TLS 1.0/1.1-only
// client with a protocol_version alert.
func TestTLS_refusesLegacyVersions(t *testing.T) {
	t.Parallel()
	host, addr := gatewayHost(t)
	requireReachable(t, host, addr)
	cfg := pinnedConfig(t, host)
	cfg.MinVersion, cfg.MaxVersion = tls.VersionTLS10, tls.VersionTLS11
	_, err := handshake(t, cfg, addr)
	var alert tls.AlertError
	if !(errors.As(err, &alert) && alert == alertProtocolVersion) && (err == nil || !strings.Contains(err.Error(), "protocol version")) {
		t.Fatalf("want a protocol_version alert for TLS 1.0/1.1, got %v", err)
	}
}
