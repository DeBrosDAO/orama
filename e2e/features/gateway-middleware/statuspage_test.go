//go:build e2e_fleet

package gatewaymiddleware

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// statusCSP is the page's policy: its own script and style, its own origin
// for data, nothing inline (core/pkg/gateway/statuspage/statuspage.go).
const statusCSP = "default-src 'none'; script-src 'self'; style-src 'self'; " +
	"connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// TestStatusPage_htmlForBrowsersJSONForTheRest: /status is the page to a
// browser and the /v1/status JSON to anything else, with Vary: Accept so a
// shared cache keeps them apart; the page loads only its own script and
// stylesheet under an exact CSP (website/src/docs/operator/monitoring.mdx "Public status page";
// docs/whitepaper/technical-reference/appendices/i-api-surface.md "/status").
func TestStatusPage_htmlForBrowsersJSONForTheRest(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	page := c.MustSend(t, gw.Req{Path: "/status", Header: http.Header{"Accept": {"text/html,application/xhtml+xml"}}}).Expect(t, http.StatusOK)
	if ct := page.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("page Content-Type %q", ct)
	}
	if got := page.Header.Get("Content-Security-Policy"); got != statusCSP {
		t.Errorf("page CSP %q, want %q", got, statusCSP)
	}
	for _, ref := range []string{`src="/status/assets/app.js"`, `href="/status/assets/app.css"`} {
		if !bytes.Contains(page.Body, []byte(ref)) {
			t.Errorf("the page does not load %s", ref)
		}
	}
	if bytes.Contains(page.Body, []byte("<script>")) || bytes.Contains(page.Body, []byte("style=")) {
		t.Error("the page carries inline script or style, which its CSP forbids")
	}
	for _, accept := range []string{"application/json", "", "*/*"} {
		resp := c.MustSend(t, gw.Req{Path: "/status", Header: http.Header{"Accept": {accept}}}).Expect(t, http.StatusOK)
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Accept %q: Content-Type %q, want JSON", accept, ct)
		}
		if !strings.Contains(strings.Join(resp.Header.Values("Vary"), ","), "Accept") {
			t.Errorf("Accept %q: no Vary: Accept", accept)
		}
	}
}

// TestStatusPage_assetsServedUnderTheCSP: both assets load with the page's
// CSP and nosniff; an asset that does not exist is 404, and a traversal out
// of the assets directory reaches nothing.
func TestStatusPage_assetsServedUnderTheCSP(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	for path, ct := range map[string]string{"/status/assets/app.js": "javascript", "/status/assets/app.css": "text/css"} {
		resp := c.MustSend(t, gw.Req{Path: path}).Expect(t, http.StatusOK)
		if !strings.Contains(resp.Header.Get("Content-Type"), ct) || resp.Header.Get("Content-Security-Policy") != statusCSP ||
			resp.Header.Get("X-Content-Type-Options") != "nosniff" || len(resp.Body) == 0 {
			t.Errorf("%s: %s CSP %q nosniff %q, %d bytes", path, resp.Header.Get("Content-Type"),
				resp.Header.Get("Content-Security-Policy"), resp.Header.Get("X-Content-Type-Options"), len(resp.Body))
		}
	}
	if resp := c.MustSend(t, gw.Req{Path: "/status/assets/nope.js"}); resp.Status != http.StatusNotFound {
		t.Errorf("a missing asset answered %d", resp.Status)
	}
	raw, err := c.Raw(t.Context(), []byte("GET /status/assets/..%2f..%2findex.html HTTP/1.1\r\nHost: "+harness.Fleet(t).State.BaseDomain+"\r\nConnection: close\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	// The mux answers an unclean path with a redirect to the cleaned one, so
	// the request is never served as sent; what must hold is that nothing
	// answers 200 for it.
	if bytes.HasPrefix(raw, []byte("HTTP/1.1 200")) {
		t.Errorf("an encoded traversal under /status/assets/ was served: %.200q", raw)
	}
	raw, err = c.Raw(t.Context(), []byte("GET /status/assets/../../etc/passwd HTTP/1.1\r\nHost: "+harness.Fleet(t).State.BaseDomain+"\r\nConnection: close\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(raw, []byte("HTTP/1.1 200")) || bytes.Contains(raw, []byte("root:")) {
		t.Errorf("a dot-dot traversal under /status/assets/ was served: %.200q", raw)
	}
}

// TestStatusPage_subdomainStatusIsTheDeployments: only the bare base domain
// serves the platform's page; on any other name /status belongs to that
// name's deployment, and with none it is not found (website/src/docs/operator/monitoring.mdx
// "Public status page").
func TestStatusPage_subdomainStatusIsTheDeployments(t *testing.T) {
	t.Parallel()
	host := edge.RandomLabel(t, "nodeploy-") + "." + harness.Fleet(t).State.BaseDomain
	resp := harness.GW(t).MustSend(t, gw.Req{Path: "/status", Host: host, Header: http.Header{"Accept": {"text/html"}}})
	if resp.Status != http.StatusNotFound || resp.Header.Get("Content-Security-Policy") == statusCSP {
		t.Fatalf("/status on %s answered %d with CSP %q, want 404 (no deployment there), not the platform page",
			host, resp.Status, resp.Header.Get("Content-Security-Policy"))
	}
}
