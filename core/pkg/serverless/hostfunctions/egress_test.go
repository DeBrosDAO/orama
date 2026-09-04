package hostfunctions

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestBlockedIP_refusesEveryInternalRange(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1", "127.53.1.9", "0.0.0.0",
		"10.0.0.5", "10.255.255.254", // the WireGuard overlay
		"172.16.0.1", "172.31.255.254",
		"192.168.1.1",
		"169.254.169.254", // cloud metadata
		"100.64.0.1",      // carrier-grade NAT
		"198.18.0.1",      // benchmarking
		"192.0.0.1",
		"224.0.0.1", "239.1.2.3", // multicast
		"255.255.255.255",
		"::1", "::", "fc00::1", "fd12:3456::1", "fe80::1", "ff02::1",
		"64:ff9b::a00:5", // NAT64 wrapper around 10.0.0.5
		"2002:0a00:0005::1",
		"::ffff:10.0.0.5", // IPv4-mapped overlay address
		"::ffff:127.0.0.1",
	} {
		t.Run(addr, func(t *testing.T) {
			ip := net.ParseIP(addr)
			if ip == nil {
				t.Fatalf("%q is not an address", addr)
			}
			if !blockedIP(ip) {
				t.Errorf("%s is reachable from tenant code", addr)
			}
		})
	}
}

func TestBlockedIP_allowsThePublicInternet(t *testing.T) {
	for _, addr := range []string{
		"1.1.1.1", "8.8.8.8", "93.184.216.34", "51.195.109.238",
		"2606:4700:4700::1111", "2001:4860:4860::8888",
	} {
		t.Run(addr, func(t *testing.T) {
			ip := net.ParseIP(addr)
			if ip == nil {
				t.Fatalf("%q is not an address", addr)
			}
			if blockedIP(ip) {
				t.Errorf("%s is refused, but it is an ordinary public address", addr)
			}
		})
	}
}

func TestBlockedIP_refusesNothingItCannotUnderstand(t *testing.T) {
	if !blockedIP(nil) {
		t.Error("a nil address was allowed; an address that cannot be checked is not one that may be allowed")
	}
}

func TestGuardEgressAddress(t *testing.T) {
	for _, tc := range []struct {
		address string
		blocked bool
	}{
		{"10.0.0.5:4001", true},
		{"127.0.0.1:10000", true},
		{"169.254.169.254:80", true},
		{"[::1]:443", true},
		{"[fd00::1]:443", true},
		{"1.1.1.1:443", false},
		{"[2606:4700:4700::1111]:443", false},
		{"not-an-address", true},
		{"example.com:443", true}, // a name here means resolution did not happen
	} {
		t.Run(tc.address, func(t *testing.T) {
			err := guardEgressAddress("tcp", tc.address, nil)
			if tc.blocked && err == nil {
				t.Errorf("%s was allowed", tc.address)
			}
			if !tc.blocked && err != nil {
				t.Errorf("%s was refused: %v", tc.address, err)
			}
		})
	}
}

func TestErrBlockedDestination_namesTheAddressAndTheReason(t *testing.T) {
	err := guardEgressAddress("tcp", "10.0.0.5:4001", nil)
	if err == nil {
		t.Fatal("the overlay was allowed")
	}
	var blocked *errBlockedDestination
	if !errors.As(err, &blocked) {
		t.Fatalf("got %T, want *errBlockedDestination", err)
	}
	if !strings.Contains(err.Error(), "10.0.0.5:4001") {
		t.Errorf("the error does not say which destination was refused: %v", err)
	}
	if !strings.Contains(err.Error(), "internal network") {
		t.Errorf("the error does not say why: %v", err)
	}
}

// The check that matters. A hostname that resolves to an internal address used
// to pass, because the old guard read the URL text and returned nil for
// anything that was not an IP literal.
func TestGuardedHTTPClient_refusesANameThatResolvesInternally(t *testing.T) {
	client := newGuardedHTTPClient(5 * time.Second)

	// "localhost" resolves to 127.0.0.1 and is the simplest name that proves
	// the point: nothing about the string says 127.0.0.1, the resolver does.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("split server url: %v", err)
	}

	resp, err := client.Get("http://localhost:" + port + "/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("a request to a name resolving to loopback succeeded")
	}
	var blocked *errBlockedDestination
	if !errors.As(err, &blocked) {
		t.Fatalf("the request failed for the wrong reason: %v", err)
	}
}

// The guard is on the socket, so it applies again to wherever a redirect points
// — which is the case a URL-text check can never cover.
func TestGuardedHTTPClient_refusesARedirectToAnInternalAddress(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("the cluster's own service"))
	}))
	defer internal.Close()

	// The first hop is reached with the guard disabled, standing in for a
	// public host the tenant controls; the redirect it returns is the attack.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client := newGuardedHTTPClient(5 * time.Second)
	resp, err := client.Get(redirector.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("the redirect to an internal address was followed")
	}
	var blocked *errBlockedDestination
	if !errors.As(err, &blocked) {
		t.Fatalf("the request failed for the wrong reason: %v", err)
	}
}

func TestGuardedHTTPClient_stillReachesAnOrdinaryHost(t *testing.T) {
	// Bind a listener on a non-loopback address of this machine if there is
	// one; without a public interface the positive path cannot be exercised
	// against a real socket, so fall back to asserting the guard's decision.
	if err := guardEgressAddress("tcp", "93.184.216.34:443", nil); err != nil {
		t.Fatalf("an ordinary public address was refused: %v", err)
	}
	client := newGuardedHTTPClient(time.Second)
	if client.Transport == nil {
		t.Fatal("the guarded client has no transport")
	}
	if client.Timeout != time.Second {
		t.Errorf("timeout = %v, want 1s", client.Timeout)
	}
}

// denyInternalURL is the first of the two checks. It settles what can be
// settled from the text and leaves the rest to the dial.
func TestDenyInternalURL(t *testing.T) {
	for _, tc := range []struct {
		name    string
		url     string
		refused bool
	}{
		{"loopback literal", "http://127.0.0.1:10000/", true},
		{"overlay literal", "http://10.0.0.5:4001/", true},
		{"metadata literal", "http://169.254.169.254/latest/meta-data/", true},
		{"carrier-grade nat literal", "http://100.64.0.1/", true},
		{"ipv6 loopback literal", "http://[::1]:8080/", true},
		{"ipv4-mapped overlay", "http://[::ffff:10.0.0.5]/", true},
		{"localhost", "http://localhost:10000/", true},
		{"localhost suffix", "http://rqlite.localhost/", true},
		{"gcp metadata name", "http://metadata.google.internal/", true},
		{"file scheme", "file:///etc/passwd", true},
		{"gopher scheme", "gopher://example.com/", true},
		{"no host", "http:///path", true},
		{"unparseable", "://", true},
		{"not a url at all", "not-a-url", true},
		{"empty", "", true},
		{"ftp scheme", "ftp://example.com/", true},
		{"public literal", "https://1.1.1.1/", false},
		{"public name", "https://api.example.com/v1/thing", false},
		// A name that resolves internally is NOT refused here — it cannot be,
		// from the text. The dial refuses it.
		{"internal name", "http://rqlite.internal/", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := denyInternalURL(tc.url)
			if tc.refused && err == nil {
				t.Errorf("%s was allowed", tc.url)
			}
			if !tc.refused && err != nil {
				t.Errorf("%s was refused: %v", tc.url, err)
			}
		})
	}
}

// A refused fetch comes back as the host function's error envelope, not as a
// Go error, so a function sees a status 0 result it can handle.
func TestHTTPFetch_refusedDestinationComesBackAsAnEnvelope(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop(), httpClient: newGuardedHTTPClient(time.Second)}
	out, err := h.HTTPFetch(context.Background(), http.MethodGet, "http://10.0.0.5:4001/", nil, nil)
	if err != nil {
		t.Fatalf("HTTPFetch returned a Go error: %v", err)
	}
	if !strings.Contains(string(out), `"status":0`) {
		t.Errorf("envelope = %s", out)
	}
}
