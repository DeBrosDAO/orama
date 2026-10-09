package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

const leakedCredential = "SECRET-KEY-VALUE"

// observed puts an observing logger on g and returns what it logs, rendered as
// one string per entry: message and every field.
func observed(g *Gateway) func() []string {
	core, logs := observer.New(zapcore.DebugLevel)
	g.logger = &logging.ColoredLogger{Logger: zap.New(core)}
	return func() []string {
		var out []string
		for _, e := range logs.All() {
			out = append(out, fmt.Sprintf("%s %v", e.Message, e.ContextMap()))
		}
		return out
	}
}

func assertNoCredential(t *testing.T, lines []string) {
	t.Helper()
	if len(lines) == 0 {
		t.Fatal("nothing was logged, so the test checks nothing")
	}
	for _, l := range lines {
		if strings.Contains(l, leakedCredential) || strings.Contains(l, "api_key") || strings.Contains(l, "token=") {
			t.Errorf("a log line quotes the request URL's query: %s", l)
		}
	}
}

// The error of the HTTP client quotes the whole target URL, and a credential
// can be in the query string. It reached the node log, and the client was told
// the member's overlay address, port, path and query.
func TestNamespaceProxy_neitherLogNorClientSeesTheRequestURL(t *testing.T) {
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: freePort(t)})
	lines := observed(g)

	rec := proxyOnce(g, "acme", get("/v1/functions?api_key="+leakedCredential))

	assertNoCredential(t, lines())
	body := rec.Body.String()
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(body, namespaceGatewayUnavailableMessage) {
		t.Fatalf("got %d %s, want the fixed 503 message", rec.Code, body)
	}
	for _, leaked := range []string{leakedCredential, "api_key", "127.0.0.1", "/v1/functions", "dial"} {
		if strings.Contains(body, leaked) {
			t.Errorf("the client was told %q: %s", leaked, body)
		}
	}
}

func TestForwardToHomeNode_theLogCarriesNoRequestURL(t *testing.T) {
	g := deploymentGateway(t)
	lines := observed(g)
	r := httptest.NewRequest(http.MethodGet, "/?token="+leakedCredential, nil)
	dead := "127.0.0.1:" + strconv.Itoa(freePort(t))
	if g.forwardToHomeNode(httptest.NewRecorder(), r, appDeployment("dep-shop", "shop"), "127.0.0.1", dead, time.Second) {
		t.Fatal("a refused connection was served")
	}
	assertNoCredential(t, lines())
}

func TestForwardToReplica_theLogCarriesNoRequestURL(t *testing.T) {
	g := deploymentGateway(t)
	lines := observed(g)
	r := httptest.NewRequest(http.MethodGet, "/?token="+leakedCredential, nil)
	dead := "127.0.0.1:" + strconv.Itoa(freePort(t))
	if g.forwardToReplica(httptest.NewRecorder(), r, appDeployment("dep-shop", "shop"), "127.0.0.1", dead) {
		t.Fatal("a refused connection was served")
	}
	assertNoCredential(t, lines())
}

func TestProxyToDynamicDeployment_aDownProcessLogCarriesNoRequestURL(t *testing.T) {
	g := deploymentGateway(t)
	g.nodePeerID = ""
	lines := observed(g)
	d := &deployments.Deployment{ID: "dep-shop", Namespace: "acme", Name: "shop", Port: freePort(t)}
	rec := httptest.NewRecorder()
	g.proxyToDynamicDeployment(rec, httptest.NewRequest(http.MethodGet, "/?token="+leakedCredential, nil), d)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
	assertNoCredential(t, lines())
}
