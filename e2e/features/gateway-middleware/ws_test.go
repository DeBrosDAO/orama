//go:build e2e_fleet

package gatewaymiddleware

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const pubsubWS = "/v1/pubsub/ws"

// TestWebSocket_originChecked: every WebSocket upgrader validates Origin
// against the host the client asked for: no Origin (a non-browser client)
// and the namespace's own origin or a name under it upgrade; a foreign site,
// a lookalike, the parent base domain and "null" are refused with 403 before
// the upgrade, and a forged X-Forwarded-Host does not change the host
// compared against (docs/SECURITY.md "WebSocket Origin Validation"; the
// cluster gateway sets X-Forwarded-Host itself on the proxy hop).
func TestWebSocket_originChecked(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	host := tenancy.NamespaceHost(f, n.Name)
	path := pubsubWS + "?" + url.Values{"topic": {"e2e-origin"}}.Encode()
	allowed := []string{"", "https://" + host, "https://app." + host}
	refused := []string{"https://evil.example", "https://" + host + ".evil.example", "https://evil" + host,
		"https://" + f.State.BaseDomain, "null"}
	for _, origin := range allowed {
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		conn, resp, err := n.Client.DialWS(t.Context(), path, n.Owner.Token(), h)
		if err != nil {
			t.Errorf("origin %q refused (%v): %v", origin, statusOf(resp), err)
			continue
		}
		conn.Close()
	}
	for _, origin := range refused {
		for _, spoof := range []string{"", "evil.example"} {
			h := http.Header{"Origin": {origin}}
			if spoof != "" {
				h.Set("X-Forwarded-Host", spoof)
			}
			conn, resp, err := n.Client.DialWS(t.Context(), path, n.Owner.Token(), h)
			if err == nil {
				conn.Close()
				t.Errorf("origin %q (X-Forwarded-Host %q) upgraded: cross-site WebSocket hijacking", origin, spoof)
				continue
			}
			if statusOf(resp) != http.StatusForbidden {
				t.Errorf("origin %q (X-Forwarded-Host %q): HTTP %d, want 403", origin, spoof, statusOf(resp))
			}
		}
	}
}

// TestWebSocket_noCredentialRefusedBeforeUpgrade: the origin being right is
// not a credential: an anonymous upgrade is refused with a readable 401.
func TestWebSocket_noCredentialRefusedBeforeUpgrade(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	h := http.Header{"Origin": {"https://" + tenancy.NamespaceHost(f, n.Name)}}
	conn, resp, err := n.Client.DialWS(t.Context(), pubsubWS+"?topic=e2e", "", h)
	if err == nil {
		conn.Close()
		t.Fatal("an anonymous pub/sub WebSocket upgraded")
	}
	if statusOf(resp) != http.StatusUnauthorized {
		t.Fatalf("anonymous upgrade: HTTP %d, want 401", statusOf(resp))
	}
}

func statusOf(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}
