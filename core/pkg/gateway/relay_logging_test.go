package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/DeBrosOfficial/network/pkg/gateway/routepolicy"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/telemetry/traffic"
)

// The relay keeps no per-stream timing or volume (bugboard #266): no
// request_logs row and no access-log line, so no size and no duration is
// written anywhere. The request metrics count it by status.
func loggedGateway(t *testing.T) (*Gateway, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	g := &Gateway{
		logger:  &logging.ColoredLogger{Logger: zap.New(core)},
		cfg:     &Config{ClientNamespace: "index"},
		traffic: traffic.New(nil),
	}
	g.logBatcher = &requestLogBatcher{gw: g, maxBatch: 1000}
	return g, logs
}

func serveLogged(g *Gateway, path string) {
	h := g.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Millisecond)
		_, _ = w.Write([]byte("0123456789"))
	}))
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = "198.51.100.7:4000"
	h.ServeHTTP(httptest.NewRecorder(), r)
}

func TestLoggingMiddleware_relayStreamWritesNoRowAndNoLine(t *testing.T) {
	g, logs := loggedGateway(t)
	serveLogged(g, relayPath)

	if n := len(g.logBatcher.entries); n != 0 {
		t.Errorf("the relay left %d request_logs rows", n)
	}
	for _, e := range logs.All() {
		t.Errorf("the relay logged %q %v", e.Message, e.Context)
	}
	snap := g.TrafficSnapshot()
	if snap.TotalRequests != 1 {
		t.Errorf("the relay was not counted: %+v", snap)
	}
	if snap.BytesPerSec != 0 || snap.P99Ms != 0 {
		t.Errorf("the relay left a size or a latency in the metrics: %+v", snap)
	}
}

func TestLoggingMiddleware_relayedDownloadKeepsTheRowWithoutTheAddress(t *testing.T) {
	g, logs := loggedGateway(t)
	serveLogged(g, "/v1/storage/relayed/bafy")

	if len(g.logBatcher.entries) != 1 {
		t.Fatalf("rows = %d, want 1", len(g.logBatcher.entries))
	}
	if ip := g.logBatcher.entries[0].ip; ip != "" {
		t.Errorf("the row carries the address %q", ip)
	}
	if logs.Len() == 0 {
		t.Error("the relayed download writes no access-log line")
	}
	for _, e := range logs.All() {
		for _, f := range e.Context {
			if f.String == "198.51.100.7" {
				t.Errorf("the access log names the address: %v", e.Context)
			}
		}
	}
}

func TestLoggingMiddleware_anOrdinaryRouteIsFullyLogged(t *testing.T) {
	g, logs := loggedGateway(t)
	serveLogged(g, "/v1/storage/get/bafy")
	if len(g.logBatcher.entries) != 1 || g.logBatcher.entries[0].bytesOut != 10 {
		t.Errorf("rows = %+v", g.logBatcher.entries)
	}
	if logs.Len() == 0 {
		t.Error("no access-log line")
	}
}

func TestRoutePolicy_relayLevels(t *testing.T) {
	if got := policyOf(http.MethodGet, relayPath).RequestLog; got != routepolicy.LogNone {
		t.Errorf("relay level = %v", got)
	}
	if got := policyOf(http.MethodGet, "/v1/storage/relayed/x").RequestLog; got != routepolicy.LogNoAddress {
		t.Errorf("relayed level = %v", got)
	}
	if got := policyOf(http.MethodGet, "/v1/storage/get/x").RequestLog; got != routepolicy.LogFull {
		t.Errorf("get level = %v", got)
	}
}
