package gateway

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// peerListDB answers the replica queries a gateway makes: this node's replica
// port, and the list of the deployment's active replicas. listed counts how
// often the list was asked for, which a request about to be forwarded to a
// replica does first.
type peerListDB struct {
	rqlite.Client
	port   int
	listed atomic.Int32
}

func (d *peerListDB) Query(_ context.Context, dest any, query string, _ ...any) error {
	rows := reflect.ValueOf(dest).Elem()
	row := reflect.New(rows.Type().Elem()).Elem()
	switch {
	case strings.Contains(query, "SELECT port FROM deployment_replicas"):
		row.FieldByName("Port").SetInt(int64(d.port))
	case strings.Contains(query, "SELECT node_id FROM deployment_replicas"):
		d.listed.Add(1)
		row.FieldByName("NodeID").SetString("peer-c")
	default:
		return nil
	}
	rows.Set(reflect.Append(rows, row))
	return nil
}

// deadLocalPort is a port nothing listens on: the deployed app is down.
func deadLocalPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// A request another node forwarded here, for an app that is down here, was sent
// on to the other replicas; with the app down on two replicas they handed it
// back and forth. It is answered with the unmarked 503 the forwarder counts
// against this node, and its list of replicas is not even read.
func TestProxyToDynamicDeployment_aForwardedRequestIsNotForwardedAgain(t *testing.T) {
	cases := map[string]struct {
		home string
		ws   bool
	}{
		"replica, http":       {home: "peer-home"},
		"replica, websocket":  {home: "peer-home", ws: true},
		"home node, http":     {home: "peer-replica"},
		"home node websocket": {home: "peer-replica", ws: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			db := &peerListDB{port: deadLocalPort(t)}
			g := replicaGateway(t, db)
			d := &deployments.Deployment{ID: "dep-shop", Namespace: "acme", Name: "shop", HomeNodeID: tc.home, Port: db.port}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("X-Orama-Proxy-Node", "peer-a")
			if tc.ws {
				r.Header.Set("Connection", "Upgrade")
				r.Header.Set("Upgrade", "websocket")
			}
			rec := httptest.NewRecorder()
			g.proxyToDynamicDeployment(rec, r, d)

			// A recorder cannot be hijacked, so a websocket answers 500 first.
			if !tc.ws && rec.Code != http.StatusServiceUnavailable {
				t.Errorf("got %d, want 503", rec.Code)
			}
			if rec.Header().Get(httputil.HeaderTenantOrigin) != "" {
				t.Error("the 503 carries the tenant-origin marker, so the forwarder would take it for the app's answer")
			}
			if n := db.listed.Load(); n != 0 {
				t.Errorf("the replica list was read %d times: the request was about to be forwarded again", n)
			}
		})
	}
}

// The other side of the rule: a request from a client, not from a node, still
// goes to the other replicas when the local app is down.
func TestProxyToDynamicDeployment_aClientsRequestStillTriesTheOtherReplicas(t *testing.T) {
	db := &peerListDB{port: deadLocalPort(t)}
	g := replicaGateway(t, db)
	d := &deployments.Deployment{ID: "dep-shop", Namespace: "acme", Name: "shop", HomeNodeID: "peer-home", Port: db.port}
	rec := httptest.NewRecorder()
	g.proxyToDynamicDeployment(rec, httptest.NewRequest(http.MethodGet, "/", nil), d)

	if n := db.listed.Load(); n != 1 {
		t.Fatalf("the replica list was read %d times, want 1", n)
	}
}

// blockingPeer answers nothing until the request is cancelled, and reports
// when it was.
func blockingPeer(t *testing.T) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	gone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			close(gone)
		case <-time.After(10 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	return srv, gone
}

func cancelledSoon(r *http.Request) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithCancel(r.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	return r.WithContext(ctx), cancel
}

// hopTimeLimit is how long a hop may outlive the client that left. The peers
// of these tests hold their answer for 10s, and a hop's own timeout is 5s or
// more.
const hopTimeLimit = 2 * time.Second

func returnsOnceTheClientLeft(t *testing.T, gone <-chan struct{}, what string, hop func()) {
	t.Helper()
	start := time.Now()
	hop()
	if elapsed := time.Since(start); elapsed > hopTimeLimit {
		t.Errorf("%s: the hop ran %v after the client left", what, elapsed)
	}
	select {
	case <-gone:
	case <-time.After(hopTimeLimit):
		t.Errorf("%s: the next gateway never saw the request cancelled", what)
	}
}

// Each hop of a forwarded request used to have no context, so a client that
// left did not cancel it: the chain of gateways it had been handed along went
// on waiting for an app nobody was waiting for.
func TestForwardToHomeNode_aClientThatLeavesCancelsTheHop(t *testing.T) {
	peer, gone := blockingPeer(t)
	g := deploymentGateway(t)
	r, cancel := cancelledSoon(httptest.NewRequest(http.MethodGet, "/", nil))
	defer cancel()

	returnsOnceTheClientLeft(t, gone, "home node", func() {
		if g.forwardToHomeNode(httptest.NewRecorder(), r, appDeployment("dep-shop", "shop"), "127.0.0.1", hostPort(peer), time.Minute) {
			t.Error("a request nobody answered was reported served")
		}
	})
}

func TestForwardToReplica_aClientThatLeavesCancelsTheHop(t *testing.T) {
	peer, gone := blockingPeer(t)
	g := deploymentGateway(t)
	r, cancel := cancelledSoon(httptest.NewRequest(http.MethodGet, "/", nil))
	defer cancel()

	returnsOnceTheClientLeft(t, gone, "replica", func() {
		if g.forwardToReplica(httptest.NewRecorder(), r, appDeployment("dep-shop", "shop"), "127.0.0.1", hostPort(peer)) {
			t.Error("a request nobody answered was reported served")
		}
	})
}

func TestProxyToDynamicDeployment_aClientThatLeavesCancelsTheLocalHop(t *testing.T) {
	peer, gone := blockingPeer(t)
	port, err := strconv.Atoi(strings.Split(hostPort(peer), ":")[1])
	if err != nil {
		t.Fatal(err)
	}
	g := deploymentGateway(t)
	g.nodePeerID = ""
	r, cancel := cancelledSoon(httptest.NewRequest(http.MethodGet, "/", nil))
	defer cancel()

	returnsOnceTheClientLeft(t, gone, "local app", func() {
		g.proxyToDynamicDeployment(httptest.NewRecorder(), r, &deployments.Deployment{ID: "dep-shop", Namespace: "acme", Name: "shop", Port: port})
	})
}

// When the client leaves, the replicas that are left are not tried one by one.
func TestProxyCrossNodeWithReplicas_stopsWhenTheClientLeaves(t *testing.T) {
	db := &peerListDB{}
	g := replicaGateway(t, db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

	if g.proxyCrossNodeWithReplicas(httptest.NewRecorder(), r, &deployments.Deployment{ID: "dep-shop", Name: "shop"}) {
		t.Fatal("a request whose client left was served")
	}
}

// A replica list that is out of date on the forwarding node ends every request
// it sends here the same way. The line is a warning, once a minute per
// deployment, and says how many were held back.
func TestProxyToDynamicDeployment_staleReplicaListIsWarnedOncePerInterval(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	g := replicaGateway(t, replicaDB{port: 0})
	g.logger = &logging.ColoredLogger{Logger: zap.New(core)}
	d := &deployments.Deployment{ID: "dep-shop", Namespace: "acme", Name: "shop", HomeNodeID: "peer-home", Port: 1}

	for i := 0; i < 5; i++ {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Orama-Proxy-Node", "peer-a")
		rec := httptest.NewRecorder()
		g.proxyToDynamicDeployment(rec, r, d)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("got %d, want 503", rec.Code)
		}
	}
	entries := logs.FilterMessage("Forwarded request for a deployment this node has no active replica of").All()
	if len(entries) != 1 {
		t.Fatalf("%d lines for 5 requests, want 1", len(entries))
	}
	if entries[0].Level != zapcore.WarnLevel {
		t.Errorf("level %v, want warn", entries[0].Level)
	}
	if logs.FilterLevelExact(zapcore.ErrorLevel).Len() != 0 {
		t.Error("a stale replica list was logged at error level")
	}

	g.staleReplicaLog = logThrottle{}
	other := &deployments.Deployment{ID: "dep-blog", Namespace: "acme", Name: "blog", HomeNodeID: "peer-home", Port: 1}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Orama-Proxy-Node", "peer-a")
	g.proxyToDynamicDeployment(httptest.NewRecorder(), r, other)
	g.proxyToDynamicDeployment(httptest.NewRecorder(), r, d)
	if got := logs.FilterMessage("Forwarded request for a deployment this node has no active replica of").Len(); got != 3 {
		t.Errorf("%d lines after a second deployment, want 3: one deployment must not hide another", got)
	}
}
