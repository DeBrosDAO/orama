package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/statuspage"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/hub"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"

	_ "github.com/mattn/go-sqlite3"
)

type onePeer struct{ p hub.Peer }

func (o onePeer) Peers(context.Context) ([]hub.Peer, error) { return []hub.Peer{o.p}, nil }

// telemetryGateway is a gateway whose telemetry reports one healthy node.
func telemetryGateway(t *testing.T) *Gateway {
	t.Helper()
	log, err := logging.NewColoredLogger(logging.ComponentGateway, false)
	if err != nil {
		t.Fatal(err)
	}
	self := &report.NodeReport{
		Timestamp: time.Now(),
		Hostname:  "athena",
		Gateway:   &report.GatewayReport{Responsive: true, HTTPStatus: http.StatusOK},
		RQLite:    &report.RQLiteReport{Responsive: true, RaftState: "Leader"},
	}
	agg := &hub.Aggregator{
		SelfID: "n1", Self: func() (*report.NodeReport, error) { return self, nil },
		Peers:    onePeer{hub.Peer{ID: "n1", PublicIP: "37.59.116.212", WGIP: "10.0.0.1"}},
		CacheTTL: time.Second, PeerTimeout: time.Second, MaxParallel: 1, StaleAfter: time.Minute,
	}
	return &Gateway{
		logger:    log,
		startedAt: time.Now(),
		cfg:       &Config{},
		telemetry: &telemetryService{agg: agg, uptime: hub.UptimeStore{DB: uptimeDB(t)}},
	}
}

// uptimeDB is a SQLite database holding the real uptime migration's table.
func uptimeDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	ddl, err := os.ReadFile("../../migrations/062_status_uptime.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestStreamInterval_bounds(t *testing.T) {
	if d, err := streamInterval(""); err != nil || d != streamDefaultInterval {
		t.Errorf("default = %v %v", d, err)
	}
	if d, err := streamInterval("10"); err != nil || d != 10*time.Second {
		t.Errorf("10 = %v %v", d, err)
	}
	for _, bad := range []string{"1", "61", "abc", "-5", "2.5"} {
		if _, err := streamInterval(bad); err == nil {
			t.Errorf("interval %q accepted", bad)
		}
	}
}

func TestStatusHandler_namespaceGatewayKeepsMinimalBody(t *testing.T) {
	g := &Gateway{startedAt: time.Now()}
	rec := httptest.NewRecorder()
	g.statusHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["server"] == nil || body["overall"] != nil {
		t.Fatalf("body = %v, want only status and server", body)
	}
}

func TestStatusHandler_publicViewWithoutNodeDetail(t *testing.T) {
	g := telemetryGateway(t)
	rec := httptest.NewRecorder()
	g.statusHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	var body struct {
		Status string `json:"status"`
		cluster.PublicStatus
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" || body.Nodes.Total != 1 || len(body.Components) == 0 {
		t.Fatalf("body = %s", rec.Body.String())
	}
	for _, secret := range []string{"37.59.116.212", "10.0.0.1", "athena"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("public status leaks %q", secret)
		}
	}
}

func TestStatusHandler_browserGetsPageUnderCSP(t *testing.T) {
	g := telemetryGateway(t)
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	rec := httptest.NewRecorder()
	g.statusHandler(rec, req)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type = %q", ct)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("csp = %q", csp)
	}
	if !strings.Contains(rec.Body.String(), "/status/assets/app.js") {
		t.Fatal("page does not load its script")
	}
}

func TestStatusHandler_v1StatusIsAlwaysJSON(t *testing.T) {
	g := telemetryGateway(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	g.statusHandler(rec, req)
	if !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("/v1/status answered a browser with non-JSON: %.80s", rec.Body.String())
	}
}

func TestInternalTelemetryHandler_unsignedIsNotFound(t *testing.T) {
	g := telemetryGateway(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/internal/telemetry", nil)
	req.RemoteAddr = "10.0.0.2:4000"
	rec := httptest.NewRecorder()
	g.internalTelemetryHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unsigned request = %d, want 404", rec.Code)
	}
}

func TestOperatorTelemetryHandler_withoutOperatorListIsUnavailable(t *testing.T) {
	g := telemetryGateway(t)
	rec := httptest.NewRecorder()
	g.operatorTelemetryHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/operator/telemetry", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the operator list cannot be checked", rec.Code)
	}
}

func TestStatusAssets_servedWithCSP(t *testing.T) {
	for _, asset := range []string{"app.js", "app.css"} {
		rec := httptest.NewRecorder()
		statuspage.Assets().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status/assets/"+asset, nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: status %d, csp %q", asset, rec.Code, rec.Header().Get("Content-Security-Policy"))
		}
	}
}

// routes.go registers these with literal patterns, which is what the route
// policy tests read; they must stay equal to the constants the code uses.
func TestTelemetryRoutes_matchConstants(t *testing.T) {
	if statuspage.AssetsPrefix != "/status/assets/" || hub.InternalReportPath != "/v1/internal/telemetry" {
		t.Fatal("a route literal in routes.go no longer matches its constant")
	}
}

// selfCollected is a SelfCollector that has collected rpt once.
func selfCollected(t *testing.T, rpt string) *hub.SelfCollector {
	t.Helper()
	s := &hub.SelfCollector{
		Collect:  func(context.Context) ([]byte, error) { return []byte(rpt), nil },
		Interval: time.Hour, Timeout: time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Run collects once, then sees the cancelled context and returns
	s.Run(ctx)
	return s
}

func TestInternalTelemetryHandler_signedMeshRequestGetsReportAndAge(t *testing.T) {
	g := telemetryGateway(t)
	g.cfg.ClusterSecret = "test-cluster-secret-with-enough-entropy-0123456789"
	g.cfg.NodePeerID = coordinationTestNode
	g.telemetry.self = selfCollected(t, `{"hostname":"athena","timestamp":"`+time.Now().UTC().Format(time.RFC3339Nano)+`"}`)
	req := httptest.NewRequest(http.MethodGet, "/v1/internal/telemetry", nil)
	req.RemoteAddr = "10.0.0.2:4000"
	key, err := nodeauth.CoordinationKey(g.cfg.ClusterSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := nodeauth.SignCoordination(key, req, time.Now(), coordinationTestNode); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	g.internalTelemetryHandler(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "athena") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get(hub.ReportAgeHeader) == "" {
		t.Fatal("no report age header: the peer could not judge staleness on the right clock")
	}
}

func TestInternalTelemetryHandler_unsignedPostIsNotFound(t *testing.T) {
	g := telemetryGateway(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/telemetry", nil)
	req.RemoteAddr = "203.0.113.9:4000"
	rec := httptest.NewRecorder()
	g.internalTelemetryHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unsigned POST = %d, want 404 (a 405 would confirm the route exists)", rec.Code)
	}
}

// flushRecorder is an httptest.ResponseRecorder that counts flushes.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushRecorder) Flush() { f.flushes++ }

func TestStreamSnapshots_sendsSnapshotEventsUntilClientLeaves(t *testing.T) {
	g := telemetryGateway(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/v1/operator/telemetry/stream", nil).WithContext(ctx)
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	g.streamSnapshots(req, rec, rec, g.telemetry.agg, time.Second)
	events := strings.Count(rec.Body.String(), "event: snapshot\ndata: {")
	if events < 2 || rec.flushes < events {
		t.Fatalf("%d snapshot events, %d flushes in 2.5s at a 1s interval: %q", events, rec.flushes, rec.Body.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(rec.Body.String()), "\n") {
		if strings.HasPrefix(line, "data: ") && !json.Valid([]byte(strings.TrimPrefix(line, "data: "))) {
			t.Fatalf("data line is not one line of JSON: %.80s", line)
		}
	}
}

type failingPeers struct{ calls *int }

func (f failingPeers) Peers(context.Context) ([]hub.Peer, error) {
	*f.calls++
	return nil, context.DeadlineExceeded
}

func TestPublicStatus_failureIsCachedForTTL(t *testing.T) {
	g := telemetryGateway(t)
	calls := 0
	g.telemetry.agg.Peers = failingPeers{calls: &calls}
	for i := 0; i < 5; i++ {
		ps := g.publicStatus(context.Background())
		if ps.Overall != cluster.StateUnknown {
			t.Fatalf("overall = %s, want unknown when no snapshot can be assembled", ps.Overall)
		}
	}
	if calls != 1 {
		t.Fatalf("5 public requests caused %d assemblies against a failing registry, want 1", calls)
	}
}

func TestReadinessPassthrough_monitoringRoutes(t *testing.T) {
	for _, p := range []string{"/status", "/v1/status", "/status/assets/app.js", "/v1/internal/telemetry"} {
		if !readinessPassthrough(p) {
			t.Errorf("%s is blocked while the gateway starts", p)
		}
	}
	for _, p := range []string{"/v1/operator/telemetry", "/v1/operator/telemetry/stream", "/status/assetsx"} {
		if readinessPassthrough(p) {
			t.Errorf("%s passes the readiness gate; it should not", p)
		}
	}
}

func TestStatusHandler_variesOnAccept(t *testing.T) {
	g := &Gateway{startedAt: time.Now()}
	rec := httptest.NewRecorder()
	g.statusHandler(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if !strings.Contains(rec.Header().Get("Vary"), "Accept") {
		t.Fatal("/status negotiates on Accept without Vary: Accept")
	}
}

func TestDecorateNodeReport_carriesTheDisownedTenants(t *testing.T) {
	g := &Gateway{}
	r := &report.NodeReport{}
	g.decorateNodeReport(r)
	if r.RegistryDisownedTenants != nil {
		t.Fatalf("got %v without a source", r.RegistryDisownedTenants)
	}
	g.SetRegistryDisownedSource(func() []string { return []string{"acme"} })
	g.decorateNodeReport(r)
	if len(r.RegistryDisownedTenants) != 1 || r.RegistryDisownedTenants[0] != "acme" {
		t.Fatalf("got %v", r.RegistryDisownedTenants)
	}
}
