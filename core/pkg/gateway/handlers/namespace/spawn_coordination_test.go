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

const coordTestSecret = "ab"

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
	return NewSpawnHandler(nil, path, zap.NewNop()), key
}

func spawnRequestWith(t *testing.T, key []byte, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/namespace/spawn", bytes.NewReader([]byte(body)))
	r.RemoteAddr = "10.0.0.5:40000"
	if err := auth.SignCoordination(key, r, time.Now()); err != nil {
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

// A node still on the previous build stamps v1 only. That is accepted for a
// request that only starts something, and refused for anything that removes.
func TestSpawnHandler_v1StampOnlyForNonDestructiveActions(t *testing.T) {
	h, key := coordHandler(t)
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"action":"teardown-namespace","namespace":"index","node_id":"n1"}`, http.StatusUnauthorized},
		{`{"action":"stop-rqlite","namespace":"mine","node_id":"n1"}`, http.StatusUnauthorized},
		{`{"action":"delete-cluster-state","namespace":"mine","node_id":"n1"}`, http.StatusUnauthorized},
		{`{"action":"spawn-olric","namespace":"mine","node_id":"n1"}`, http.StatusBadRequest},
	} {
		r := spawnRequestWith(t, key, tc.body)
		r.Header.Del(auth.CoordinationMACV2Header)
		r.Header.Del(auth.CoordinationNonceHeader)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("v1 %s: status %d, want %d: %s", tc.body, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestRequiresBodyBoundMAC(t *testing.T) {
	for action, want := range map[string]bool{
		"teardown-namespace": true, "teardown-sfu": true, "teardown-turn": true,
		"stop-rqlite": true, "stop-olric": true, "stop-gateway": true, "stop-sfu": true, "stop-turn": true,
		"delete-cluster-state": true, "unknown-action": true, "": true,
		"spawn-olric": false, "spawn-gateway": false, "restart-gateway": false,
		"spawn-sfu": false, "save-cluster-state": false, "spawn-rqlite": false,
	} {
		if got := requiresBodyBoundMAC(&SpawnRequest{Action: action}); got != want {
			t.Errorf("%q: %v, want %v", action, got, want)
		}
	}
	if !requiresBodyBoundMAC(&SpawnRequest{Action: "spawn-rqlite", RQLiteFreshStart: true}) {
		t.Error("a fresh-start spawn-rqlite accepted under v1")
	}
}
