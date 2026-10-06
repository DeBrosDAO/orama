package namespace

import (
	"bytes"
	"context"
	"errors"
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

type fakeHostTURN struct {
	err         error
	ns          string
	released    string
	deadline    time.Time
	hasDeadline bool
}

func (f *fakeHostTURN) ConfirmHostTURN(ctx context.Context, namespace string) error {
	f.ns = namespace
	f.deadline, f.hasDeadline = ctx.Deadline()
	return f.err
}

func TestSpawnHandler_reconcileHostTURN(t *testing.T) {
	body := `{"action":"reconcile-host-turn","namespace":"acme","node_id":"n1"}`
	for name, tc := range map[string]struct {
		confirmer *fakeHostTURN
		want      int
	}{
		"confirmed":     {&fakeHostTURN{}, http.StatusOK},
		"not served":    {&fakeHostTURN{err: errors.New("does not serve acme")}, http.StatusInternalServerError},
		"no reconciler": {nil, http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			h, key := coordHandler(t)
			if tc.confirmer != nil {
				h.SetHostTURN(tc.confirmer)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, spawnRequestWith(t, key, body))
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.confirmer != nil && tc.confirmer.ns != "acme" {
				t.Errorf("confirmed namespace %q, want acme", tc.confirmer.ns)
			}
		})
	}
}

func (f *fakeHostTURN) ReleaseHostTURN(ctx context.Context, namespace string) error {
	f.released = namespace
	f.deadline, f.hasDeadline = ctx.Deadline()
	return f.err
}

// A background context has no deadline, so a host that never answers would hold
// the handler for good.
func TestSpawnHandler_reconcileHostTURNRunsUnderATimeout(t *testing.T) {
	f := &fakeHostTURN{}
	h, key := coordHandler(t)
	h.SetHostTURN(f)
	body := `{"action":"reconcile-host-turn","namespace":"acme","node_id":"n1","release":true}`
	w := httptest.NewRecorder()
	h.ServeHTTP(w, spawnRequestWith(t, key, body))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if f.released != "acme" || f.ns != "" {
		t.Fatalf("release=%q confirm=%q, want a release only", f.released, f.ns)
	}
	if !f.hasDeadline || time.Until(f.deadline) > hostTURNRequestTimeout {
		t.Fatalf("no bounded deadline on the reconcile (has=%v)", f.hasDeadline)
	}
}

// release:true must reach ReleaseHostTURN, and its failure must be reported; a
// request without it must never release.
func TestSpawnHandler_releaseDispatchesToReleaseHostTURN(t *testing.T) {
	f := &fakeHostTURN{err: errors.New("still serves acme")}
	h, key := coordHandler(t)
	h.SetHostTURN(f)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, spawnRequestWith(t, key, `{"action":"reconcile-host-turn","namespace":"acme","node_id":"n1","release":true}`))
	if w.Code != http.StatusInternalServerError || f.released != "acme" {
		t.Fatalf("status %d released=%q: %s", w.Code, f.released, w.Body.String())
	}

	g := &fakeHostTURN{}
	h.SetHostTURN(g)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, spawnRequestWith(t, key, `{"action":"reconcile-host-turn","namespace":"acme","node_id":"n1"}`))
	if g.released != "" || g.ns != "acme" {
		t.Fatalf("a request without release dispatched to release (released=%q confirmed=%q)", g.released, g.ns)
	}
}
