package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// The repair endpoint stops a namespace's services and starts them again. It
// was reachable by anything on the WireGuard overlay that knew a constant
// printed in this repository — which is every namespace's own workloads.

// coordinationTestNode is the peer id the gateways under test are, and so the
// audience their requests are signed for.
const coordinationTestNode = "12D3KooWCoordinationTestNode"

func coordinationGateway(secret string) *Gateway {
	return &Gateway{cfg: &Config{ClusterSecret: secret, NodePeerID: coordinationTestNode}}
}

func meshRequest(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "10.0.0.7:41000"
	return r
}

func TestVerifyCoordination_acceptsASignedRequestFromTheMesh(t *testing.T) {
	g := coordinationGateway("a cluster secret")
	key, err := nodeauth.CoordinationKey("a cluster secret")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	r := meshRequest(http.MethodPost, "/v1/internal/namespace/repair?namespace=acme")
	if err := nodeauth.SignCoordination(key, r, time.Now(), coordinationTestNode); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !g.verifyCoordination(r) {
		t.Fatal("a signed request from the mesh was refused")
	}
}

func TestVerifyCoordination_refuses(t *testing.T) {
	g := coordinationGateway("a cluster secret")
	key, _ := nodeauth.CoordinationKey("a cluster secret")

	sign := func(r *http.Request) *http.Request {
		if err := nodeauth.SignCoordination(key, r, time.Now(), coordinationTestNode); err != nil {
			t.Fatalf("sign: %v", err)
		}
		return r
	}

	t.Run("the old constant, which is in this source", func(t *testing.T) {
		r := meshRequest(http.MethodPost, "/v1/internal/namespace/repair?namespace=acme")
		r.Header.Set("X-Orama-Internal-Auth", "namespace-coordination")
		if g.verifyCoordination(r) {
			t.Error("the constant still authenticates a coordination request")
		}
	})

	t.Run("a signed request from off the mesh", func(t *testing.T) {
		r := sign(httptest.NewRequest(http.MethodPost, "/v1/internal/namespace/repair", nil))
		r.RemoteAddr = "203.0.113.9:41000"
		if g.verifyCoordination(r) {
			t.Error("a request from a public address was accepted")
		}
	})

	t.Run("a gateway with no cluster secret", func(t *testing.T) {
		r := sign(meshRequest(http.MethodPost, "/v1/internal/namespace/repair"))
		if coordinationGateway("").verifyCoordination(r) {
			t.Error("a gateway with no cluster secret accepted a coordination request; " +
				"it has no way to check one, so it must refuse")
		}
	})

	t.Run("no configuration at all", func(t *testing.T) {
		r := sign(meshRequest(http.MethodPost, "/v1/internal/namespace/repair"))
		if (&Gateway{}).verifyCoordination(r) {
			t.Error("an unconfigured gateway accepted a coordination request")
		}
	})
}

// The repair handler is the one that stops and restarts a namespace's services,
// so the check has to be in the chain rather than merely written.
func TestNamespaceClusterRepairHandler_refusesAnUnsignedRequest(t *testing.T) {
	g := coordinationGateway("a cluster secret")

	w := httptest.NewRecorder()
	r := meshRequest(http.MethodPost, "/v1/internal/namespace/repair?namespace=acme")
	r.Header.Set("X-Orama-Internal-Auth", "namespace-coordination")
	g.namespaceClusterRepairHandler(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
}

// The reencrypt route carries the new root key material in its body, which the
// v1 stamp does not cover: a stripped-v2 replay with an attacker-chosen root
// must be refused before the body is read.
func TestHandleInternalReencrypt_requiresTheV2Stamp(t *testing.T) {
	g := coordinationGateway("a cluster secret")
	key, _ := nodeauth.CoordinationKey("a cluster secret")
	body := `{"root":{"current_ikm":"attacker"}}`

	r := httptest.NewRequest(http.MethodPost, "/v1/internal/secrets/reencrypt", strings.NewReader(body))
	r.RemoteAddr = "10.0.0.7:41000"
	if err := nodeauth.SignCoordination(key, r, time.Now(), coordinationTestNode); err != nil {
		t.Fatalf("sign: %v", err)
	}
	r.Header.Del(nodeauth.CoordinationMACV2Header)
	r.Header.Del(nodeauth.CoordinationNonceHeader)
	w := httptest.NewRecorder()
	g.handleInternalReencrypt(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("v1-only reencrypt: status %d, want 401: %s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/internal/secrets/reencrypt", strings.NewReader(body))
	r.RemoteAddr = "10.0.0.7:41000"
	if err := nodeauth.SignCoordination(key, r, time.Now(), coordinationTestNode); err != nil {
		t.Fatalf("sign: %v", err)
	}
	w = httptest.NewRecorder()
	g.handleInternalReencrypt(w, r)
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("a v2-stamped reencrypt was refused: %s", w.Body.String())
	}
}

func signedReencrypt(t *testing.T, secret, audience, body string) *http.Request {
	t.Helper()
	key, err := nodeauth.CoordinationKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/secrets/reencrypt", strings.NewReader(body))
	r.RemoteAddr = "10.0.0.7:41000"
	if err := nodeauth.SignCoordination(key, r, time.Now(), audience); err != nil {
		t.Fatal(err)
	}
	return r
}

// A request captured on its way to one node and replayed at another, with the
// first node's Host, must not pass: the audience is the verifier's own peer id,
// not anything the sender can set.
func TestVerifyCoordinationV2_aStampForAnotherNodeIsRefused(t *testing.T) {
	g := coordinationGateway("a cluster secret")
	r := signedReencrypt(t, "a cluster secret", "12D3KooWSomeOtherNode", `{}`)
	r.Host = "10.0.0.1:6001"
	if g.verifyCoordinationV2(r) {
		t.Fatal("a stamp signed for another node was accepted")
	}
	if !g.verifyCoordinationV2(signedReencrypt(t, "a cluster secret", coordinationTestNode, `{}`)) {
		t.Fatal("a stamp signed for this node was refused")
	}
}

func TestVerifyCoordinationV2_aGatewayWithoutANodeIdRefuses(t *testing.T) {
	g := &Gateway{cfg: &Config{ClusterSecret: "a cluster secret"}}
	if g.verifyCoordinationV2(signedReencrypt(t, "a cluster secret", coordinationTestNode, `{}`)) {
		t.Fatal("a gateway that does not know its own id accepted a v2 stamp")
	}
}

// A captured reencrypt replayed inside its window at a gateway that has not yet
// taken the newer root must not put it back on an older one.
func TestHandleInternalReencrypt_refusesAnOlderRoot(t *testing.T) {
	const secret = "a cluster secret"
	stateDir := t.TempDir()
	g := coordinationGateway(secret)
	g.cfg.StateDir = stateDir
	g.encHolder = secrets.NewHolder(secrets.Root{CurrentID: "3", CurrentIKM: "gen-3"})

	w := httptest.NewRecorder()
	g.handleInternalReencrypt(w, signedReencrypt(t, secret, coordinationTestNode,
		`{"root":{"CurrentID":"2","CurrentIKM":"gen-2"}}`))
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409 for an older root: %s", w.Code, w.Body.String())
	}
	if got := g.encHolder.Get(); got.CurrentID != "3" || got.CurrentIKM != "gen-3" {
		t.Fatalf("the gateway was rolled back to %+v", got)
	}
	if entries, _ := os.ReadDir(stateDir); len(entries) != 0 {
		t.Fatalf("the refused root was persisted: %v", entries)
	}
}

type fanoutDB struct {
	rqlite.Client
	ip     string
	port   int
	nodeID string
}

func (d fanoutDB) Query(_ context.Context, dest any, _ string, _ ...any) error {
	rows := reflect.ValueOf(dest).Elem()
	row := reflect.New(rows.Type().Elem()).Elem()
	row.FieldByName("Namespace").SetString("acme")
	row.FieldByName("NodeID").SetString(d.nodeID)
	row.FieldByName("InternalIP").SetString(d.ip)
	row.FieldByName("Port").SetInt(int64(d.port))
	rows.Set(reflect.Append(rows, row))
	return nil
}

// The fan-out stamps each call for the node that hosts the namespace gateway
// it dials, from the registry row.
func TestFanoutReencrypt_signsForTheNodeOfEachRow(t *testing.T) {
	const secret = "a cluster secret"
	key, _ := nodeauth.CoordinationKey(secret)
	var accepted int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nodeauth.VerifyCoordinationV2(key, r, time.Now(), "12D3KooWHostingNode") {
			atomic.AddInt32(&accepted, 1)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())

	g := coordinationGateway(secret)
	g.ormClient = fanoutDB{ip: u.Hostname(), port: port, nodeID: "12D3KooWHostingNode"}
	res := g.fanoutReencrypt(context.Background(), secrets.Root{CurrentID: "2", CurrentIKM: "x"})
	if atomic.LoadInt32(&accepted) != 1 {
		t.Fatalf("the fan-out call was not stamped for the node of its row: %+v", res)
	}
}
