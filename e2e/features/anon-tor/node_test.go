//go:build e2e_fleet

package anontor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	torUnit  = "orama-namespace-tor@index.service"
	torrc    = "/etc/orama/tor/torrc"
	socks    = 9050
	checkKey = "anon_proxy"
)

func pollEvery() time.Duration { return 5 * time.Second }

func deadline() time.Time { return time.Now().Add(ioBudget) }

func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

func upgradeReq(path, apiKey string) gw.Req {
	return gw.Req{Path: path, APIKey: apiKey, Header: http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"},
		"Sec-Websocket-Version": {"13"}, "Sec-Websocket-Key": {"dGhlIHNhbXBsZSBub25jZQ=="}}}
}

// TestTor_unitHardening: every node runs the Tor client as a client only,
// SOCKS on loopback 9050 with per-credential isolation, internal addresses
// refused, and the unit denied the private ranges (docs/ARCHITECTURE.md,
// systemd/orama-namespace-tor@.service).
func TestTor_unitHardening(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	want := []string{"SocksPort 127.0.0.1:9050 IsolateSOCKSAuth", "ClientOnly 1", "ORPort 0", "ClientRejectInternalAddresses 1"}
	for _, n := range f.State.Nodes {
		if s := f.Unit(t, n, torUnit); s != "active" {
			t.Errorf("%s: %s is %q", n.Name, torUnit, s)
		}
		conf := string(f.ReadFile(t, n, torrc))
		for _, line := range want {
			if !strings.Contains(conf, line) {
				t.Errorf("%s: torrc lacks %q", n.Name, line)
			}
		}
		props := f.MustExec(t, n, "systemctl show "+torUnit+" -p IPAddressDeny -p User -p NoNewPrivileges").Stdout
		for _, p := range []string{"10.0.0.0/8", "169.254.0.0/16", "100.64.0.0/10", "User=debian-tor", "NoNewPrivileges=yes"} {
			if !strings.Contains(props, p) {
				t.Errorf("%s: the tor unit lacks %q:\n%s", n.Name, p, props)
			}
		}
		for _, l := range f.Listeners(t, n) {
			if l.Port == socks && l.Addr != "127.0.0.1" {
				t.Errorf("%s: SOCKS listens on %s", n.Name, l.Addr)
			}
		}
		if s := f.Unit(t, n, "tor.service"); s == "active" {
			t.Errorf("%s: the distribution's tor.service is active", n.Name)
		}
	}
}

// TestHealth_anonProxyOk: /v1/health on every node reports checks.anon_proxy
// ok while Tor runs (the unavailable case is anon-tor-chaos).
func TestHealth_anonProxyOk(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		r := harness.GW(t).PinTo(n.PublicIP).MustSend(t, gw.Req{Path: "/v1/health"}).Expect(t, http.StatusOK)
		if got := anonCheck(t, r.Body); got != "ok" {
			t.Errorf("%s: checks.anon_proxy = %q, want ok", n.Name, got)
		}
	}
}

// anonCheck reads checks.anon_proxy, a status string or {status}.
func anonCheck(t testing.TB, raw []byte) string {
	t.Helper()
	var h struct {
		Checks map[string]json.RawMessage `json:"checks"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("/v1/health: %v", err)
	}
	c := h.Checks[checkKey]
	var s string
	if json.Unmarshal(c, &s) == nil {
		return s
	}
	var obj struct{ Status string }
	if err := json.Unmarshal(c, &obj); err != nil {
		t.Fatalf("checks.%s is %s", checkKey, c)
	}
	return obj.Status
}
