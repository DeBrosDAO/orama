//go:build e2e_fleet

package dnstls

import (
	"context"
	"crypto/tls"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	handshakeBudget = 15 * time.Second
	// stagingIssuerPrefix starts the common name of every Let's Encrypt
	// staging intermediate ("(STAGING) Counterfeit Cashew R10", ...): the run
	// uses staging (e2e/README.md), and a production chain here would mean
	// the run spent real rate limits.
	stagingIssuerPrefix = "(STAGING)"
	// freshCertMinLeft: a certificate issued for this run has most of its
	// lifetime left.
	freshCertMinLeft = 60 * 24 * time.Hour
)

// dialTLS handshakes with ip:443 presenting sni, with the run's pinned roots
// unless edit changes the config.
func dialTLS(t *testing.T, ip, sni string, edit func(*tls.Config)) (*tls.ConnectionState, error) {
	t.Helper()
	pool, err := gw.LoadCAPool(harness.Fleet(t).State.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &tls.Config{RootCAs: pool, ServerName: sni, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	if edit != nil {
		edit(cfg)
	}
	ctx, cancel := context.WithTimeout(t.Context(), handshakeBudget)
	defer cancel()
	conn, err := (&tls.Dialer{Config: cfg}).DialContext(ctx, "tcp", net.JoinHostPort(ip, "443"))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	st := conn.(*tls.Conn).ConnectionState()
	return &st, nil
}

// TestTLS_everyNodeServesStagingCertsForBaseAndWildcard: every node's Caddy
// serves, for the base name and for a name under it, a certificate that
// chains to the pinned staging roots, covers the name (the wildcard one as
// *.<base>), was issued by a staging intermediate and has most of its life
// left (docs/ARCHITECTURE.md "TLS/HTTPS": ACME DNS-01 by the network's own
// DNS; docs/NAMESERVER_SETUP.md "delegation before certificates").
func TestTLS_everyNodeServesStagingCertsForBaseAndWildcard(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	base := f.State.BaseDomain
	for _, n := range f.State.Nodes {
		for _, sni := range []string{base, under(t, base)} {
			st, err := dialTLS(t, n.PublicIP, sni, nil)
			if err != nil {
				t.Errorf("%s: TLS for %s with the pinned roots: %v", n.Name, sni, err)
				continue
			}
			leaf := st.PeerCertificates[0]
			if err := leaf.VerifyHostname(sni); err != nil {
				t.Errorf("%s: certificate does not cover %s: %v", n.Name, sni, err)
			}
			if !strings.HasPrefix(leaf.Issuer.CommonName, stagingIssuerPrefix) {
				t.Errorf("%s: %s issued by %q, want a Let's Encrypt staging intermediate", n.Name, sni, leaf.Issuer.CommonName)
			}
			if left := time.Until(leaf.NotAfter); left < freshCertMinLeft {
				t.Errorf("%s: %s expires in %s, want more than %s for a certificate issued for this run", n.Name, sni, left.Round(time.Hour), freshCertMinLeft)
			}
			if sni != base && !slices.Contains(leaf.DNSNames, "*."+base) {
				t.Errorf("%s: the certificate for %s names %v, want the wildcard *.%s", n.Name, sni, leaf.DNSNames, base)
			}
			if len(leaf.IPAddresses) != 0 {
				t.Errorf("%s: certificate carries IP SANs %v", n.Name, leaf.IPAddresses)
			}
		}
	}
}

// TestTLS_versionsTwelveAndThirteen: TLS 1.2 and 1.3 both handshake
// (docs/ARCHITECTURE.md "internet-facing TLS is 1.2+"; 1.0/1.1 refusal is in
// the smoke feature).
func TestTLS_versionsTwelveAndThirteen(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		for _, v := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
			st, err := dialTLS(t, n.PublicIP, f.State.BaseDomain, func(c *tls.Config) { c.MinVersion, c.MaxVersion = v, v })
			if err != nil || st.Version != v {
				t.Errorf("%s: TLS version %x: %v", n.Name, v, err)
			}
		}
	}
}

// TestTLS_onlyHTTP11ByALPN: offered h2 and http/1.1, Caddy picks http/1.1;
// offered h2 alone it never agrees to h2 (docs/ARCHITECTURE.md "HTTP/1.1
// only": HTTP/2 strips WebSocket upgrade headers, bug #249).
func TestTLS_onlyHTTP11ByALPN(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		st, err := dialTLS(t, n.PublicIP, f.State.BaseDomain, func(c *tls.Config) { c.NextProtos = []string{"h2", "http/1.1"} })
		if err != nil || st.NegotiatedProtocol != "http/1.1" {
			t.Errorf("%s: offered h2 and http/1.1: negotiated %q, %v; want http/1.1", n.Name, protocolOf(st), err)
		}
		st, err = dialTLS(t, n.PublicIP, f.State.BaseDomain, func(c *tls.Config) { c.NextProtos = []string{"h2"} })
		if err == nil && st.NegotiatedProtocol == "h2" {
			t.Errorf("%s: agreed to h2", n.Name)
		}
	}
}

func protocolOf(st *tls.ConnectionState) string {
	if st == nil {
		return ""
	}
	return st.NegotiatedProtocol
}

// foreignDomain is a domain no cluster serves (RFC 2606).
const foreignDomain = "example.org"

// TestTLS_foreignNamesGetNoCertificate: a name the cluster does not serve —
// another domain, a random name under another domain, the base spelled as a
// suffix of a longer label, no name at all — is never answered with a
// certificate that verifies for it (docs/ARCHITECTURE.md "TLS/HTTPS":
// certificates cover the base domain and its subdomains only). A name any
// depth below the base is served (on-demand TLS allows every *.<base>:
// core/pkg/gateway/status_handlers.go tlsCheckHandler), so none is used here.
func TestTLS_foreignNamesGetNoCertificate(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	for _, sni := range []string{"example.com", under(t, foreignDomain), "evil" + f.State.BaseDomain} {
		if _, err := dialTLS(t, n.PublicIP, sni, nil); err == nil {
			t.Errorf("%s: a verified TLS session for %s, which the cluster does not serve", n.Name, sni)
		}
	}
	if _, err := dialTLS(t, n.PublicIP, n.PublicIP, nil); err == nil {
		t.Errorf("%s: a verified TLS session for its bare IP", n.Name)
	}
}
