package gateway

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/httputil"
)

// markerApp answers 503 and sets the tenant-origin marker as the tenant's code
// would: blank, or forged.
func markerApp(t *testing.T, values ...string) int {
	t.Helper()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()[httputil.HeaderTenantOrigin] = values
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(app.Close)
	return serverPort(app)
}

// The app's own marker was copied to the client with the rest of its headers,
// and on a forwarded request the platform's was added beside it.
func TestProxyToDynamicDeployment_anAppCannotSetTheMarker(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sent    []string
		wantFwd []string
	}{
		{"empty", []string{""}, []string{"1"}},
		{"fake", []string{"forged"}, []string{"1"}},
		{"two", []string{"", "forged"}, []string{"1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := deploymentGateway(t)
			g.nodePeerID = ""
			d := &deployments.Deployment{ID: "dep-shop", Namespace: "acme", Name: "shop", Port: markerApp(t, tc.sent...)}

			forwarded := httptest.NewRequest(http.MethodGet, "/", nil)
			forwarded.Header.Set("X-Orama-Proxy-Node", "peer-a")
			rec := httptest.NewRecorder()
			g.proxyToDynamicDeployment(rec, forwarded, d)
			if got := rec.Header().Values(httputil.HeaderTenantOrigin); !reflect.DeepEqual(got, tc.wantFwd) {
				t.Errorf("forwarded request: marker %q, want only the platform's %q", got, tc.wantFwd)
			}

			direct := httptest.NewRecorder()
			g.proxyToDynamicDeployment(direct, httptest.NewRequest(http.MethodGet, "/", nil), d)
			if got := direct.Header().Values(httputil.HeaderTenantOrigin); len(got) != 0 {
				t.Errorf("client request: marker %q reached the client, want none", got)
			}
		})
	}
}

// A node relaying through the hop must not pass an app's marker to a client.
func TestForwardToHomeNode_aNodesMarkerNeverReachesTheClientWhateverItsValue(t *testing.T) {
	g := deploymentGateway(t)
	for _, sent := range [][]string{{""}, {"forged"}, {"1"}} {
		rec, served := forwardHome(g, appDeployment("dep-shop", "shop"), "127.0.0.1:"+strconv.Itoa(markerApp(t, sent...)))
		if !served || len(rec.Header().Values(httputil.HeaderTenantOrigin)) != 0 {
			t.Errorf("marker %q: served=%v, client saw %q", sent, served, rec.Header().Values(httputil.HeaderTenantOrigin))
		}
	}
}

// A marker that is present counts, whatever its value: blanking it must not
// make a tenant's 503 the gateway's.
func TestIsUpstreamFailure_aPresentMarkerCountsEvenWhenEmpty(t *testing.T) {
	resp := &http.Response{StatusCode: 503, Header: http.Header{httputil.HeaderTenantOrigin: {""}}}
	if isUpstreamFailure(resp) {
		t.Error("a 503 with an empty tenant-origin marker was held against the gateway")
	}
	resp.Header = http.Header{}
	if !isUpstreamFailure(resp) {
		t.Error("a 503 with no marker was not held against the gateway")
	}
}

func TestMarkTenantOrigin_replacesWhatWasThere(t *testing.T) {
	h := http.Header{httputil.HeaderTenantOrigin: {"", "forged"}}
	httputil.MarkTenantOrigin(h)
	if got := h.Values(httputil.HeaderTenantOrigin); !reflect.DeepEqual(got, []string{"1"}) {
		t.Errorf("marker = %q, want exactly [1]", got)
	}
}
