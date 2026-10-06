package upgrade

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/install/installers"
)

// fakeUnits records the stops.
type fakeUnits struct {
	present   map[string]bool
	namespace []string
	deploys   []string
	stopped   []string
}

func (f *fakeUnits) stop(unit string) error {
	f.stopped = append(f.stopped, unit)
	return nil
}
func (f *fakeUnits) unitFileExists(unit string) (bool, error) { return f.present[unit], nil }
func (f *fakeUnits) loadedNamespaceUnits() ([]string, error)  { return f.namespace, nil }
func (f *fakeUnits) legacyDeploymentUnits() ([]string, error) { return f.deploys, nil }

// liveClusterNode is an oramaDir whose node.yaml points at a live index rqlite
// that answers /nodes and /status for a cluster of peer-id members, and holds
// raft state.
func liveClusterNode(t *testing.T) (oramaDir string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/nodes":
			_, _ = w.Write([]byte(`{"12D3KooWMtjibhBBfgbhN9Vio4jTFDeD1NLZCSxG98B6nTsoscRy":{"addr":"10.0.0.1:10101","voter":true,"reachable":true}}`))
		case "/status":
			_, _ = w.Write([]byte(`{"store":{"node_id":"12D3KooWMtjibhBBfgbhN9Vio4jTFDeD1NLZCSxG98B6nTsoscRy","nodes":[{"id":"12D3KooWMtjibhBBfgbhN9Vio4jTFDeD1NLZCSxG98B6nTsoscRy","addr":"10.0.0.1:10101"}]}}`))
		}
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	oramaDir = filepath.Join(t.TempDir(), ".orama")
	writeTestFile(t, filepath.Join(oramaDir, "configs", "node.yaml"),
		"database:\n  rqlite_port: "+u.Port()+"\n  rqlite_username: \"u\"\n  rqlite_password: \"p\"\n"+
			"discovery:\n  http_adv_address: \""+u.Host+"\"\n")
	writeTestFile(t, filepath.Join(oramaDir, "data", "rqlite", "raft.db"), "raft")
	if err := os.MkdirAll(filepath.Join(oramaDir, "data", "rqlite", "raft"), 0o755); err != nil {
		t.Fatal(err)
	}
	return oramaDir
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The stop used to capture /nodes and write a recovery peers.json from it,
// with each member's raft id as its address (ids are peer ids) and the
// non-voters left out: every upgraded node restarted into a configuration of
// addresses that do not exist. The stop stops, and writes nothing.
func TestStopServices_writesNoPeersJSON(t *testing.T) {
	drainPause = 0
	oramaDir := liveClusterNode(t)
	units := &fakeUnits{present: map[string]bool{supervisorUnit: true}}
	o := &Orchestrator{oramaDir: oramaDir, flags: &Flags{}, units: units}

	if err := o.stopServices(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(oramaDir, "data", "rqlite", "raft", "peers.json"),
		filepath.Join(oramaDir, "cluster-state.json"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("the stop wrote %s", p)
		}
	}
}

// The supervisor first, so it cannot start again what is being stopped; then
// every loaded namespace unit; then 0.122.x's deployment units, which would
// hold their ports against the templates that replace them; then the host
// daemons older installs wrote.
func TestStopServices_order(t *testing.T) {
	drainPause = 0
	units := &fakeUnits{
		present:   map[string]bool{supervisorUnit: true, installers.LegacyHostUnits[0]: true},
		namespace: []string{"orama-namespace-rqlite@index.service", "orama-namespace-gateway@acme.service"},
		deploys:   []string{"orama-deploy-acme-web.service"},
	}
	o := &Orchestrator{oramaDir: t.TempDir(), flags: &Flags{}, units: units}
	if err := o.stopServices(); err != nil {
		t.Fatal(err)
	}
	want := []string{supervisorUnit, "orama-namespace-rqlite@index.service", "orama-namespace-gateway@acme.service",
		"orama-deploy-acme-web.service", installers.LegacyHostUnits[0]}
	if !reflect.DeepEqual(units.stopped, want) {
		t.Errorf("stopped %v, want %v", units.stopped, want)
	}
}

// Every loaded namespace unit, in any state: a failed unit's line starts with
// the unit name under --plain, and a unit between two crash-loop restarts is
// not "running" but would start again mid-upgrade.
func TestParseUnitList(t *testing.T) {
	out := "orama-namespace-rqlite@index.service loaded active running RQLite\n" +
		"orama-namespace-gateway@acme.service loaded activating auto-restart Gateway\n" +
		"orama-namespace-olric@acme.service loaded failed failed Olric\n\n"
	want := []string{"orama-namespace-rqlite@index.service", "orama-namespace-gateway@acme.service", "orama-namespace-olric@acme.service"}
	if got := parseUnitList(out); !reflect.DeepEqual(got, want) {
		t.Errorf("parseUnitList = %v, want %v", got, want)
	}
}

// orama-gateway.service was never written by a release an upgrade starts
// from; the list is the host units install knows as legacy.
func TestStopServices_stopsNoUnitNoReleaseWrote(t *testing.T) {
	drainPause = 0
	units := &fakeUnits{present: map[string]bool{"orama-gateway.service": true}}
	o := &Orchestrator{oramaDir: t.TempDir(), flags: &Flags{}, units: units}
	if err := o.stopServices(); err != nil {
		t.Fatal(err)
	}
	if len(units.stopped) != 0 {
		t.Errorf("stopped %v", units.stopped)
	}
}
