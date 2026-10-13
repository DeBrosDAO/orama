package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// warnAndAbove puts an observing logger on g and returns the entries at warn
// level and above.
func warnAndAbove(g *Gateway) func() []observer.LoggedEntry {
	core, logs := observer.New(zapcore.DebugLevel)
	g.logger = &logging.ColoredLogger{Logger: zap.New(core)}
	return func() []observer.LoggedEntry {
		var out []observer.LoggedEntry
		for _, e := range logs.All() {
			if e.Level >= zapcore.WarnLevel {
				out = append(out, e)
			}
		}
		return out
	}
}

// A client that leaves is not a gateway error: the hop it cancelled is not
// logged at warn or error level, whichever hop it was. The same failure with
// the client still there is logged, so the silence is the client's and not the
// log's.
func TestClientThatLeaves_isNotLoggedAsAGatewayError(t *testing.T) {
	deadApp := deadLocalPort(t)
	scenarios := map[string]func(t *testing.T) (g *Gateway, hop func(*http.Request)){
		"local app": func(t *testing.T) (*Gateway, func(*http.Request)) {
			g := deploymentGateway(t)
			g.nodePeerID = ""
			d := &deployments.Deployment{ID: "dep-shop", Namespace: "acme", Name: "shop", Port: deadApp}
			return g, func(r *http.Request) { g.proxyToDynamicDeployment(httptest.NewRecorder(), r, d) }
		},
		"every replica": func(t *testing.T) (*Gateway, func(*http.Request)) {
			g := replicaGateway(t, &peerListDB{port: 0})
			d := &deployments.Deployment{ID: "dep-shop", Namespace: "acme", Name: "shop", HomeNodeID: "peer-home", Port: 1}
			return g, func(r *http.Request) { g.proxyToDynamicDeployment(httptest.NewRecorder(), r, d) }
		},
		"home node": func(t *testing.T) (*Gateway, func(*http.Request)) {
			g := deploymentGateway(t)
			peer := httptest.NewServer(http.NotFoundHandler())
			addr := hostPort(peer)
			peer.Close()
			return g, func(r *http.Request) {
				g.forwardToHomeNode(httptest.NewRecorder(), r, appDeployment("dep-shop", "shop"), "127.0.0.1", addr, time.Second)
			}
		},
		"one replica": func(t *testing.T) (*Gateway, func(*http.Request)) {
			g := deploymentGateway(t)
			peer := httptest.NewServer(http.NotFoundHandler())
			addr := hostPort(peer)
			peer.Close()
			return g, func(r *http.Request) {
				g.forwardToReplica(httptest.NewRecorder(), r, appDeployment("dep-shop", "shop"), "127.0.0.1", addr)
			}
		},
	}
	for name, build := range scenarios {
		t.Run(name, func(t *testing.T) {
			g, hop := build(t)
			logged := warnAndAbove(g)

			hop(httptest.NewRequest(http.MethodGet, "/", nil))
			if len(logged()) == 0 {
				t.Fatal("a hop that failed with its client still there logged nothing, so the test checks nothing")
			}

			g, hop = build(t)
			logged = warnAndAbove(g)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			hop(httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
			for _, e := range logged() {
				t.Errorf("a client that left was logged as %s: %q %v", e.Level, e.Message, e.ContextMap())
			}
		})
	}
}
