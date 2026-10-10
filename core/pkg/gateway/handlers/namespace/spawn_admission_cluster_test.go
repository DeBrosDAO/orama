package namespace

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	namespacepkg "github.com/DeBrosOfficial/network/pkg/namespace"
)

// The request's cluster_id reaches the admission, and a spawn for an earlier
// incarnation of the namespace is told 409.
func TestSpawnHandler_aSpawnForAnotherClusterIsRefused(t *testing.T) {
	adm := &fakeAdmitter{err: fmt.Errorf("spawn refused: %w", namespacepkg.ErrClusterMismatch)}
	h, key, _ := admissionHandler(t, adm)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, spawnRequestWith(t, key,
		`{"action":"spawn-olric","namespace":"acme","node_id":"n1","cluster_id":"c-old","olric_bind_addr":"10.0.0.1"}`))
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", w.Code, w.Body.String())
	}
	if len(adm.clusterIDs) != 1 || adm.clusterIDs[0] != "c-old" {
		t.Fatalf("admission asked with cluster ids %v, want [c-old]", adm.clusterIDs)
	}
}

// A request from a provisioner on the previous release carries none.
func TestSpawnHandler_aSpawnWithoutAClusterIdAsksForNone(t *testing.T) {
	adm := &fakeAdmitter{}
	h, key, _ := admissionHandler(t, adm)
	h.ServeHTTP(httptest.NewRecorder(), spawnRequestWith(t, key,
		`{"action":"spawn-sfu","namespace":"acme","node_id":"n1"}`))
	if len(adm.clusterIDs) != 1 || adm.clusterIDs[0] != "" {
		t.Fatalf("admission asked with cluster ids %q, want [\"\"]", adm.clusterIDs)
	}
}
