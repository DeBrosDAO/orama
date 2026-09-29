// Package chainonion submits Orama chain transactions to a validator's onion
// service through a Tor SOCKS proxy.
//
// The contract is that an onion submission never touches the clearnet. The
// HTTP client built here has no other route than the SOCKS proxy: no direct
// dialer, no environment proxy, no redirects. When the proxy or the onion
// service cannot be reached, the error says so and the transaction is not sent
// anywhere else, because sending it in the clear would tie it to the
// submitter's address.
//
// Each client carries one random SOCKS isolation credential. Tor gives streams
// with different credentials different circuits, so a client per transaction
// means a fresh circuit per transaction, and two transactions cannot be linked
// by their circuit.
package chainonion

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/anonproxy"
)

const (
	// DefaultSOCKS is the SOCKS5 address of a Tor daemon on this machine.
	DefaultSOCKS = "127.0.0.1:9050"

	// requestTimeout covers building a circuit to an onion service and one
	// request over it, which takes far longer than a clearnet request.
	requestTimeout = 90 * time.Second

	onionSuffix      = ".onion"
	isolationKeySize = 16
	// v3 onion addresses are 56 base32 characters before the suffix.
	onionLabelLen = 56
	defaultPort   = "80"
)

// ErrNotOnion is an address that is not a v3 onion service.
var ErrNotOnion = errors.New("not a v3 .onion address")

// ErrUnreachable is a failure to reach the onion service through Tor. The
// transaction was not sent, and no other route was tried.
var ErrUnreachable = errors.New("the onion service could not be reached through Tor; " +
	"the transaction was not sent, and nothing was tried outside Tor")

// Base validates onion (an address with optional :port, no scheme or path) and
// returns the base URL requests go to. Onion services are end-to-end
// encrypted by Tor itself, so the scheme is http.
func Base(onion string) (string, error) {
	host, port := onion, defaultPort
	if h, p, err := net.SplitHostPort(onion); err == nil {
		host, port = h, p
	} else if strings.Contains(onion, ":") {
		return "", fmt.Errorf("%w: %q", ErrNotOnion, onion)
	}
	label, ok := strings.CutSuffix(strings.ToLower(host), onionSuffix)
	if !ok || len(label) != onionLabelLen || !isBase32(label) || !validPort(port) {
		return "", fmt.Errorf("%w: %q", ErrNotOnion, onion)
	}
	return "http://" + net.JoinHostPort(strings.ToLower(host), port), nil
}

// isBase32 reports whether s is written in the lowercase RFC 4648 base32 alphabet an onion
// address uses: a-z and 2-7.
func isBase32(s string) bool {
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return false
		}
	}
	return true
}

// validPort reports whether s is a decimal port number in 1..65535, written without a sign,
// a leading zero or any other character.
func validPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == s
}

// ValidateSOCKS checks a Tor SOCKS5 address: a loopback host and a numeric port. The proxy carries
// the transaction and the isolation credential in the clear to whatever answers there, so it must
// be a process on this machine, never a name or another host.
func ValidateSOCKS(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("tor SOCKS address %q must be host:port: %w", addr, err)
	}
	if !validPort(port) {
		return fmt.Errorf("tor SOCKS address %q: %q is not a numeric port", addr, port)
	}
	if host != "localhost" {
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.IsLoopback() {
			return fmt.Errorf("tor SOCKS address %q: %q is not a loopback address (127.0.0.0/8, ::1 or localhost)", addr, host)
		}
	}
	return nil
}

// NewClient returns an HTTP client whose every connection goes through the
// SOCKS5 proxy at socksAddr under one new isolation credential. Use one client
// for one transaction. An empty socksAddr is DefaultSOCKS; any other must pass
// ValidateSOCKS.
func NewClient(socksAddr string) (*http.Client, error) {
	if socksAddr == "" {
		socksAddr = DefaultSOCKS
	}
	if err := ValidateSOCKS(socksAddr); err != nil {
		return nil, err
	}
	raw := make([]byte, isolationKeySize)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate the circuit isolation key: %w", err)
	}
	return newClient(socksAddr, hex.EncodeToString(raw), anonproxy.DialVia), nil
}

type dialVia func(ctx context.Context, socksAddr, addr, isolationKey string) (net.Conn, error)

func newClient(socksAddr, isolationKey string, dial dialVia) *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			// Proxy stays nil: an http_proxy in the environment must not
			// replace the Tor route, and there is no Dial or DialTLS to fall
			// back to.
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				conn, err := dial(ctx, socksAddr, addr, isolationKey)
				if err != nil {
					return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
				}
				return conn, nil
			},
		},
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			return fmt.Errorf("refusing a redirect to %s: an onion submission stays on the onion service", req.URL.Host)
		},
	}
}
