package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// replicaDB answers the replica-port query with port, or with err.
type replicaDB struct {
	rqlite.Client
	port int
	err  error
}

func (d replicaDB) Query(_ context.Context, dest any, _ string, _ ...any) error {
	if d.err != nil {
		return d.err
	}
	rows := reflect.ValueOf(dest).Elem()
	row := reflect.New(rows.Type().Elem()).Elem()
	row.FieldByName("Port").SetInt(int64(d.port))
	rows.Set(reflect.Append(rows, row))
	return nil
}

func replicaGateway(t *testing.T, db rqlite.Client) *Gateway {
	g := deploymentGateway(t)
	g.nodePeerID = "peer-replica"
	g.replicaManager = deployments.NewReplicaManager(db, nil, nil, zap.NewNop())
	return g
}

// A replica's port is allocated on the replica's node and is not the home
// node's, which is the one the deployments row carries. A request another node
// forwarded to a replica dialed the home node's port.
func TestProxyToDynamicDeployment_aForwardedRequestToAReplicaUsesTheReplicasPort(t *testing.T) {
	var homePortHits atomic.Int32
	homePort := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { homePortHits.Add(1) }))
	defer homePort.Close()
	replica := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("replica")) }))
	defer replica.Close()

	g := replicaGateway(t, replicaDB{port: serverPort(replica)})
	d := &deployments.Deployment{ID: "dep-shop", Namespace: "acme", Name: "shop", HomeNodeID: "peer-home", Port: serverPort(homePort)}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Orama-Proxy-Node", "peer-a")
	rec := httptest.NewRecorder()
	g.proxyToDynamicDeployment(rec, r, d)

	if rec.Code != http.StatusOK || rec.Body.String() != "replica" || homePortHits.Load() != 0 {
		t.Fatalf("got %d %q with %d hits on the home node's port, want the replica's answer", rec.Code, rec.Body.String(), homePortHits.Load())
	}
	if rec.Header().Get(httputil.HeaderTenantOrigin) == "" {
		t.Error("the replica did not mark the app's answer for the forwarding node")
	}
}

// Forwarded to a node that runs no replica of it, the request is refused
// rather than sent to whatever listens on the home node's port number here.
func TestProxyToDynamicDeployment_aForwardedRequestToANonReplicaIsRefused(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer other.Close()

	for name, db := range map[string]rqlite.Client{
		"no replica row":    replicaDB{err: errors.New("no rows")},
		"a replica, port 0": replicaDB{port: 0},
	} {
		g := replicaGateway(t, db)
		d := &deployments.Deployment{ID: "dep-shop", Namespace: "acme", Name: "shop", HomeNodeID: "peer-home", Port: serverPort(other)}
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Orama-Proxy-Node", "peer-a")
		rec := httptest.NewRecorder()
		g.proxyToDynamicDeployment(rec, r, d)
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get(httputil.HeaderTenantOrigin) != "" {
			t.Errorf("%s: got %d with marker %q, want an unmarked 503", name, rec.Code, rec.Header().Get(httputil.HeaderTenantOrigin))
		}
	}
	if hits.Load() != 0 {
		t.Errorf("the home node's port was dialed %d times on a node that is not a replica", hits.Load())
	}
}
