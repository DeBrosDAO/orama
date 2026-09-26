package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	operatorhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	"go.uber.org/zap"
)

// readyGatewayWithReport is a ready gateway whose health report is already
// computed, so the handlers can be read without running real checks.
func readyGatewayWithReport(t *testing.T, operators ...string) *Gateway {
	t.Helper()
	g, db := registryGateway(t, "index", operators...)
	g.ready = newReadiness()
	g.ready.set(ReadinessReady, "", "")
	g.operatorHandler = operatorhandlers.NewHandler(zap.NewNop(), db)
	g.healthCache = &cachedHealthResult{
		httpStatus: http.StatusOK,
		cachedAt:   time.Now(),
		response: map[string]any{
			"status": "healthy",
			"server": g.serverInfo(),
			"checks": map[string]checkResult{
				"rqlite": {Status: "ok", Latency: "1.2ms"},
				"libp2p": {Status: "ok", Peers: 4},
				"ipfs":   {Status: "error", Error: "ipfs check failed; see this node's gateway log"},
			},
			"namespaces": map[string]any{"alice": map[string]any{"gateway": map[string]any{"port": 10204}}},
		},
	}
	return g
}

// The finding: /v1/health listed every namespace on the node with its internal
// ports, to anyone.
func TestHealth_anonymousCallerSeesStatusOnly(t *testing.T) {
	g := readyGatewayWithReport(t)
	rec := httptest.NewRecorder()
	g.healthHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, leak := range []string{"alice", "10204", "namespaces", "latency", "peers", "gateway log"} {
		if strings.Contains(body, leak) {
			t.Errorf("the anonymous health report carries %q: %s", leak, body)
		}
	}
	var got struct {
		Status string                       `json:"status"`
		Checks map[string]map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "healthy" || got.Checks["ipfs"]["status"] != "error" || got.Checks["rqlite"]["status"] != "ok" {
		t.Errorf("the status was lost with the detail: %+v", got)
	}
}

// The detail is an operator's.
func TestOperatorHealth_refusesAnyoneElse(t *testing.T) {
	g := readyGatewayWithReport(t, "0xoperator")
	rec := httptest.NewRecorder()
	g.operatorHealthHandler(rec, walletRequest("/v1/operator/health", "0xtenant"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "alice") {
		t.Error("a refusal carried the namespace list")
	}
}

func TestOperatorHealth_showsAnOperatorEverything(t *testing.T) {
	g := readyGatewayWithReport(t, "0xoperator")
	rec := httptest.NewRecorder()
	g.operatorHealthHandler(rec, walletRequest("/v1/operator/health", "0xoperator"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"alice", "10204", "1.2ms"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("the operator report lacks %q", want)
		}
	}
}

// /v1/status embedded this node's peer id, its peers and its storage peers'
// swarm addresses.
func TestStatus_carriesNoNetworkMap(t *testing.T) {
	g := readyGatewayWithReport(t)
	rec := httptest.NewRecorder()
	g.statusHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["network"]; ok {
		t.Errorf("/v1/status carries the network status: %s", rec.Body.String())
	}
	if got["status"] != "ok" {
		t.Errorf("status = %v", got["status"])
	}
}

// The ping answers the prober and names nothing.
func TestPing_namesNoNode(t *testing.T) {
	g := &Gateway{nodePeerID: "12D3KooWnode"}
	rec := httptest.NewRecorder()
	g.pingHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/internal/ping", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "12D3KooWnode") {
		t.Errorf("status %d, body %s", rec.Code, rec.Body.String())
	}
}

func coordinationRequest(t *testing.T, path, remote string, key []byte) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = remote
	if key != nil {
		if err := nodeauth.SignCoordination(key, r, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// Another node's discovery reaches the network status with a coordination MAC
// over the mesh, and only so.
func TestNetworkDetail_admitsANodeOverTheMesh(t *testing.T) {
	g := readyGatewayWithReport(t, "0xoperator")
	g.cfg.ClusterSecret = "network-detail-secret"
	key, err := nodeauth.CoordinationKey(g.cfg.ClusterSecret)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		r    *http.Request
		want bool
	}{
		"a node on the mesh":           {coordinationRequest(t, "/v1/network/status", "10.0.0.4:40000", key), true},
		"a MAC from off the mesh":      {coordinationRequest(t, "/v1/network/status", "198.51.100.9:40000", key), false},
		"a MAC under the wrong key":    {coordinationRequest(t, "/v1/network/status", "10.0.0.4:40000", []byte("wrong")), false},
		"a tenant's credential":        {walletRequest("/v1/network/status", "0xtenant"), false},
		"an operator's credential":     {walletRequest("/v1/network/status", "0xoperator"), true},
		"nobody at all, from loopback": {httptest.NewRequest(http.MethodGet, "/v1/network/status", nil), false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			if got := g.authorizeNetworkDetail(rec, c.r); got != c.want {
				t.Errorf("admitted = %v, want %v (status %d)", got, c.want, rec.Code)
			}
		})
	}
}

// The policy sends only a stamped request past the middleware without a
// credential; everything else needs the operator grant first.
func TestNetworkDetailPolicy(t *testing.T) {
	plain := httptest.NewRequest(http.MethodGet, "/v1/network/status", nil)
	if networkDetailPolicy(plain).Access.Anonymous() {
		t.Error("an unstamped request reaches the network status without a credential")
	}
	stamped := httptest.NewRequest(http.MethodGet, "/v1/network/status", nil)
	stamped.Header.Set(nodeauth.CoordinationMACHeader, "1.00")
	if !networkDetailPolicy(stamped).Access.Anonymous() {
		t.Error("a stamped request is asked for a credential it cannot have")
	}
}
