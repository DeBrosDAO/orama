// Package invitemint mints, from the operator's machine, an invite that names
// one node of a cluster. `orama invite` and `orama node setup` both use it.
//
// The environment's gateway URL is the cluster's base domain, which DNS
// spreads across every nameserver, each serving a certificate of its own. An
// invite used to carry that URL and the fingerprint of whichever node answered
// the fingerprinting handshake — or no fingerprint at all — and the joining
// node, resolving the same name later, reached another node and failed its pin
// or trusted whatever it was shown. So the invite names the node it was minted
// through: its public address as the URL, the base domain as the name to
// present, and the certificate that node served on the minting connection.
// The same shape `orama node invite` produces on the node itself.
package invitemint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/shared"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/invite"
)

// dialTimeout bounds the connection to the chosen node.
const dialTimeout = shared.RequestTimeout

// Minted is an encoded invite naming one node, and what it names.
type Minted struct {
	// Invite is the encoded invite a new node installs with.
	Invite string
	// JoinURL is the chosen node's public address.
	JoinURL string
	// SNI is the domain the joining node presents to it.
	SNI string
	// NodeIP is the node the token was minted through.
	NodeIP string
	// ExpiresAt is when the gateway stops accepting the token.
	ExpiresAt string
	// Fingerprint is the SHA-256 of the certificate the node served.
	Fingerprint string
}

// lookupIPv4 resolves host to its IPv4 addresses.
type lookupIPv4 func(ctx context.Context, host string) ([]net.IP, error)

func resolveIPv4(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip4", host)
}

// GatewayHost is the DNS name in gatewayURL: the name every node serves its
// certificate under, and so the name a joining node presents.
func GatewayHost(gatewayURL string) (string, error) {
	u, err := url.Parse(gatewayURL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("gateway URL %q has no host", gatewayURL)
	}
	if net.ParseIP(u.Hostname()) != nil {
		return "", fmt.Errorf("gateway URL %q is an address; an invite needs the cluster's domain, "+
			"which its nodes serve their certificates under (`orama env add <name> https://<base-domain>`)", gatewayURL)
	}
	return u.Hostname(), nil
}

// ChooseNode picks the node the invite names: requested when it is set, else
// the lowest public IPv4 address host resolves to. The choice is
// deterministic, so a failure names a node the operator can steer past.
func ChooseNode(ctx context.Context, requested, host string) (string, error) {
	return chooseNode(ctx, requested, host, resolveIPv4)
}

func chooseNode(ctx context.Context, requested, host string, lookup lookupIPv4) (string, error) {
	if requested != "" {
		if err := install.ValidatePublicIP(requested); err != nil {
			return "", fmt.Errorf("the requested node: %w", err)
		}
		// Canonical dotted quad: ::ffff:a.b.c.d validates but is no address
		// a joining node could be sent to.
		return net.ParseIP(requested).To4().String(), nil
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return "", fmt.Errorf("resolve %s to choose the node the invite names: %w", host, err)
	}
	var candidates []string
	for _, ip := range ips {
		if install.ValidatePublicIP(ip.String()) == nil {
			candidates = append(candidates, ip.To4().String())
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("%s resolves to no public IPv4 address (%v)", host, ips)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return bytes.Compare(net.ParseIP(candidates[i]).To4(), net.ParseIP(candidates[j]).To4()) < 0
	})
	return candidates[0], nil
}

// MintThrough mints an invite through nodeIP — a node of the cluster
// gatewayURL names, host being its domain — and encodes it to name that node
// and pin the certificate it answered with. A failure is returned rather than
// an invite with no pin: one that silently drops to trust-on-first-use is
// worse than one the operator has to retry.
func MintThrough(gatewayURL, host, nodeIP, bearer string, expiry time.Duration) (Minted, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return Minted{}, fmt.Errorf("http.DefaultTransport is a %T, not an *http.Transport", http.DefaultTransport)
	}
	return mintThrough(nodeClient(base, nodeIP), gatewayURL, host, nodeIP, bearer, expiry)
}

func mintThrough(client *http.Client, gatewayURL, host, nodeIP, bearer string, expiry time.Duration) (Minted, error) {
	joinURL, err := joinURLFor(gatewayURL, nodeIP)
	if err != nil {
		return Minted{}, err
	}
	m, err := mintAt(client, gatewayURL, bearer, int(expiry.Minutes()))
	if err != nil {
		return Minted{}, fmt.Errorf("mint an invite through %s (%s): %w", nodeIP, host, err)
	}
	encoded, err := invite.Encode(invite.Invite{
		JoinURL:       joinURL,
		Token:         m.token,
		CAFingerprint: m.fingerprint,
		SNI:           host,
	})
	if err != nil {
		return Minted{}, fmt.Errorf("encode the invite: %w", err)
	}
	return Minted{
		Invite:      encoded,
		JoinURL:     joinURL,
		SNI:         host,
		NodeIP:      nodeIP,
		ExpiresAt:   m.expiresAt,
		Fingerprint: m.fingerprint,
	}, nil
}

// nodeClient reaches gateway URLs through nodeIP, whatever their host
// resolves to. TLS still verifies the certificate against the URL's host.
//
// No proxy: a proxied request would CONNECT to the proxy, not to nodeIP, and
// the dial override would send that CONNECT — and any proxy credentials — to
// nodeIP in plaintext. No redirects: the pin must be the certificate of the
// connection that minted the token, not of wherever a redirect led.
func nodeClient(base *http.Transport, nodeIP string) *http.Client {
	t := base.Clone()
	t.Proxy = nil
	dialer := &net.Dialer{Timeout: dialTimeout}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("split %q: %w", addr, err)
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(nodeIP, port))
	}
	return &http.Client{
		Transport: t,
		Timeout:   shared.RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// joinURLFor is the join URL naming nodeIP, on the gateway URL's port when it
// has one: the joining node reaches the node where the operator did.
func joinURLFor(gatewayURL, nodeIP string) (string, error) {
	u, err := url.Parse(gatewayURL)
	if err != nil {
		return "", fmt.Errorf("parse gateway URL %q: %w", gatewayURL, err)
	}
	if port := u.Port(); port != "" && port != "443" {
		return "https://" + net.JoinHostPort(nodeIP, port), nil
	}
	return "https://" + nodeIP, nil
}

// minted is an invite token and the certificate of the node that issued it.
type minted struct {
	token       string
	expiresAt   string
	fingerprint string
}

// mintAt asks the node client reaches for an invite token, and fingerprints
// the certificate it answered with — so the pin is of the node that minted
// the token, on the very connection that did.
func mintAt(client *http.Client, gatewayURL, bearer string, expiryMinutes int) (minted, error) {
	raw, state, err := shared.RequestWith(client, gatewayURL, bearer, http.MethodPost, "/v1/operator/invite",
		map[string]int{"expiry_minutes": expiryMinutes})
	if err != nil {
		return minted{}, err
	}
	var resp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return minted{}, fmt.Errorf("could not parse the gateway's reply: %w", err)
	}
	if resp.Token == "" {
		return minted{}, fmt.Errorf("the gateway returned no token")
	}
	if state == nil || len(state.PeerCertificates) == 0 {
		return minted{}, fmt.Errorf("the gateway answered without a TLS certificate to pin")
	}
	sum := sha256.Sum256(state.PeerCertificates[0].Raw)
	return minted{token: resp.Token, expiresAt: resp.ExpiresAt, fingerprint: hex.EncodeToString(sum[:])}, nil
}
