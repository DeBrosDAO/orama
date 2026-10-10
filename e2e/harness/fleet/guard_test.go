package fleet

import (
	"strings"
	"testing"
)

func goodState() *State {
	return &State{RunID: "ab12", Env: "e2e-ab12", BaseDomain: "e2e-ab12.dbrsteting.bid",
		ChainID: "orama-devnet-e2e-ab12", Home: "/tmp/e2e-rw-test0001", RWSock: "/tmp/e2e-rw-test0001/a.sock"}
}

func TestCheckState_runState(t *testing.T) {
	if err := CheckState(goodState(), "/home/o"); err != nil {
		t.Fatal(err)
	}
	noChain := goodState()
	noChain.ChainID = ""
	if err := CheckState(noChain, "/home/o"); err != nil {
		t.Fatalf("a run without a chain was refused: %v", err)
	}
}

func TestCheckState_refusesSharedTargets(t *testing.T) {
	cases := map[string]func(*State){
		"env devnet":        func(s *State) { s.Env = "devnet" },
		"env not e2e":       func(s *State) { s.Env = "sandbox" },
		"domain other zone": func(s *State) { s.BaseDomain = "e2e-ab12.orama.network" },
		"chain testnet":     func(s *State) { s.ChainID = "orama-testnet-1" },
		"real wallet":       func(s *State) { s.RWSock = "/home/o/.rootwallet/agent.sock" },
		"no socket":         func(s *State) { s.RWSock = "" },
		"run id mainnet":    func(s *State) { s.RunID = "mainnet" },
		"no home":           func(s *State) { s.Home = "" },
		"real home as HOME": func(s *State) { s.Home, s.RWSock = "/home/o", "/home/o/a.sock" },
		"home not an agent": func(s *State) { s.Home, s.RWSock = "/tmp/other", "/tmp/other/a.sock" },
		"home nested":       func(s *State) { s.Home, s.RWSock = "/tmp/x/e2e-rw-1", "/tmp/x/e2e-rw-1/a.sock" },
		"socket elsewhere":  func(s *State) { s.RWSock = "/tmp/e2e-rw-other/a.sock" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			st := goodState()
			mutate(st)
			if err := CheckState(st, "/home/o"); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	bad := goodState()
	bad.Env, bad.RWSock = "testnet", ""
	if err := CheckState(bad, "/home/o"); err == nil || !strings.Contains(err.Error(), "testnet") || !strings.Contains(err.Error(), "RW_AGENT_SOCK") {
		t.Fatalf("not every problem reported: %v", err)
	}
}
