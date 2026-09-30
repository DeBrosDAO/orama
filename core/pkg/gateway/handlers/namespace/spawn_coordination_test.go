package namespace

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/auth"
)

const (
	coordTestSecret = "ab"
	coordTestNodeID = "n1"
)

func coordHandler(t *testing.T) (*SpawnHandler, []byte) {
	t.Helper()
	secret := strings.Repeat(coordTestSecret, 32)
	path := filepath.Join(t.TempDir(), "cluster-secret")
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := auth.CoordinationKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	return NewSpawnHandler(nil, path, coordTestNodeID, zap.NewNop()), key
}

func spawnRequestWith(t *testing.T, key []byte, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/namespace/spawn", bytes.NewReader([]byte(body)))
	r.RemoteAddr = "10.0.0.5:40000"
	if err := auth.SignCoordination(key, r, time.Now(), coordTestNodeID); err != nil {
		t.Fatal(err)
	}
	return r
}

// A stamp captured on a stop-rqlite request, replayed with the body of a
// teardown-namespace: the v2 MAC covers the body, and v1 is no fallback.
func TestSpawnHandler_refusesASwappedBody(t *testing.T) {
	h, key := coordHandler(t)
	r := spawnRequestWith(t, key, `{"action":"stop-rqlite","namespace":"mine","node_id":"n1"}`)
	r.Body = http.NoBody
	r.Body = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(
		`{"action":"teardown-namespace","namespace":"victim","node_id":"n1"}`)).Body
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: %s", w.Code, w.Body.String())
	}
}

func TestSpawnHandler_refusesAReplayedRequest(t *testing.T) {
	h, key := coordHandler(t)
	body := `{"action":"teardown-namespace","namespace":"index","node_id":"n1"}`
	first := spawnRequestWith(t, key, body)
	replay := httptest.NewRequest(http.MethodPost, "/v1/internal/namespace/spawn", strings.NewReader(body))
	replay.RemoteAddr = first.RemoteAddr
	replay.Header = first.Header.Clone()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, first)
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("the first request was refused: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, replay)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("replay status %d, want 401", w.Code)
	}
}

// Every spawn action carries its parameters in the body (DSNs, peer addresses,
// TURN and encryption secrets), which the v1 stamp does not cover: a request
// stamped only with v1 is refused for every action, so a captured stamp cannot
// be replayed with a swapped body.
func TestSpawnHandler_v1StampRefusedForEveryAction(t *testing.T) {
	h, key := coordHandler(t)
	for _, action := range []string{
		"teardown-namespace", "stop-rqlite", "delete-cluster-state",
		"spawn-olric", "spawn-gateway", "restart-gateway", "spawn-sfu", "save-cluster-state", "spawn-rqlite",
	} {
		r := spawnRequestWith(t, key, `{"action":"`+action+`","namespace":"mine","node_id":"n1"}`)
		r.Header.Del(auth.CoordinationMACV2Header)
		r.Header.Del(auth.CoordinationNonceHeader)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("v1 %s: status %d, want 401: %s", action, w.Code, w.Body.String())
		}
	}
}

// A v2 teardown captured on its way to node A must not act on node B: the node
// id in the body has to be this node's, and the MAC covers the Host it dialled.
func TestSpawnHandler_refusesARequestForAnotherNode(t *testing.T) {
	h, key := coordHandler(t)
	r := spawnRequestWith(t, key, `{"action":"teardown-namespace","namespace":"mine","node_id":"other-node"}`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", w.Code, w.Body.String())
	}
}

// A stamp captured on its way to node A, replayed at node B with A's Host and
// the body unchanged (so its node_id still says A), is refused: the stamp is
// signed for A's peer id, which B checks against its own and not the Host.
func TestSpawnHandler_refusesAStampSignedForAnotherNode(t *testing.T) {
	h, key := coordHandler(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/namespace/spawn", strings.NewReader(
		`{"action":"teardown-namespace","namespace":"mine","node_id":"n1"}`))
	r.RemoteAddr = "10.0.0.5:40000"
	r.Host = "10.0.0.1:6001"
	if err := auth.SignCoordination(key, r, time.Now(), "node-A"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: %s", w.Code, w.Body.String())
	}
}

// A replica teardown fan-out is stamped for the replica's node, with the
// cluster secret read from disk; a missing secret is an error, not an
// unauthenticated call.
func TestDeleteHandler_replicaTeardownIsStampedForItsNode(t *testing.T) {
	secret := strings.Repeat(coordTestSecret, 32)
	path := filepath.Join(t.TempDir(), "cluster-secret")
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := auth.CoordinationKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	h := &DeleteHandler{}
	h.SetClusterSecretPath(path)
	stamped := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/internal/deployments/replica/teardown", strings.NewReader(`{}`))
		if err := h.signReplicaTeardown(r, "node-2"); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if auth.VerifyCoordinationV2(key, stamped(), time.Now(), "node-3") {
		t.Fatal("a teardown stamped for node-2 verified at node-3")
	}
	if !auth.VerifyCoordinationV2(key, stamped(), time.Now(), "node-2") {
		t.Fatal("a teardown stamped for node-2 did not verify at node-2")
	}

	missing := &DeleteHandler{}
	missing.SetClusterSecretPath(filepath.Join(t.TempDir(), "absent"))
	if err := missing.signReplicaTeardown(httptest.NewRequest(http.MethodPost, "/x", nil), "node-2"); err == nil {
		t.Fatal("signed without a cluster secret")
	}
}
