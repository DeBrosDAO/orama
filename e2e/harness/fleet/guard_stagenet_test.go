package fleet

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
)

const stagenetHome = "/home/o"

func stagenetState() *State {
	st := &State{
		Target: config.TargetStagenet, RunID: "stagenet-20260930-101500", Env: config.StagenetEnv, OperatorNamespace: config.StagenetOperatorNamespace,
		BaseDomain: config.StagenetBaseDomain, GatewayURL: config.StagenetGatewayURL, ChainID: "orama-stagenet-4",
		Home:       config.StagenetPath(stagenetHome, config.StagenetHomeRel),
		RWSock:     config.StagenetPath(stagenetHome, config.StagenetRWSockRel),
		CAFile:     config.StagenetPath(stagenetHome, config.StagenetCAFileRel),
		SSHKeyFile: config.StagenetPath(stagenetHome, config.StagenetSSHKeyRel),
	}
	for _, n := range config.StagenetNodes {
		st.Nodes = append(st.Nodes, Node{Name: n.Name, Role: RoleNameserver, PublicIP: n.IP, WGIP: n.WGIP, SSHUser: n.User})
	}
	return st
}

func TestCheckState_stagenetAcceptsThePinnedState(t *testing.T) {
	if err := CheckState(stagenetState(), stagenetHome); err != nil {
		t.Fatal(err)
	}
}

func TestCheckState_stagenetRefusals(t *testing.T) {
	cases := map[string]struct {
		mutate func(*State)
		want   string
	}{
		"env devnet":         {func(s *State) { s.Env = "devnet" }, "environment"},
		"env testnet":        {func(s *State) { s.Env = "testnet" }, "environment"},
		"env e2e run":        {func(s *State) { s.Env = "e2e-ab12" }, "environment"},
		"devnet domain":      {func(s *State) { s.BaseDomain = "orama-devnet.network" }, "base domain"},
		"testnet domain":     {func(s *State) { s.BaseDomain = "orama-testnet.network" }, "base domain"},
		"e2e run domain":     {func(s *State) { s.BaseDomain = "e2e-ab12.dbrsteting.bid" }, "base domain"},
		"devnet gateway":     {func(s *State) { s.GatewayURL = "https://orama-devnet.network" }, "gateway url"},
		"gateway http":       {func(s *State) { s.GatewayURL = "http://stagenet.dbrsteting.bid" }, "gateway url"},
		"devnet chain":       {func(s *State) { s.ChainID = "orama-devnet-e2e-ab12" }, "chain id"},
		"testnet chain":      {func(s *State) { s.ChainID = "orama-testnet-1" }, "chain id"},
		"chain suffix":       {func(s *State) { s.ChainID = "orama-stagenet-4-devnet-1" }, "chain id"},
		"no chain":           {func(s *State) { s.ChainID = "" }, "chain id"},
		"fleet run id":       {func(s *State) { s.RunID = "ab12cd34" }, "run id"},
		"extra node ip":      {func(s *State) { s.Nodes = append(s.Nodes, Node{Name: "node-4", PublicIP: "203.0.113.9"}) }, "public addresses"},
		"unknown node ip":    {func(s *State) { s.Nodes[2].PublicIP = "203.0.113.9" }, "public addresses"},
		"missing node":       {func(s *State) { s.Nodes = s.Nodes[:2] }, "public addresses"},
		"duplicate node":     {func(s *State) { s.Nodes[2].PublicIP = s.Nodes[1].PublicIP }, "public addresses"},
		"extra server":       {func(s *State) { s.Extras = []Node{{Name: "extra-1", PublicIP: config.StagenetNodes[0].IP}} }, "extras or probes"},
		"probe":              {func(s *State) { s.Probes = []Node{{Name: "probe-1", PublicIP: config.StagenetNodes[0].IP}} }, "extras or probes"},
		"real wallet socket": {func(s *State) { s.RWSock = "/home/o/.rootwallet/agent.sock" }, "real"},
		"other socket":       {func(s *State) { s.RWSock = "/home/o/other/agent.sock" }, "agent socket"},
		"no socket":          {func(s *State) { s.RWSock = "" }, "RW_AGENT_SOCK"},
		"real home as HOME":  {func(s *State) { s.Home = stagenetHome }, "CLI HOME"},
		"fleet agent HOME":   {func(s *State) { s.Home, s.RWSock = "/tmp/e2e-rw-test0001", "/tmp/e2e-rw-test0001/a.sock" }, "CLI HOME"},
		"other CA":           {func(s *State) { s.CAFile = "/tmp/ca.pem" }, "CA bundle"},
		"other ssh key":      {func(s *State) { s.SSHKeyFile = "/home/o/.ssh/id_ed25519" }, "SSH key"},
		"unknown target":     {func(s *State) { s.Target = "devnet" }, "target"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			st := stagenetState()
			c.mutate(st)
			err := CheckState(st, stagenetHome)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestCheckState_stagenetTargetRefusesFleetValues(t *testing.T) {
	st := goodState()
	st.Target = config.TargetStagenet
	err := CheckState(st, stagenetHome)
	if err == nil {
		t.Fatal("the target stagenet accepted a fleet state")
	}
	for _, want := range []string{"environment", "base domain", "chain id", "run id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not report the %s: %v", want, err)
		}
	}
}

func TestCheckState_fleetTargetRefusesStagenetValues(t *testing.T) {
	st := stagenetState()
	st.Target = config.TargetFleet
	if err := CheckState(st, stagenetHome); err == nil {
		t.Fatal("the default target accepted the stagenet state")
	}
}

func TestRunIDShape_acceptsStagenetRunIDs(t *testing.T) {
	for id, want := range map[string]bool{
		"stagenet-20260930-101500": true, "ab12cd34": true, "stagenet-": false, "stagenet-2026": false, "Stagenet-20260930-101500": false,
	} {
		if got := runIDShape(id); got != want {
			t.Errorf("runIDShape(%q) = %v, want %v", id, got, want)
		}
	}
}
