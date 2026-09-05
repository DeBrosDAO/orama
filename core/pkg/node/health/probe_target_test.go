package health

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// The probe port was a literal once. It survived a port migration untouched,
// every probe on a healthy fleet failed, and the ring evicted the whole cluster
// about seven minutes after the gateways came up. Pin the target so a future
// port move cannot repeat it silently.
func TestProbeUsesConfiguredPort(t *testing.T) {
	var gotPath atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	port := u.Port()
	portNum := 0
	if _, err := fmt.Sscanf(port, "%d", &portNum); err != nil {
		t.Fatalf("parse port %q: %v", port, err)
	}

	m, err := NewMonitor(Config{NodeID: "self", ProbePort: portNum})
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}

	if ok := m.probe(context.Background(), nodeInfo{ID: "peer", InternalIP: u.Hostname()}); !ok {
		t.Fatal("probe against a live server returned unhealthy")
	}
	if got := gotPath.Load(); got != "/v1/internal/ping" {
		t.Errorf("probe path = %v, want /v1/internal/ping", got)
	}
}

// A wrong port must fail the probe rather than pass it: the detector's whole
// job is to notice unreachability.
func TestProbeFailsOnClosedPort(t *testing.T) {
	m, err := NewMonitor(Config{NodeID: "self", ProbePort: 1, ProbeTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	if m.probe(context.Background(), nodeInfo{ID: "peer", InternalIP: "127.0.0.1"}) {
		t.Error("probe against a closed port reported healthy")
	}
}

// A peer that wrote dns_nodes.last_seen recently did a raft write, so it was
// alive and had quorum. That must stand in for the HTTP probe — otherwise a
// single wrong port or a gateway restart marks a healthy node dead.
func TestFreshHeartbeatSkipsHTTPProbe(t *testing.T) {
	var probes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	portNum := 0
	_, _ = fmt.Sscanf(u.Port(), "%d", &portNum)

	m, err := NewMonitor(Config{NodeID: "self", ProbePort: portNum})
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}

	fresh := nodeInfo{ID: "peer", InternalIP: u.Hostname(), HeartbeatFresh: true}
	if !m.probeNode(context.Background(), fresh) {
		t.Error("peer with a fresh heartbeat was reported unhealthy")
	}
	if n := probes.Load(); n != 0 {
		t.Errorf("fresh heartbeat still issued %d HTTP probe(s)", n)
	}

	// A stale heartbeat proves nothing, so it must fall through to the probe —
	// which this server fails, so the peer counts as a miss.
	stale := nodeInfo{ID: "peer", InternalIP: u.Hostname(), HeartbeatFresh: false}
	if m.probeNode(context.Background(), stale) {
		t.Error("stale heartbeat + failing probe was reported healthy")
	}
	if n := probes.Load(); n != 1 {
		t.Errorf("stale heartbeat issued %d HTTP probes, want 1", n)
	}
}

// A missing ProbePort is a programming error that used to be invisible.
func TestNewMonitorRejectsMissingProbePort(t *testing.T) {
	if _, err := NewMonitor(Config{NodeID: "self"}); err == nil {
		t.Fatal("NewMonitor accepted a zero ProbePort")
	}
}
