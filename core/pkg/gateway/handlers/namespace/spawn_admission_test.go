package namespace

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	namespacepkg "github.com/DeBrosOfficial/network/pkg/namespace"
)

type fakeAdmitter struct {
	err      error
	admitted []string
	released int
}

func (f *fakeAdmitter) AdmitSpawn(_ context.Context, namespace string) (func(), error) {
	f.admitted = append(f.admitted, namespace)
	if f.err != nil {
		return nil, f.err
	}
	return func() { f.released++ }, nil
}

const startActionBody = `{"action":"%s","namespace":"acme","node_id":"n1","olric_bind_addr":"10.0.0.1","rqlite_http_adv_addr":"10.0.0.2:10000","rqlite_raft_adv_addr":"10.0.0.2:10001"}`

var startActions = []string{"spawn-rqlite", "spawn-olric", "spawn-gateway", "restart-gateway", "spawn-sfu"}

func admissionHandler(t *testing.T, a SpawnAdmitter) (*SpawnHandler, []byte, string) {
	t.Helper()
	h, key := coordHandler(t)
	base := t.TempDir()
	h.systemdSpawner = namespacepkg.NewSystemdSpawner(base, "", zap.NewNop())
	h.SetSpawnAdmitter(a)
	return h, key, base
}

// A remote provisioner of a namespace deleted mid-provision is told 409 and
// nothing is written for the namespace on this node.
func TestSpawnHandler_refusesToStartUnitsOfANamespaceBeingDeleted(t *testing.T) {
	refusal := fmt.Errorf("spawn refused for namespace acme: %w", namespacepkg.ErrNamespaceBeingDeleted)
	for _, action := range startActions {
		adm := &fakeAdmitter{err: refusal}
		h, key, base := admissionHandler(t, adm)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, spawnRequestWith(t, key, fmt.Sprintf(startActionBody, action)))
		if w.Code != http.StatusConflict {
			t.Errorf("%s: status %d, want 409: %s", action, w.Code, w.Body.String())
		}
		if len(adm.admitted) != 1 || adm.admitted[0] != "acme" {
			t.Errorf("%s: admission asked for %v, want [acme]", action, adm.admitted)
		}
		if _, err := os.Stat(filepath.Join(base, "acme")); err == nil {
			t.Errorf("%s: the namespace's directory was created on a refused spawn", action)
		}
	}
}

// A lock that stays held past its wait is a node that cannot take the request
// now: 503, not a spawn without the lock.
func TestSpawnHandler_aNamespaceLockThatStaysHeldIsUnavailable(t *testing.T) {
	adm := &fakeAdmitter{err: fmt.Errorf("namespace acme is locked: %w", context.DeadlineExceeded)}
	h, key, _ := admissionHandler(t, adm)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, spawnRequestWith(t, key, fmt.Sprintf(startActionBody, "spawn-rqlite")))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503: %s", w.Code, w.Body.String())
	}
}

func TestSpawnHandler_anAdmissionFailureIsNotASpawn(t *testing.T) {
	adm := &fakeAdmitter{err: fmt.Errorf("read its cluster from the registry: no leader")}
	h, key, _ := admissionHandler(t, adm)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, spawnRequestWith(t, key, fmt.Sprintf(startActionBody, "spawn-gateway")))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500: %s", w.Code, w.Body.String())
	}
}

// A node wired without admission cannot tell, and says so instead of starting.
func TestSpawnHandler_withoutAnAdmitterAStartIsRefused(t *testing.T) {
	h, key, base := admissionHandler(t, nil)
	h.admitter = nil
	w := httptest.NewRecorder()
	h.ServeHTTP(w, spawnRequestWith(t, key, fmt.Sprintf(startActionBody, "spawn-sfu")))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(base, "acme")); err == nil {
		t.Fatal("a unit was prepared without admission")
	}
}

// An admitted start runs under the admission and lets it go when done, whether
// or not the spawn succeeded (here it fails: no systemd in the test).
func TestSpawnHandler_anAdmittedStartReleasesTheNamespace(t *testing.T) {
	adm := &fakeAdmitter{}
	h, key, _ := admissionHandler(t, adm)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, spawnRequestWith(t, key, fmt.Sprintf(startActionBody, "spawn-sfu")))
	if len(adm.admitted) != 1 {
		t.Fatalf("admission asked %d times, want 1", len(adm.admitted))
	}
	if adm.released != 1 {
		t.Fatalf("admission released %d times, want 1 (status %d: %s)", adm.released, w.Code, w.Body.String())
	}
}

// Stops and teardowns are not starts: a delete's own stop must not be refused
// because the namespace is being deleted.
func TestSpawnHandler_stopsAreNotAdmitted(t *testing.T) {
	adm := &fakeAdmitter{err: namespacepkg.ErrNamespaceBeingDeleted}
	h, key, _ := admissionHandler(t, adm)
	for _, action := range []string{"stop-rqlite", "stop-gateway", "teardown-namespace", "save-cluster-state"} {
		h.ServeHTTP(httptest.NewRecorder(), spawnRequestWith(t, key,
			fmt.Sprintf(`{"action":"%s","namespace":"acme","node_id":"n1"}`, action)))
	}
	if len(adm.admitted) != 0 {
		t.Fatalf("admission asked for %v on stop/teardown actions", adm.admitted)
	}
}
