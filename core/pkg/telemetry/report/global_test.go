package report

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func stubGlobalUnits(t *testing.T, states map[string]string) {
	t.Helper()
	old := globalSystemctl
	t.Cleanup(func() { globalSystemctl = old })
	globalSystemctl = func(_ context.Context, args ...string) (string, error) {
		name := args[len(args)-1]
		state, ok := states[name]
		if !ok {
			return chainUnitNotFound, nil
		}
		if args[0] == "show" {
			return "loaded", nil
		}
		return state, nil
	}
}

func TestCollectGlobal_absentIsNil(t *testing.T) {
	stubGlobalUnits(t, nil)
	if g := collectGlobal(); g != nil {
		t.Fatalf("a node without global units reported %+v", g)
	}
}

func TestCollectGlobal_failedUnitAndOverCapacity(t *testing.T) {
	stubGlobalUnits(t, map[string]string{constants.GlobalIPFSUnit: "active"})
	oldPost := globalIPFSPost
	oldToken := publicKuboTokenPath
	t.Cleanup(func() {
		globalIPFSPost = oldPost
		publicKuboTokenPath = oldToken
	})
	dir := t.TempDir()
	publicKuboTokenPath = filepath.Join(dir, "api-token")
	if err := os.WriteFile(publicKuboTokenPath, []byte("abc123\n"), 0600); err != nil {
		t.Fatal(err)
	}
	globalIPFSPost = func(_ context.Context, url, token string) ([]byte, error) {
		if token != "abc123" {
			t.Errorf("token = %q", token)
		}
		if url == "" {
			t.Fatal("empty url")
		}
		return []byte(`{"RepoSize":11,"StorageMax":10}`), nil
	}
	g := collectGlobal()
	if g == nil || g.PublicIPFS == nil || g.PublicIPFS.Error != "" {
		t.Fatalf("got %+v", g)
	}
	if g.PublicIPFS.RepoBytes != 11 || g.PublicIPFS.StorageMaxBytes != 10 {
		t.Fatalf("repo %+v", g.PublicIPFS)
	}
	if len(g.Units) != 1 || g.Units[0].State != "active" {
		t.Fatalf("units %+v", g.Units)
	}
}

func TestCollectGlobal_providerMonitor(t *testing.T) {
	stubGlobalUnits(t, map[string]string{constants.GlobalProviderUnit: "active"})
	old := providerMonitorPath
	t.Cleanup(func() { providerMonitorPath = old })
	dir := t.TempDir()
	providerMonitorPath = filepath.Join(dir, "monitor.json")
	body := []byte(`{"hot_key_balance_norama":0,"proof_misses":2,"disk_bytes":5,"storage_max_bytes":4,"held_slots":7,"pending_slots":1}`)
	if err := os.WriteFile(providerMonitorPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	g := collectGlobal()
	if g == nil || g.Provider == nil || g.Provider.Error != "" {
		t.Fatalf("got %+v", g)
	}
	if g.Provider.HotKeyBalanceNorama == nil || *g.Provider.HotKeyBalanceNorama != 0 {
		t.Fatalf("balance %+v", g.Provider)
	}
	if g.Provider.ProofMisses == nil || *g.Provider.ProofMisses != 2 {
		t.Fatalf("misses %+v", g.Provider)
	}
	if g.Provider.HeldSlots == nil || *g.Provider.HeldSlots != 7 || g.Provider.PendingSlots == nil || *g.Provider.PendingSlots != 1 {
		t.Fatalf("deal slots %+v", g.Provider)
	}
}

func TestParseMonitor_rejectsANegativeSlotCount(t *testing.T) {
	for _, body := range []string{`{"held_slots":-1}`, `{"pending_slots":-1}`} {
		if _, err := parseMonitor([]byte(body)); err == nil {
			t.Fatalf("%s was accepted", body)
		}
	}
}

func TestParseMonitor_slotCountsAreOptional(t *testing.T) {
	mon, err := parseMonitor([]byte(`{"proof_misses":0}`))
	if err != nil {
		t.Fatal(err)
	}
	if mon.HeldSlots != nil || mon.PendingSlots != nil {
		t.Fatalf("absent slot counts read as %+v", mon)
	}
}

func TestParseMonitor_rejectsANegativeBalance(t *testing.T) {
	if _, err := parseMonitor([]byte(`{"hot_key_balance_norama":-1}`)); err == nil {
		t.Fatal("a negative balance was accepted")
	}
}

func TestParseMonitor_unknownFieldIsRejected(t *testing.T) {
	if _, err := parseMonitor([]byte(`{"hot_key":1}`)); err == nil {
		t.Fatal("an unknown field was accepted")
	}
}

func TestGlobalIPFSAPI_isLoopbackWithoutTheNamespaceLayout(t *testing.T) {
	// The test machine has no orama-global-netns unit.
	if got := globalIPFSAPI(); got != constants.LocalGlobalIPFSAPIURL() {
		t.Errorf("globalIPFSAPI() = %q, want %q", got, constants.LocalGlobalIPFSAPIURL())
	}
	if constants.ColocatedGlobalIPFSAPIURL() != "http://198.18.0.2:31011" {
		t.Errorf("ColocatedGlobalIPFSAPIURL = %q", constants.ColocatedGlobalIPFSAPIURL())
	}
}
