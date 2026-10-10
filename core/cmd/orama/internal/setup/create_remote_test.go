package setup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/install"
)

func TestSSHMachine_initChainPutsAPlaceholderAndRemovesIt(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	in := seatInstall("root", install.GlobalServiceChain)
	if err := m.InitChain(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	wants := []string{"install -d -m 0700", "cat > /opt/orama/.setup-global/genesis.json", "global install --colocated", "sudo rm -rf /opt/orama/.setup-global"}
	last := -1
	for _, want := range wants {
		found := -1
		for i, c := range sh.calls {
			if strings.Contains(c, want) {
				found = i
				break
			}
		}
		if found <= last {
			t.Fatalf("%q ran at %d, after %d; calls:\n%s", want, found, last, strings.Join(sh.calls, "\n"))
		}
		last = found
	}
	if got := sh.stdins[sh.first("genesis.json")]; got != "{\"chain_id\":\"orama-stagenet-6\"}\n" {
		t.Errorf("the placeholder genesis is sent on stdin, got %q", got)
	}
	if sh.first("global start") != "" {
		t.Error("the first install starts nothing: the chain has no genesis yet")
	}
}

func TestSSHMachine_initChainRemovesThePlaceholderWhenTheInstallFails(t *testing.T) {
	sh := &recShell{fail: map[string]error{"global install": errors.New("exit status 1")}}
	m, _ := testMachine(sh)
	err := m.InitChain(context.Background(), seatInstall("root", install.GlobalServiceChain))
	if err == nil || !strings.Contains(err.Error(), "--init-chain") {
		t.Fatalf("got %v", err)
	}
	if sh.first("sudo rm -rf /opt/orama/.setup-global") == "" {
		t.Error("the staging directory must go whatever happens")
	}
}

func TestSSHMachine_initChainPutsTheTorNetworkBesideTheBinaries(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	in := seatInstall("root", install.GlobalServiceChain, install.GlobalServiceRelay)
	in.TorNetwork = []byte(`{"name":"tor"}`)
	if err := m.InitChain(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if cmd := sh.first("tor-network.json"); cmd == "" || sh.stdins[cmd] != `{"name":"tor"}` {
		t.Errorf("the Tor network file is not written from stdin: %v", sh.calls)
	}
}

func TestSSHMachine_seatAndHomeStateAreParsed(t *testing.T) {
	seat := fakeSeat(2)
	sh := &recShell{answers: map[string]string{
		"keys add":      "__NODE_ID__\n" + seat.NodeID + "\n__CONSENSUS__\n{\"key\":\"" + seat.ConsensusPubKey + "\"}\n__SEAT__\n" + seat.Address + "\n",
		"__COMMITTEE__": "__GENESIS__\nnone\n__COMMITTEE__\n0\n__STARTED__\n0\n",
	}}
	m, _ := testMachine(sh)
	got, err := m.Seat(context.Background())
	if err != nil || got != seat {
		t.Fatalf("Seat = %+v, %v", got, err)
	}
	if st, err := m.HomeState(context.Background()); err != nil || st != (HomeState{}) {
		t.Errorf("HomeState = %+v, %v", st, err)
	}
}

func TestSSHMachine_buildGenesisReturnsWhatTheScriptPrints(t *testing.T) {
	sh := &recShell{answers: map[string]string{"genesis-work": `{"chain_id":"x"}`}}
	m, _ := testMachine(sh)
	got, err := m.BuildGenesis(context.Background(), GenesisSteps(GenesisSpec{ChainID: "orama-stagenet-6", Seats: twoSeats(), TestNetwork: true}))
	if err != nil || string(got) != `{"chain_id":"x"}` {
		t.Fatalf("BuildGenesis = %q, %v", got, err)
	}
	if !strings.HasPrefix(sh.calls[0], "bash -c ") || !strings.Contains(sh.calls[0], "add-bootstrap-validator") {
		t.Errorf("the genesis is built by one script: %s", sh.calls[0])
	}
}

func TestSSHMachine_buildGenesisFailureNamesTheStep(t *testing.T) {
	sh := &recShell{fail: map[string]error{"genesis-work": errors.New("run on 203.0.113.11: exit status 1: add-standard-contracts: no wasm module")}}
	m, _ := testMachine(sh)
	if _, err := m.BuildGenesis(context.Background(), [][]string{{"genesis", "validate"}}); err == nil || !strings.Contains(err.Error(), "no wasm module") {
		t.Fatalf("the failure must carry oramad's own words: %v", err)
	}
}

func TestSSHMachine_aGenesisBeyondTheLimitIsRefused(t *testing.T) {
	l := &limited{max: 4}
	if _, err := l.Write([]byte("12345")); err == nil {
		t.Error("a write past the limit succeeded")
	}
}

func TestSSHMachine_putGenesisSendsItOnStdin(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	if err := m.PutGenesis(context.Background(), []byte(`{"chain_id":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if got := sh.stdins[sh.calls[0]]; got != `{"chain_id":"x"}` {
		t.Errorf("stdin = %q", got)
	}
}

func TestSSHMachine_epochAndHealth(t *testing.T) {
	sh := &recShell{answers: map[string]string{
		"current-epoch": `{"epoch_state":{"current_epoch":"3"}}`,
		"__ACTIVE__":    "__STATUS__\n{\"result\":{\"sync_info\":{\"latest_block_height\":\"12\",\"catching_up\":false}}}\n__ACTIVE__\nactive\n__LOG__\nok\n",
	}}
	m, _ := testMachine(sh)
	if e, err := m.Epoch(context.Background()); err != nil || e != 3 {
		t.Errorf("Epoch = %d, %v", e, err)
	}
	if h, err := m.ChainHealth(context.Background()); err != nil || !h.RPCUp || h.Height != 12 || !h.Running {
		t.Errorf("ChainHealth = %+v, %v", h, err)
	}
	sh = &recShell{fail: map[string]error{"current-epoch": errors.New("connection refused")}}
	m, _ = testMachine(sh)
	if _, err := m.Epoch(context.Background()); err == nil || !strings.Contains(err.Error(), "203.0.113.11") {
		t.Errorf("an unreachable chain must be reported with the machine: %v", err)
	}
}

func TestSSHMachine_wireChainRunsTheSecondInstall(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	in := WireInput{Node: seatInstall("root", install.GlobalServiceChain).Node, IP: ip1, User: "root", Peers: strings.Repeat("a", 40) + "@203.0.113.12:31000"}
	if err := m.WireChain(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sh.calls[0], "--persistent-peers") || strings.Contains(sh.calls[0], "--init-chain") {
		t.Errorf("wire = %s", sh.calls[0])
	}
}
