package install

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestParseGlobalServices_ordersAndDeduplicates(t *testing.T) {
	got, err := ParseGlobalServices([]string{"repair", "indexer", "chain", " archiver", "chain"})
	if err != nil {
		t.Fatal(err)
	}
	want := []GlobalService{GlobalServiceChain, GlobalServiceArchiver, GlobalServiceIndexer, GlobalServiceRepair}
	if !slices.Equal(got, want) {
		t.Fatalf("services = %v, want %v", got, want)
	}
}

func TestParseGlobalServices_refusals(t *testing.T) {
	cases := map[string][]string{
		"empty":                 nil,
		"unknown":               {"chain", "bogus"},
		"exit is not a service": {"chain", "exit"},
		"dirauth and relay":     {"dirauth", "relay"},
		"no chain":              {"provider"},
		"provider + repair":     {"chain", "ipfs", "provider", "repair"},
		"provider without ipfs": {"chain", "provider"},
	}
	for name, in := range cases {
		if _, err := ParseGlobalServices(in); err == nil {
			t.Errorf("%s: %v was accepted", name, in)
		}
	}
}

func TestParseGlobalServices_ipfsStartsAfterTheChainAndBeforeTheProvider(t *testing.T) {
	got, err := ParseGlobalServices([]string{"provider", "ipfs", "chain", "indexer"})
	if err != nil {
		t.Fatal(err)
	}
	want := []GlobalService{GlobalServiceChain, GlobalServiceIPFS, GlobalServiceProvider, GlobalServiceIndexer}
	if !slices.Equal(got, want) {
		t.Fatalf("services = %v, want %v", got, want)
	}
	if only, err := ParseGlobalServices([]string{"chain", "ipfs"}); err != nil || len(only) != 2 {
		t.Fatalf("the public Kubo alone beside the chain was refused: %v %v", only, err)
	}
}

func TestGlobalServiceCompanions(t *testing.T) {
	want := map[GlobalService][]string{
		GlobalServiceIPFS:    {"orama-global-ipfs-gc.timer"},
		GlobalServiceDirauth: {constants.GlobalTorArchiveTimer, constants.GlobalTorMonitorTimer},
		GlobalServiceRelay:   {constants.GlobalTorMonitorTimer},
		GlobalServiceOnion:   {constants.GlobalTxGateUnit},
	}
	for _, s := range GlobalServiceOrder {
		if got := GlobalServiceCompanions(s); !slices.Equal(got, want[s]) {
			t.Errorf("%s companions = %v, want %v", s, got, want[s])
		}
	}
}

func TestParseGlobalServices_torRolesNeedNoChain(t *testing.T) {
	for _, in := range [][]string{{"relay"}, {"dirauth"}, {"chain", "relay"}, {"chain", "onion", "relay"}, {"onion"}} {
		if _, err := ParseGlobalServices(in); err != nil {
			t.Errorf("%v refused: %v", in, err)
		}
	}
	got, err := ParseGlobalServices([]string{"onion", "relay", "chain", "indexer"})
	if err != nil {
		t.Fatal(err)
	}
	want := []GlobalService{GlobalServiceChain, GlobalServiceIndexer, GlobalServiceRelay, GlobalServiceOnion}
	if !slices.Equal(got, want) {
		t.Fatalf("services = %v, want %v", got, want)
	}
}

func TestParseGlobalRoles_exitIsARelayWithAPolicy(t *testing.T) {
	services, exit, err := ParseGlobalRoles([]string{"relay", "exit"})
	if err != nil || !exit || !slices.Equal(services, []GlobalService{GlobalServiceRelay}) {
		t.Fatalf("relay,exit = %v %v %v", services, exit, err)
	}
	if _, exit, err := ParseGlobalRoles([]string{"relay"}); err != nil || exit {
		t.Fatalf("relay alone = exit %v err %v", exit, err)
	}
	for name, in := range map[string][]string{
		"exit alone":       {"exit"},
		"exit and chain":   {"chain", "exit"},
		"exit + dirauth":   {"dirauth", "exit"},
		"nothing":          nil,
		"exit and unknown": {"relay", "exit", "bogus"},
	} {
		if _, _, err := ParseGlobalRoles(in); err == nil {
			t.Errorf("%s: %v was accepted", name, in)
		}
	}
}

func TestGlobalServiceNeedsChain(t *testing.T) {
	for _, s := range GlobalServiceOrder {
		want := s != GlobalServiceChain && s != GlobalServiceRelay && s != GlobalServiceDirauth
		if GlobalServiceNeedsChain(s) != want {
			t.Errorf("%s needs the chain: %v, want %v", s, GlobalServiceNeedsChain(s), want)
		}
	}
}

func TestValidatePersistentPeers_acceptsIDHostPortList(t *testing.T) {
	id := strings.Repeat("a1", 20)
	if err := ValidatePersistentPeers(id + "@203.0.113.7:31000," + id + "@seed.example.org:31000"); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePersistentPeers(""); err != nil {
		t.Fatalf("an empty list is no peers: %v", err)
	}
}

func TestValidatePersistentPeers_refusesAnythingElse(t *testing.T) {
	id := strings.Repeat("a1", 20)
	for _, bad := range []string{
		id + "@host",
		"short@host:1",
		id + "@host:31000 --p2p.pex=true",
		id + "@host:31000\nUser=root",
		id + "@host:31000,",
	} {
		if err := ValidatePersistentPeers(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestRenderGlobalChainUnit_runsCosmovisorAsTheChainAccountWithTheChainListeners(t *testing.T) {
	unit := RenderGlobalChainUnit("")
	exec := mustDirective(t, unit, "ExecStart")
	if !strings.HasPrefix(exec, constants.GlobalBinDir+"/cosmovisor run start --home "+constants.ChainHome+" ") {
		t.Fatalf("ExecStart %q does not run oramad under cosmovisor", exec)
	}
	if strings.Contains(unit, "persistent_peers") {
		t.Errorf("no peers were given but the unit names some\n%s", unit)
	}
	if mustDirective(t, unit, "User") != constants.ChainUser {
		t.Error("the chain unit does not run as the chain account")
	}
	for _, want := range []string{"tcp://0.0.0.0:31000", "tcp://127.0.0.1:31001", "127.0.0.1:31002", "tcp://127.0.0.1:31003"} {
		if !strings.Contains(exec, want) {
			t.Errorf("ExecStart is missing %s", want)
		}
	}
	peers := strings.Repeat("0f", 20) + "@203.0.113.7:31000"
	if exec := mustDirective(t, RenderGlobalChainUnit(peers), "ExecStart"); !strings.HasSuffix(exec, " --p2p.persistent_peers "+peers) {
		t.Errorf("ExecStart %q does not pass the peers", exec)
	}
}

func TestRenderGlobalChainUnit_checksTheSignFloorBeforeEveryStart(t *testing.T) {
	pre := mustDirective(t, RenderGlobalChainUnit(""), "ExecStartPre")
	want := "+" + constants.GlobalBinDir + "/orama global validator check-sign-floor"
	if pre != want {
		t.Fatalf("ExecStartPre = %q, want %q", pre, want)
	}
}

func TestGlobalServiceUnits_orderAfterTheChain(t *testing.T) {
	for name, unit := range map[string]string{
		"provider": RenderGlobalProviderUnit("127.0.0.1"),
		"archiver": RenderGlobalArchiverUnit(),
		"indexer":  RenderGlobalIndexerUnit(),
		"repair":   RenderGlobalRepairUnit(),
	} {
		want := "network-online.target " + constants.ChainServiceUnit
		if name == "provider" {
			// The provider pins through the public Kubo, so it starts after it too.
			want += " " + constants.GlobalIPFSUnit
		}
		for _, key := range []string{"After", "Wants"} {
			if got := mustDirective(t, unit, key); got != want {
				t.Errorf("%s %s=%q, want %q", name, key, got, want)
			}
		}
		if strings.Contains(unit, "Requires=") || strings.Contains(unit, "PartOf=") {
			t.Errorf("%s is bound to the chain's lifecycle", name)
		}
	}
	if strings.Contains(RenderGlobalChainUnit(""), constants.ChainServiceUnit) {
		t.Error("the chain unit orders after itself")
	}
}

func TestSSHDPorts_readsPortAndListenAddressLines(t *testing.T) {
	out := "port 22\nlistenaddress [::]:2222\nlistenaddress 0.0.0.0:2200 rdomain mgmt\nlistenaddress 10.0.0.5\npermitrootlogin no\n"
	if got := sshdPorts(out); !slices.Equal(got, []string{"2222", "2200"}) {
		t.Fatalf("ports = %v, want only the listenaddress ports", got)
	}
	if got := sshdPorts("port 22\nport 2022\n"); !slices.Equal(got, []string{"22", "2022"}) {
		t.Fatalf("with no listenaddress, ports = %v", got)
	}
	listenOnly := func(string, ...string) ([]byte, error) {
		return []byte("port 22\nlistenaddress 0.0.0.0:2222\n"), nil
	}
	if err := checkSSHPort(listenOnly, 22); err == nil {
		t.Fatal("--ssh-port 22 was accepted while sshd listens only on 0.0.0.0:2222")
	}
	run := func(string, ...string) ([]byte, error) { return []byte("listenaddress [::]:2222\n"), nil }
	if err := checkSSHPort(run, 2222); err != nil {
		t.Fatalf("a port only in listenaddress was refused: %v", err)
	}
	if err := checkSSHPort(run, 22); err == nil {
		t.Fatal("a port sshd does not listen on was accepted")
	}
	failing := func(string, ...string) ([]byte, error) {
		return []byte("sshd: no hostkeys"), errors.New("exit status 1")
	}
	if err := checkSSHPort(failing, 22); err == nil {
		t.Fatal("an unreadable sshd configuration was accepted")
	}
}
