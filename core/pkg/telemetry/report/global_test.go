package report

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/tornet"
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

// The Tor relay's own writer and the node report agree on the file: what
// tornet.WriteMonitor writes is what collectGlobal reads as the relay's
// state, and a relay that cannot know leaves it unknown.
func TestCollectGlobal_relayMonitorWrittenByTheTorRelay(t *testing.T) {
	stubGlobalUnits(t, map[string]string{constants.GlobalTorRelayUnit: "active"})
	old := relayMonitorPath
	t.Cleanup(func() { relayMonitorPath = old })
	home := t.TempDir()
	relayMonitorPath = filepath.Join(home, constants.GlobalMonitorFile)
	if constants.GlobalMonitorFile != tornet.MonitorFile {
		t.Fatalf("the relay writes %s and the report reads %s", tornet.MonitorFile, constants.GlobalMonitorFile)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "tornet", "testdata", "consensus-microdesc.txt"))
	if err != nil {
		t.Fatal(err)
	}
	consensus, err := tornet.ParseConsensus(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "cached-microdesc-consensus"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	now := consensus.ValidAfter.Add(time.Minute)
	for fingerprint, want := range map[string]bool{consensus.Relays[0].Fingerprint: true, "00000000000000000000000000000000000000EE": false} {
		if err := os.WriteFile(filepath.Join(home, "fingerprint"), []byte("OramaRelayTest "+fingerprint+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := tornet.WriteMonitor(home, now); err != nil {
			t.Fatal(err)
		}
		g := collectGlobal()
		if g == nil || g.Relay == nil || g.Relay.Error != "" || g.Relay.InConsensus == nil || *g.Relay.InConsensus != want {
			t.Fatalf("relay %s: report %+v, want in_consensus %t", fingerprint, g, want)
		}
	}
	if _, err := tornet.WriteMonitor(home, now.Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if g := collectGlobal(); g.Relay == nil || g.Relay.Error != "" || g.Relay.InConsensus != nil {
		t.Fatalf("an expired consensus must read as unknown: %+v", g.Relay)
	}
}

// A directory authority is in the consensus as a relay is: its monitor.json,
// written in its own home by the same timer, is read as the node's relay
// state, and a relay's file is not read for it.
func TestCollectGlobal_directoryAuthorityMonitor(t *testing.T) {
	stubGlobalUnits(t, map[string]string{constants.GlobalTorDirauthUnit: "active"})
	oldDirauth, oldRelay := dirauthMonitorPath, relayMonitorPath
	t.Cleanup(func() { dirauthMonitorPath, relayMonitorPath = oldDirauth, oldRelay })
	dirauthMonitorPath = filepath.Join(t.TempDir(), constants.GlobalMonitorFile)
	relayMonitorPath = filepath.Join(t.TempDir(), constants.GlobalMonitorFile)
	if err := os.WriteFile(relayMonitorPath, []byte(`{"in_consensus":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if g := collectGlobal(); g == nil || g.Relay == nil || g.Relay.Error != "" || g.Relay.InConsensus != nil {
		t.Fatalf("an authority that wrote no file must read as unknown, not as the relay home's answer: %+v", g)
	}
	if err := os.WriteFile(dirauthMonitorPath, []byte(`{"in_consensus":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	g := collectGlobal()
	if g == nil || g.Relay == nil || g.Relay.InConsensus == nil || *g.Relay.InConsensus {
		t.Fatalf("the authority's own file says it is not in the consensus: %+v", g)
	}
	if state, ok := globalUnitState(g, constants.GlobalTorDirauthUnit); !ok || state != "active" {
		t.Errorf("units = %+v, want the authority's unit", g.Units)
	}
	if err := os.WriteFile(dirauthMonitorPath, []byte(`{"in_consensus":"yes"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if g := collectGlobal(); g.Relay == nil || g.Relay.Error == "" {
		t.Fatalf("a malformed file must be an error in the section: %+v", g.Relay)
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

// The monitor files sit in directories owned by unprivileged accounts and the
// reader is root: a link is refused, and a FIFO neither hangs the open nor
// reads as a file.
func TestReadCapped_refusesALinkAndAFIFO(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readCapped(link, 4096); err == nil {
		t.Error("a symlink was read")
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := readCapped(fifo, 4096); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a FIFO was read as a file")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a FIFO hung the reader")
	}
	if data, err := readCapped(target, 4096); err != nil || string(data) != "{}" {
		t.Errorf("a regular file: %q, %v", data, err)
	}
}
