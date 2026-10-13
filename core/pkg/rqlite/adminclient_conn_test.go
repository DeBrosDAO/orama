package rqlite

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

// countingRQLite is a fake rqlited that counts the TCP connections opened to it.
func countingRQLite(t *testing.T, status int) (Endpoint, *atomic.Int64) {
	t.Helper()
	var opened atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"store":{"node_id":"n1","raft":{"state":"Leader"}}}`))
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	host, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return Endpoint{Host: host, Port: port, Username: "orama", Password: "s3cret"}, &opened
}

// Bugboard 2729. The node's health checks build an admin client per call
// (ep.Admin().Status every 30s, from several components). Each client used to
// bring its own connection pool, so every check left one idle keep-alive
// connection to rqlited that nothing would reuse or close: ~10 a minute on
// stagenet, a goroutine each on both sides. Repeated checks must share one.
func TestAdminClient_periodic_status_checks_reuse_one_connection(t *testing.T) {
	ep, opened := countingRQLite(t, http.StatusOK)

	const checks = 20
	for i := 0; i < checks; i++ {
		st, err := ep.Admin().Status(context.Background())
		if err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if st.Store.Raft.State != "Leader" {
			t.Fatalf("check %d: state %q; want Leader", i, st.Store.Raft.State)
		}
	}
	if got := opened.Load(); got != 1 {
		t.Fatalf("%d status checks opened %d connections to rqlited; want 1", checks, got)
	}
}

// A failing check (rqlited answering non-2xx) must hand its connection back
// too, or a node in trouble leaks faster than a healthy one.
func TestAdminClient_failed_checks_reuse_one_connection(t *testing.T) {
	ep, opened := countingRQLite(t, http.StatusServiceUnavailable)

	for i := 0; i < 5; i++ {
		if _, err := NewAdminClient(ep.BaseURL(), ep.Username, ep.Password).Status(context.Background()); err == nil {
			t.Fatalf("check %d: want an error for a 503", i)
		}
	}
	if got := opened.Load(); got != 1 {
		t.Fatalf("5 failed checks opened %d connections; want 1", got)
	}
}

// Admin clients for different rqlite nodes are pooled per host: one each.
func TestAdminClient_distinct_nodes_get_their_own_connection(t *testing.T) {
	epA, openedA := countingRQLite(t, http.StatusOK)
	epB, openedB := countingRQLite(t, http.StatusOK)

	for i := 0; i < 5; i++ {
		for _, ep := range []Endpoint{epA, epB} {
			if _, err := ep.Admin().Status(context.Background()); err != nil {
				t.Fatalf("status %s: %v", ep, err)
			}
		}
	}
	if a, b := openedA.Load(), openedB.Load(); a != 1 || b != 1 {
		t.Fatalf("connections opened: A=%d B=%d; want 1 each", a, b)
	}
}
