package namespace

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	namespacepkg "github.com/DeBrosOfficial/network/pkg/namespace"
)

// A teardown owed for a deleted namespace reaches a node that holds the name
// again for a new cluster: the node refuses it (409) and keeps the namespace.
func TestSpawnHandler_refusesATeardownForAnotherCluster(t *testing.T) {
	h, key := coordHandler(t)
	base := t.TempDir()
	dir := filepath.Join(base, "acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "cluster-state.json")
	if err := os.WriteFile(state, []byte(`{"cluster_id":"c-new"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.systemdSpawner = namespacepkg.NewSystemdSpawner(base, "", zap.NewNop())

	for _, action := range []string{"teardown-namespace", "teardown-sfu", "teardown-turn"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, spawnRequestWith(t, key,
			`{"action":"`+action+`","namespace":"acme","node_id":"n1","cluster_id":"c-old","purge_data":true}`))
		if w.Code != http.StatusConflict {
			t.Fatalf("%s: status %d, want 409: %s", action, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "another cluster") {
			t.Errorf("%s: body %q does not say why", action, w.Body.String())
		}
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("the new namespace's state was removed: %v", err)
	}
}
