package report

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// systemctl list-units --all --plain --type=service --no-legend output.
const deployUnitsFixture = `orama-deploy-node@acme-web.service     loaded    active   running Orama deployment acme-web
orama-deploy-go@acme-api.service       loaded    failed   failed  Orama deployment acme-api
orama-deploy-npm@beta-site.service     loaded    inactive dead    Orama deployment beta-site
orama-deploy-node@gone-app.service     not-found inactive dead    orama-deploy-node@gone-app.service
`

func TestParseDeploymentUnits_countsLoadedUnits(t *testing.T) {
	got := parseDeploymentUnits(deployUnitsFixture)
	// A cleanly stopped deployment (inactive/dead) is not failed; it used to be
	// counted as one.
	if got.TotalCount != 3 || got.RunningCount != 1 || got.FailedCount != 1 {
		t.Errorf("got %+v, want total 3, running 1, failed 1", got)
	}
}

func TestParseDeploymentUnits_empty(t *testing.T) {
	if got := parseDeploymentUnits(""); got.TotalCount != 0 || got.RunningCount != 0 || got.FailedCount != 0 {
		t.Errorf("no units produced %+v", got)
	}
}

func TestParseDeploymentUnits_shortLinesIgnored(t *testing.T) {
	if got := parseDeploymentUnits("orama-deploy-node@x.service loaded\n\n"); got.TotalCount != 0 {
		t.Errorf("a truncated line was counted: %+v", got)
	}
}

// The serverless probe used to hit http://localhost:8080 — Kubo's IPFS
// gateway (constants.IPFSGatewayPort) — and so reported the IPFS gateway's
// answer as the engine's. TestCollectors_noStaleServicePorts pins the address.
func TestServerlessStatus_healthyAndUnhealthy(t *testing.T) {
	for code, want := range map[int]string{
		http.StatusOK:                 "healthy",
		http.StatusServiceUnavailable: "unhealthy (HTTP 503)",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		got := serverlessStatus(context.Background(), srv.URL+"/v1/health")
		srv.Close()
		if got.EngineStatus != want {
			t.Errorf("HTTP %d: got %q, want %q", code, got.EngineStatus, want)
		}
	}
}

func TestServerlessStatus_unreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + "/v1/health"
	srv.Close()
	if got := serverlessStatus(context.Background(), url); got.EngineStatus != "unreachable" {
		t.Errorf("got %q", got.EngineStatus)
	}
}

// The body is what pkg/gateway publicHealth serves; the collector used to read
// "subsystems" and "version", which that body has never had.
func TestGatewayStatus_readsPublicHealthChecks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"status":"degraded","server":{"started_at":"2026-09-27T10:00:00Z","uptime":"2h0m0s"},
			"checks":{"rqlite":{"status":"ok"},"olric":{"status":"error"},"ipfs":{"status":"unavailable"}}}`)
	})
	mux.HandleFunc("/v1/version", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"version":"0.200.0","commit":"63da9cc8","build_time":"","started_at":"2026-09-27T10:00:00Z","uptime":"2h0m0s"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	r := gatewayStatus(context.Background(), srv.URL)
	if !r.Responsive || r.HTTPStatus != http.StatusServiceUnavailable || r.Version != "0.200.0" {
		t.Errorf("got %+v", r)
	}
	if len(r.Subsystems) != 3 || r.Subsystems["olric"].Status != "error" || r.Subsystems["rqlite"].Status != "ok" {
		t.Errorf("subsystems = %+v", r.Subsystems)
	}
}

func TestGatewayStatus_down(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	if r := gatewayStatus(context.Background(), base); r.Responsive || r.Subsystems != nil {
		t.Errorf("a stopped gateway reported %+v", r)
	}
}

// The memberlist listens on constants.OlricMemberlistPort; the collector used
// to look for 3322, the pre-migration port, and always reported it down.
func TestMemberlistUp_indexMemberlistPort(t *testing.T) {
	port := strconv.Itoa(constants.OlricMemberlistPort)
	ss := "State  Recv-Q Send-Q Local Address:Port Peer Address:Port Process\n" +
		"LISTEN 0      4096   10.0.0.1:" + port + "      0.0.0.0:*     users:((\"olric-server\",pid=812,fd=9))\n"
	if !memberlistUp(ss) {
		t.Errorf("the listener on %s was not seen", port)
	}
	legacy := "LISTEN 0      4096   10.0.0.1:3322      0.0.0.0:*     users:((\"olric-server\",pid=812,fd=9))\n"
	if memberlistUp(legacy) {
		t.Error("the legacy 3322 listener counted as the memberlist")
	}
	if memberlistUp("") {
		t.Error("empty ss output counted as listening")
	}
}

// No collector may address a port that disagrees with pkg/constants.
func TestCollectors_noStaleServicePorts(t *testing.T) {
	for _, file := range []string{"deployments.go", "olric.go", "gateway.go", "chain.go"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		src := string(data)
		for _, stale := range []string{"localhost:8080", "3322"} {
			if strings.Contains(src, stale) {
				t.Errorf("%s still contains %q", file, stale)
			}
		}
	}
}
