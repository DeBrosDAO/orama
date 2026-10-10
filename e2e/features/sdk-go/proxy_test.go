//go:build e2e_fleet

package sdkgo

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// Anonymity proxy (website/src/docs/developer/go-sdk.mdx "Anonymity Proxy",
// core/pkg/gateway/anon_proxy_handler.go, anon_tunnel_handler.go).
const (
	anonPath   = "/v1/proxy/anon"
	tunnelPath = "/v1/proxy/tunnel"
	// codeDestination is the refusal of a private destination (auth_errors.go).
	codeDestination = "DESTINATION_NOT_ALLOWED"
	// torCheckURL answers {"IsTor": true, "IP": ...} for a request that
	// arrived through Tor.
	torCheckURL = "https://check.torproject.org/api/ip"
	// torBudget covers a fresh node's Tor bootstrap and circuit build.
	torBudget = 3 * time.Minute
	torUnit   = "orama-namespace-tor@index.service"
)

// privateDestinations are the addresses "Private destinations are refused"
// names: loopback, RFC1918 (the WireGuard mesh), link-local (cloud
// metadata), localhost.
var privateDestinations = []string{
	"http://127.0.0.1/", "http://10.0.0.1:10100/status", "http://169.254.169.254/latest/meta-data/",
	"http://localhost/", "http://[::1]/", "http://192.168.1.1/", "http://100.64.0.1/",
}

func anon(t testing.TB, c *gw.Client, bearer string, body string) *gw.Response {
	t.Helper()
	return c.MustSend(t, gw.Req{Method: http.MethodPost, Path: anonPath, Bearer: bearer,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(body)})
}

// TestAnonProxy_privateDestinationsRefused: a request aimed at the mesh, the
// node itself or cloud metadata is refused, never dialled.
func TestAnonProxy_privateDestinationsRefused(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	for _, dest := range privateDestinations {
		resp := anon(t, n.Client, n.Owner.Token(), fmt.Sprintf(`{"url":%q}`, dest))
		if resp.Status != http.StatusForbidden || resp.ErrorCode() != codeDestination {
			t.Errorf("proxy to %s: %d %s, want 403 %s: %.200s", dest, resp.Status, resp.ErrorCode(), codeDestination, resp.Body)
		}
	}
}

// TestAnonProxy_malformedRequestsRefused: POST only, JSON only, http(s)
// only, and only ordinary methods.
func TestAnonProxy_malformedRequestsRefused(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	tok := n.Owner.Token()
	if resp := n.Client.MustSend(t, gw.Req{Method: http.MethodGet, Path: anonPath, Bearer: tok}); resp.Status != http.StatusMethodNotAllowed {
		t.Errorf("GET %s: %d, want 405", anonPath, resp.Status)
	}
	for label, body := range map[string]string{
		"not json":     `{`,
		"ftp scheme":   `{"url":"ftp://example.com/"}`,
		"file scheme":  `{"url":"file:///etc/passwd"}`,
		"connect verb": `{"url":"https://example.com/","method":"CONNECT"}`,
		"trace verb":   `{"url":"https://example.com/","method":"TRACE"}`,
	} {
		if resp := anon(t, n.Client, tok, body); resp.Status != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400: %.200s", label, resp.Status, resp.Body)
		}
	}
}

// TestAnonProxy_needsAUserNotAKey: no credential is 401, and an app-runtime
// API key alone is refused: the proxy is a per-user capability.
func TestAnonProxy_needsAUserNotAKey(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	body := `{"url":"` + torCheckURL + `"}`
	if resp := anon(t, n.Client, "", body); resp.Status != http.StatusUnauthorized {
		t.Errorf("no credential: %d, want 401", resp.Status)
	}
	key := tenancy.APIKey(t, n, "app-runtime")
	resp := n.Client.MustSend(t, gw.Req{Method: http.MethodPost, Path: anonPath, Bearer: key,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(body)})
	tenancy.ExpectDenied(t, resp, "app-runtime key alone on "+anonPath)
}

// TestAnonProxy_requestLeavesThroughTor: a proxied request reaches the
// destination through the anonymity network: the Tor project's checker sees
// a Tor exit, never a fleet node's address (website/src/docs/developer/go-sdk.mdx "Request
// proxy"). Every node runs the Tor client (website/src/docs/contributor/architecture-reference.mdx index units).
func TestAnonProxy_requestLeavesThroughTor(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, node := range f.State.Nodes {
		if st := f.Unit(t, node, torUnit); st != "active" {
			t.Errorf("%s: %s is %s", node.Name, torUnit, st)
		}
	}
	realistic.RequireReachableFromNode(t, f, f.State.Nodes[0], torCheckURL)
	n := tenancy.Namespace(t, f, ns.Options{})
	eventually.Require(t, 5*time.Second, torBudget, "request through Tor", func() (bool, error) {
		var out struct {
			StatusCode int    `json:"status_code"`
			Body       string `json:"body"`
		}
		resp := anon(t, n.Client, n.Owner.Token(), `{"url":"`+torCheckURL+`"}`)
		if resp.Status != http.StatusOK {
			return false, fmt.Errorf("HTTP %d: %.200s", resp.Status, resp.Body)
		}
		if err := resp.Decode(&out); err != nil {
			return false, eventually.Stop(err)
		}
		var check struct {
			IsTor bool   `json:"IsTor"`
			IP    string `json:"IP"`
		}
		// The gateway base64-encodes the destination's body so binary survives
		// the JSON envelope (anon_proxy_handler.go).
		decoded, err := base64.StdEncoding.DecodeString(out.Body)
		if err != nil {
			return false, eventually.Stop(fmt.Errorf("the proxied body is not base64: %w", err))
		}
		if err := json.Unmarshal(decoded, &check); err != nil {
			return false, fmt.Errorf("checker answered %d: %.200s", out.StatusCode, decoded)
		}
		if _, isNode := f.Lookup(check.IP); isNode || !check.IsTor {
			return false, eventually.Stop(fmt.Errorf("the destination saw %s (IsTor=%v): not a Tor exit", check.IP, check.IsTor))
		}
		return true, nil
	})
}

// TestAnonTunnel_refusals: the tunnel is a WebSocket for a signed-in user to
// a public host on port 80 or 443; everything else is refused before any
// dial (website/src/docs/developer/go-sdk.mdx "Anonymity tunnel").
func TestAnonTunnel_refusals(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	tok := n.Owner.Token()
	if resp := n.Client.MustSend(t, gw.Req{Method: http.MethodGet, Path: tunnelPath, Bearer: tok,
		Query: url.Values{"host": {"example.com"}}}); resp.Status != http.StatusBadRequest {
		t.Errorf("plain GET on the tunnel: %d, want 400", resp.Status)
	}
	cases := map[string]struct {
		query string
		token string
		want  int
	}{
		"no credential":   {"host=example.com&port=443", "", http.StatusUnauthorized},
		"loopback host":   {"host=127.0.0.1&port=443", tok, http.StatusBadRequest},
		"mesh host":       {"host=10.0.0.1&port=443", tok, http.StatusBadRequest},
		"localhost":       {"host=localhost&port=80", tok, http.StatusBadRequest},
		"metadata host":   {"host=169.254.169.254&port=80", tok, http.StatusBadRequest},
		"ssh port":        {"host=example.com&port=22", tok, http.StatusBadRequest},
		"no host":         {"port=443", tok, http.StatusBadRequest},
		"port not number": {"host=example.com&port=https", tok, http.StatusBadRequest},
	}
	for label, c := range cases {
		conn, resp, err := n.Client.DialWS(t.Context(), tunnelPath+"?"+c.query, c.token, nil)
		if err == nil {
			conn.Close()
			t.Errorf("%s: the tunnel opened", label)
			continue
		}
		if resp == nil || resp.StatusCode != c.want {
			t.Errorf("%s: handshake answered %v, want %d (%v)", label, statusOf(resp), c.want, err)
		}
	}
}

// statusOf is a handshake response's status, or "no response".
func statusOf(resp *http.Response) any {
	if resp == nil {
		return "no response"
	}
	return resp.StatusCode
}
