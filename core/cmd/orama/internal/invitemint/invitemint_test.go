package invitemint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/invite"
)

func TestGatewayHost(t *testing.T) {
	if got, err := GatewayHost("https://stagenet.example.test"); err != nil || got != "stagenet.example.test" {
		t.Errorf("gatewayHost = %q, %v", got, err)
	}
	for _, bad := range []string{"https://203.0.113.7", "https://", "::"} {
		if _, err := GatewayHost(bad); err == nil {
			t.Errorf("GatewayHost(%q) accepted a URL with no domain to present", bad)
		}
	}
}

func fixedLookup(ips ...string) lookupIPv4 {
	return func(context.Context, string) ([]net.IP, error) {
		var out []net.IP
		for _, s := range ips {
			out = append(out, net.ParseIP(s))
		}
		return out, nil
	}
}

// Without a requested node the invite names the lowest public address the domain
// resolves to — a deterministic choice, so a failure can be steered past.
func TestChooseNode_lowestPublicAddress(t *testing.T) {
	got, err := chooseNode(context.Background(), "", "h", fixedLookup("203.0.113.20", "10.0.0.1", "203.0.113.3", "198.51.100.200"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "198.51.100.200" {
		t.Errorf("chooseNode = %s, want the lowest public address 198.51.100.200", got)
	}
}

// ::ffff:a.b.c.d validates as a public IPv4 but must be named as the dotted
// quad, requested and resolved alike.
func TestChooseNode_canonicalisesMappedIPv4(t *testing.T) {
	got, err := chooseNode(context.Background(), "::ffff:203.0.113.9", "h", fixedLookup())
	if err != nil || got != "203.0.113.9" {
		t.Errorf("requested mapped: %q, %v", got, err)
	}
	got, err = chooseNode(context.Background(), "", "h", fixedLookup("::ffff:203.0.113.4"))
	if err != nil || got != "203.0.113.4" {
		t.Errorf("resolved mapped: %q, %v", got, err)
	}
}

func TestJoinURLFor_keepsANonDefaultPort(t *testing.T) {
	for gw, want := range map[string]string{
		"https://stagenet.example.test":      "https://203.0.113.9",
		"https://stagenet.example.test:443":  "https://203.0.113.9",
		"https://stagenet.example.test:8443": "https://203.0.113.9:8443",
	} {
		if got, err := joinURLFor(gw, "203.0.113.9"); err != nil || got != want {
			t.Errorf("joinURLFor(%q) = %q, %v; want %q", gw, got, err, want)
		}
	}
}

func TestChooseNode_requestedNodeWins(t *testing.T) {
	got, err := chooseNode(context.Background(), "203.0.113.9", "h", fixedLookup("203.0.113.1"))
	if err != nil || got != "203.0.113.9" {
		t.Errorf("chooseNode = %q, %v; want the node the operator named", got, err)
	}
}

func TestChooseNode_refusals(t *testing.T) {
	ctx := context.Background()
	if _, err := chooseNode(ctx, "10.0.0.4", "h", fixedLookup()); err == nil {
		t.Error("a private requested node was accepted")
	}
	if _, err := chooseNode(ctx, "", "h", fixedLookup("10.0.0.1", "127.0.0.1")); err == nil {
		t.Error("a domain resolving only to private addresses produced a node")
	}
	failing := func(context.Context, string) ([]net.IP, error) { return nil, errors.New("no such host") }
	if _, err := chooseNode(ctx, "", "h", failing); err == nil || !strings.Contains(err.Error(), "resolve h") {
		t.Errorf("a resolution failure did not name the domain it resolved: %v", err)
	}
}

// The token is minted through the chosen node, and the fingerprint is the
// certificate that node served on that connection — not whatever the domain
// resolves to. The server here is only reachable through the address the
// client is told to dial.
func TestMintAt_mintsThroughTheNodeAndPinsItsCertificate(t *testing.T) {
	var gotHost, gotAuth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotAuth = r.Host, r.Header.Get("Authorization")
		if r.URL.Path != "/v1/operator/invite" || r.Method != http.MethodPost {
			http.Error(w, "wrong endpoint", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"token":"` + strings.Repeat("ab", 32) + `","expires_at":"2026-09-26 13:00:00"}`))
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	// example.com is in the test certificate; it does not resolve to the test
	// server, so reaching it proves the client dialled the node address.
	gatewayURL := "https://example.com:" + u.Port()
	client := nodeClient(srv.Client().Transport.(*http.Transport), "127.0.0.1")

	m, err := mintAt(client, gatewayURL, "bearer-x", 30)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(srv.Certificate().Raw)
	if m.fingerprint != hex.EncodeToString(sum[:]) {
		t.Errorf("fingerprint %s is not the answering node's certificate", m.fingerprint)
	}
	if m.token != strings.Repeat("ab", 32) || m.expiresAt == "" {
		t.Errorf("minted = %+v", m)
	}
	if !strings.HasPrefix(gotHost, "example.com") || gotAuth != "Bearer bearer-x" {
		t.Errorf("request carried Host %q and Authorization %q", gotHost, gotAuth)
	}
}

func TestMintAt_failures(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"refused":  func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "operator only", http.StatusForbidden) },
		"no token": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"expires_at":"x"}`)) },
		"garbage":  func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`not json`)) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewTLSServer(handler)
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			client := nodeClient(srv.Client().Transport.(*http.Transport), "127.0.0.1")
			if _, err := mintAt(client, "https://example.com:"+u.Port(), "b", 30); err == nil {
				t.Error("a failed mint produced an invite")
			}
		})
	}
}

// A certificate that does not verify for the domain is refused: the operator's
// machine is where the pin's trust comes from.
func TestMintAt_certificateMustVerifyForTheDomain(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token":"` + strings.Repeat("ab", 32) + `"}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	client := nodeClient(srv.Client().Transport.(*http.Transport), "127.0.0.1")
	if _, err := mintAt(client, "https://stagenet.not-in-the-cert.test:"+u.Port(), "b", 30); err == nil {
		t.Fatal("a certificate not valid for the domain was pinned")
	}
}

// A redirect is not followed: the pin has to be the certificate of the
// connection that answered the mint. The redirect points back at the same
// server, so following it would succeed — and must not happen.
func TestMintAt_doesNotFollowRedirects(t *testing.T) {
	hits := 0
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/followed" {
			_, _ = w.Write([]byte(`{"token":"` + strings.Repeat("ab", 32) + `"}`))
			return
		}
		http.Redirect(w, r, "/followed", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	client := nodeClient(srv.Client().Transport.(*http.Transport), "127.0.0.1")
	_, err := mintAt(client, "https://example.com:"+u.Port(), "b", 30)
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("a redirected mint was followed or misreported: %v", err)
	}
	if hits != 1 {
		t.Errorf("server hit %d times, want 1", hits)
	}
}

// A proxy configured in the environment is never used: the dial override
// would otherwise send the CONNECT (and proxy credentials) to the node.
func TestNodeClient_ignoresProxies(t *testing.T) {
	base := &http.Transport{Proxy: http.ProxyFromEnvironment}
	client := nodeClient(base, "127.0.0.1")
	if client.Transport.(*http.Transport).Proxy != nil {
		t.Error("the node client uses a proxy")
	}
	if client.CheckRedirect == nil {
		t.Error("the node client follows redirects")
	}
}

// The encoded invite names the node the token was minted through, the domain
// to present to it, and the certificate it served — what a joining node needs
// to reach exactly that node and pin exactly that certificate.
func TestMintThrough_encodesAnInviteNamingTheNode(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token":"` + strings.Repeat("cd", 32) + `","expires_at":"2026-09-26 13:00:00"}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	gatewayURL := "https://example.com:" + u.Port()
	client := nodeClient(srv.Client().Transport.(*http.Transport), "127.0.0.1")

	m, err := mintThrough(client, gatewayURL, "example.com", "203.0.113.9", "b", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := invite.Decode(m.Invite)
	if err != nil {
		t.Fatalf("the minted invite does not decode: %v", err)
	}
	sum := sha256.Sum256(srv.Certificate().Raw)
	if inv.CAFingerprint != hex.EncodeToString(sum[:]) || inv.CAFingerprint != m.Fingerprint {
		t.Errorf("invite pins %q, want the minting node's certificate", inv.CAFingerprint)
	}
	if inv.JoinURL != "https://203.0.113.9:"+u.Port() || inv.SNI != "example.com" {
		t.Errorf("invite names %s presenting %q, want the chosen node presenting the domain", inv.JoinURL, inv.SNI)
	}
	if inv.Token != strings.Repeat("cd", 32) {
		t.Errorf("invite carries token %q", inv.Token)
	}
}

func TestMintThrough_aFailedMintProducesNoInvite(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "operator only", http.StatusForbidden)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	client := nodeClient(srv.Client().Transport.(*http.Transport), "127.0.0.1")
	m, err := mintThrough(client, "https://example.com:"+u.Port(), "example.com", "203.0.113.9", "b", time.Minute)
	if err == nil || m.Invite != "" {
		t.Fatalf("a refused mint returned %+v, %v", m, err)
	}
	if !strings.Contains(err.Error(), "203.0.113.9") {
		t.Errorf("the error does not name the node: %v", err)
	}
}
