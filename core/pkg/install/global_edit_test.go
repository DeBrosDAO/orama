package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/install/installers"
)

func TestSetPublicStorage_resizesKuboAndKeepsItsIdentityAndToken(t *testing.T) {
	f := installedFixture(t)
	repo := filepath.Join(f.host.StateDir, "ipfs")
	tokenBefore, err := os.ReadFile(filepath.Join(repo, installers.PublicAPITokenFile))
	if err != nil {
		t.Fatal(err)
	}

	if err := SetPublicStorage(f.host, 20_000_000_000, false); err != nil {
		t.Fatalf("SetPublicStorage: %v", err)
	}

	var cfg map[string]map[string]any
	raw, err := os.ReadFile(filepath.Join(repo, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if got, want := cfg["Datastore"]["StorageMax"], installers.PublicStorageMax(20_000_000_000); got != want {
		t.Errorf("StorageMax = %v, want %v", got, want)
	}
	if cfg["Identity"]["PeerID"] != "12D3KooWtest" {
		t.Errorf("Identity = %v: resizing must keep the repo's identity", cfg["Identity"])
	}
	tokenAfter, _ := os.ReadFile(filepath.Join(repo, installers.PublicAPITokenFile))
	if string(tokenAfter) != string(tokenBefore) {
		t.Error("the RPC token changed: the provider would lose access to Kubo")
	}
}

func TestSetPublicStorage_withoutKuboIsRefused(t *testing.T) {
	f := newGlobalFixture(t)
	if err := InstallGlobal(f.options(GlobalServiceChain), f.host); err != nil {
		t.Fatal(err)
	}

	err := SetPublicStorage(f.host, 20_000_000_000, false)

	if err == nil || !strings.Contains(err.Error(), "public Kubo is not installed") {
		t.Fatalf("err = %v, want the missing Kubo named", err)
	}
}

func relayTorrc(t *testing.T, tf *torFixture) string {
	t.Helper()
	raw, err := os.ReadFile(tf.state("tor-relay.torrc"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func installRelay(t *testing.T) *torFixture {
	t.Helper()
	tf := newTorFixture(t)
	if err := InstallGlobal(tf.relayOptions(nil), tf.host); err != nil {
		t.Fatal(err)
	}
	return tf
}

// switchRelay plans and applies the role and returns the torrc written.
func switchRelay(t *testing.T, tf *torFixture, exit bool) string {
	t.Helper()
	change, err := PlanRelayExit(tf.host, exit)
	if err != nil || !change.Changed {
		t.Fatalf("PlanRelayExit(%v) = %+v, %v; want a change", exit, change, err)
	}
	if err := change.Apply(tf.host); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return relayTorrc(t, tf)
}

func TestPlanRelayExit_switchesTheExitSectionAndKeepsTheRestOfTheTorrc(t *testing.T) {
	tf := installRelay(t)
	plain := relayTorrc(t, tf)

	exit := switchRelay(t, tf, true)
	if !strings.Contains(exit, "ExitRelay 1") || strings.Contains(exit, "ExitRelay 0") {
		t.Errorf("the torrc is not an exit's:\n%s", exit)
	}
	head := plain[:strings.Index(plain, "ExitRelay 0")]
	if !strings.HasPrefix(exit, head) {
		t.Errorf("the relay's identity lines changed with the role:\n%s", exit)
	}

	switchRelay(t, tf, false)
	if relayTorrc(t, tf) != plain {
		t.Error("switching to an exit and back did not restore the plain relay's torrc")
	}
}

func TestPlanRelayExit_alreadyInTheRoleChangesNothing(t *testing.T) {
	tf := installRelay(t)
	before := relayTorrc(t, tf)

	change, err := PlanRelayExit(tf.host, false)

	if err != nil || change.Changed {
		t.Fatalf("PlanRelayExit(false) on a plain relay = %+v, %v; want unchanged", change, err)
	}
	if err := change.Apply(tf.host); err != nil || relayTorrc(t, tf) != before {
		t.Errorf("applying no change wrote the torrc: %v", err)
	}
}

func TestPlanRelayExit_writesNothingUntilApplied(t *testing.T) {
	tf := installRelay(t)
	before := relayTorrc(t, tf)

	if _, err := PlanRelayExit(tf.host, true); err != nil {
		t.Fatal(err)
	}

	if relayTorrc(t, tf) != before {
		t.Error("planning the switch changed the torrc")
	}
}

func TestPlanRelayExit_networkThatForbidsExitsIsRefused(t *testing.T) {
	tf := installRelay(t)
	network := tf.network
	network.AllowExit = false
	body, err := network.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tf.state(constants.GlobalTorAuthoritiesFile), body, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = PlanRelayExit(tf.host, true)

	if err == nil || !strings.Contains(err.Error(), "does not allow exits") {
		t.Fatalf("err = %v, want the network's refusal", err)
	}
}

func TestPlanRelayExit_withoutARelayIsRefused(t *testing.T) {
	f := newGlobalFixture(t)
	if err := InstallGlobal(f.options(GlobalServiceChain), f.host); err != nil {
		t.Fatal(err)
	}

	_, err := PlanRelayExit(f.host, true)

	if err == nil || !strings.Contains(err.Error(), "no Tor relay") {
		t.Fatalf("err = %v, want the missing relay named", err)
	}
}

func TestRequirePublicKubo(t *testing.T) {
	f := installedFixture(t)
	if err := RequirePublicKubo(f.host); err != nil {
		t.Errorf("a node with the public Kubo was refused: %v", err)
	}
	g := newGlobalFixture(t)
	if err := InstallGlobal(g.options(GlobalServiceChain), g.host); err != nil {
		t.Fatal(err)
	}
	if err := RequirePublicKubo(g.host); err == nil || !strings.Contains(err.Error(), "public Kubo is not installed") {
		t.Errorf("err = %v, want the missing Kubo named", err)
	}
}
