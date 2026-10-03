package namespace

import (
	"strings"
	"testing"
)

func TestInactiveUnits_allActiveIsHealthy(t *testing.T) {
	if err := inactiveUnits(indexStorageUnits, func(string) bool { return true }); err != nil {
		t.Fatalf("every unit active: %v", err)
	}
}

func TestInactiveUnits_namesEveryUnitThatIsDown(t *testing.T) {
	down := map[string]bool{"orama-namespace-ipfs-cluster@index.service": true, "orama-namespace-ipfs-gc@index.timer": true}
	err := inactiveUnits(indexStorageUnits, func(u string) bool { return !down[u] })
	if err == nil {
		t.Fatal("the cluster peer and the GC timer are down, reported healthy")
	}
	for u := range down {
		if !strings.Contains(err.Error(), u) {
			t.Errorf("error %q does not name %s", err, u)
		}
	}
	if strings.Contains(err.Error(), "orama-namespace-ipfs@index.service") {
		t.Errorf("error %q names the daemon, which is up", err)
	}
}

func TestIndexStorageUnits_areWhatTheEnsuresStart(t *testing.T) {
	want := []string{"orama-namespace-ipfs@index.service", "orama-namespace-ipfs-cluster@index.service", "orama-namespace-ipfs-gc@index.timer"}
	if strings.Join(indexStorageUnits, " ") != strings.Join(want, " ") {
		t.Fatalf("indexStorageUnits = %v, want %v", indexStorageUnits, want)
	}
}

func TestInactiveUnits_noUnitsIsHealthy(t *testing.T) {
	if err := inactiveUnits(nil, func(string) bool { return false }); err != nil {
		t.Fatalf("nothing to check: %v", err)
	}
}

func TestEdgeUnits_caddyAloneOrBehindTheSNIRouter(t *testing.T) {
	if got := strings.Join(edgeUnits(false), " "); got != "orama-namespace-caddy@index.service" {
		t.Errorf("edgeUnits(false) = %s", got)
	}
	if got := strings.Join(edgeUnits(true), " "); got != "orama-namespace-caddy@index.service orama-namespace-sni-router@index.service" {
		t.Errorf("edgeUnits(true) = %s", got)
	}
}
