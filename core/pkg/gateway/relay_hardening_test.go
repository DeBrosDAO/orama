package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Bugboard #266 review: a relay holds a bounded number of streams per address,
// and dials exactly the hostname it checked.

func TestRelayService_perAddressStreamsAreBoundedAndReleasable(t *testing.T) {
	s := newRelayService(nil)
	var releases []func()
	for range relayMaxStreamsPerAddress {
		release, ok := s.acquireAddress("a")
		if !ok {
			t.Fatal("refused below the bound")
		}
		releases = append(releases, release)
	}
	if _, ok := s.acquireAddress("a"); ok {
		t.Error("accepted past the per-address bound")
	}
	if _, ok := s.acquireAddress("b"); !ok {
		t.Error("another address was limited by the first")
	}
	releases[0]()
	if _, ok := s.acquireAddress("a"); !ok {
		t.Error("a released stream was not reusable")
	}
	for _, r := range releases[1:] {
		r()
	}
	if s.perAddr["a"] > 1 {
		t.Errorf("entries leaked: %v", s.perAddr)
	}
}

func TestRelayHandler_anAddressAtItsStreamCapIs429AndHoldsNoPoolSlot(t *testing.T) {
	g := relayTestGateway(t, true, func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("x") })
	r := relayWSRequest("host=ns-a." + relayBase)
	r.RemoteAddr = "198.51.100.7:1"
	for range relayMaxStreamsPerAddress {
		if _, ok := g.relay.acquireAddress(bucketKeyOf(r)); !ok {
			t.Fatal("setup")
		}
	}
	rec := httptest.NewRecorder()
	g.relayTunnelHandler(rec, r)
	wantRelayRefusal(t, rec, http.StatusTooManyRequests, "RATE_LIMITED")
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
	if len(g.relay.slots) != 0 {
		t.Errorf("a refused stream kept %d pool slots", len(g.relay.slots))
	}

	other := relayWSRequest("host=ns-a." + relayBase)
	other.RemoteAddr = "203.0.113.9:1"
	rec = httptest.NewRecorder()
	g.relayTunnelHandler(rec, other)
	wantRelayRefusal(t, rec, http.StatusServiceUnavailable, CodeRelayUnavailable) // got past the caps, the dial failed
}

func TestRelayHandler_everyExitReleasesTheAddressAndThePool(t *testing.T) {
	g := relayTestGateway(t, true, func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("down") })
	for range 3 * relayMaxStreamsPerAddress {
		r := relayWSRequest("host=ns-a." + relayBase)
		r.RemoteAddr = "198.51.100.7:1"
		rec := httptest.NewRecorder()
		g.relayTunnelHandler(rec, r)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status %d: a failed dial leaked a stream", rec.Code)
		}
	}
	if len(g.relay.slots) != 0 || len(g.relay.perAddr) != 0 {
		t.Errorf("leaked: %d slots, %v", len(g.relay.slots), g.relay.perAddr)
	}
}

func TestRelayTarget_onlyLDHAsciiAndItDialsTheCheckedString(t *testing.T) {
	s := newRelayService([]string{relayBase})
	for host, want := range map[string]bool{
		"ns-a." + relayBase:                       true,
		"NS-A." + relayBase + ".":                 true,
		"ns-a." + relayBase + "..":                false, // one trailing dot only
		"ns-a.K." + relayBase:                     false, // KELVIN SIGN lowercases to "k"
		"ns-K." + relayBase:                       false,
		"ns-a.İ." + relayBase:                     false, // dotted capital I
		"ns-a.ı." + relayBase:                     false,
		"ns-a.\x00." + relayBase:                  false,
		"ns-a." + relayBase + "\x00.evil":         false,
		"ns-a." + relayBase + ":443":              false,
		"ns-a_b." + relayBase:                     false,
		"ns-a b." + relayBase:                     false,
		"ns-a\t." + relayBase:                     false,
		"ns-a\x7f." + relayBase:                   false,
		"ns-a​." + relayBase:                      false, // zero width space
		"-ns." + relayBase:                        false,
		"ns-." + relayBase:                        false,
		"ns..a." + relayBase:                      false,
		"." + relayBase:                           false,
		strings.Repeat("a", 64) + "." + relayBase: false,
		strings.Repeat("a.", 130) + relayBase:     false,
		"é." + relayBase:                          false,
		"xn--9ca." + relayBase:                    true, // punycode is plain LDH
	} {
		got, ok := s.target(host, "443")
		if ok != want {
			t.Errorf("host %q: allowed = %v, want %v", host, ok, want)
		}
		if ok && got.host != strings.ToLower(strings.TrimSuffix(host, ".")) {
			t.Errorf("host %q: dials %q, not the checked name", host, got.host)
		}
	}
}

func TestRelayHandler_dialsTheNormalizedHost(t *testing.T) {
	var dialled string
	g := relayTestGateway(t, true, func(_ context.Context, addr, _ string) (net.Conn, error) {
		dialled = addr
		return nil, errors.New("stop")
	})
	g.relayTunnelHandler(httptest.NewRecorder(), relayWSRequest("host=NS-A."+strings.ToUpper(relayBase)+".&port=443"))
	if dialled != "ns-a."+relayBase+":443" {
		t.Errorf("dialled %q", dialled)
	}
}

func TestRelayHandler_nonASCIIHostIs400AndNothingIsDialled(t *testing.T) {
	var dialled int
	g := relayTestGateway(t, true, func(context.Context, string, string) (net.Conn, error) {
		dialled++
		return nil, errors.New("must not be called")
	})
	for _, host := range []string{"ns-a.%E2%84%AA." + relayBase, "ns-a.%C4%B0." + relayBase, "ns-a%00." + relayBase, "ns-a%3A443." + relayBase, "ns-a%0d%0a." + relayBase} {
		rec := httptest.NewRecorder()
		g.relayTunnelHandler(rec, relayWSRequest("host="+host+"&port=443"))
		wantRelayRefusal(t, rec, http.StatusBadRequest, CodeRelayDestinationNotAllowed)
	}
	if dialled != 0 {
		t.Errorf("dialled %d times", dialled)
	}
}
