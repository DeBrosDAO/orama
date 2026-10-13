package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

const originHost = "ns-acme.example.test"

func upgradeRequest(origin, forwardedHost string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "https://"+originHost+"/v1/pubsub/ws?topic=t", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if forwardedHost != "" {
		r.Header.Set("X-Forwarded-Host", forwardedHost)
	}
	return r
}

func TestRefuseCrossSiteUpgrade(t *testing.T) {
	for name, tc := range map[string]struct {
		origin, forwardedHost string
		refused               bool
	}{
		"no origin, a non-browser client": {"", "", false},
		"the namespace's own origin":      {"https://" + originHost, "", false},
		"a name under it":                 {"https://app." + originHost, "", false},
		"a foreign site":                  {"https://evil.example", "", true},
		"a suffix lookalike":              {"https://" + originHost + ".evil.example", "", true},
		"a prefix lookalike":              {"https://evil" + originHost, "", true},
		"the parent domain":               {"https://example.test", "", true},
		"null":                            {"null", "", true},
		"a foreign site naming its own":   {"https://evil.example", "evil.example", true},
		"a forged host cannot legitimise": {"https://evil.example", "evil.example:443", true},
		"a forged host cannot refuse":     {"https://" + originHost, "evil.example", false},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r := upgradeRequest(tc.origin, tc.forwardedHost)

			if got := refuseCrossSiteUpgrade(rec, r); got != tc.refused {
				t.Fatalf("refused = %v, want %v", got, tc.refused)
			}
			if tc.refused && rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
			if !tc.refused && rec.Code != http.StatusOK {
				t.Errorf("a request that was not refused was answered %d", rec.Code)
			}
		})
	}
}

func TestRefuseCrossSiteUpgrade_ignoresARequestThatIsNotAnUpgrade(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://"+originHost+"/v1/health", nil)
	r.Header.Set("Origin", "https://evil.example")
	if refuseCrossSiteUpgrade(httptest.NewRecorder(), r) {
		t.Error("a plain request was refused as a cross-site upgrade; CORS is what judges those")
	}
}

// A cross-site upgrade was answered 404 for a namespace whose gateway the
// registry could not resolve, because the origin was checked only by the
// upgrader behind the proxy hop. Here the gateway has no registry at all: any
// attempt to choose a backend would fail, and the refusal must not need one.
func TestProxyToNamespaceGateway_refusesACrossSiteUpgradeBeforeChoosingABackend(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{logger: logger, cfg: &Config{}}
	rec := httptest.NewRecorder()

	g.proxyToNamespaceGateway(rec, upgradeRequest("https://evil.example", "evil.example"), "acme", namespaceProxyAuth{})

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}
