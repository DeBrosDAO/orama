package setup

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

func testManifestFor() *netregistry.Manifest {
	return &netregistry.Manifest{Name: "stagenet", ChainID: testChainID, Channel: "nightly"}
}

func planFor(t *testing.T, o Options, existing ...string) *Plan {
	t.Helper()
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(PlanInput{Options: o, Network: testManifestFor(), Env: "stagenet-alice", ExistingHosts: existing})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildPlan_firstMachineCreatesTheRestJoin(t *testing.T) {
	p := planFor(t, Options{IPs: []string{ip1, ip2, ip3}, Name: "alice"})
	roles := []ClusterRole{p.Nodes[0].Cluster, p.Nodes[1].Cluster, p.Nodes[2].Cluster}
	if roles[0] != ClusterCreate || roles[1] != ClusterJoin || roles[2] != ClusterJoin {
		t.Fatalf("roles %v", roles)
	}
	if p.Nodes[0].Name != "alice" || p.Nodes[2].Name != "alice-3" {
		t.Errorf("names %q %q", p.Nodes[0].Name, p.Nodes[2].Name)
	}
}

func TestBuildPlan_anExistingClusterIsJoinedByEveryone(t *testing.T) {
	p := planFor(t, Options{IPs: []string{ip2}, Name: "alice"}, ip1)
	if !p.JoinsExisting || p.Nodes[0].Cluster != ClusterJoin {
		t.Fatalf("%+v", p.Nodes[0])
	}
}

func TestBuildPlan_oneValidatorOnTheFirstFullNode(t *testing.T) {
	p := planFor(t, Options{IPs: []string{ip1, ip2}, Name: "alice"})
	if !p.Nodes[0].Validator || p.Nodes[1].Validator {
		t.Fatalf("validators: %v %v", p.Nodes[0].Validator, p.Nodes[1].Validator)
	}
	p = planFor(t, Options{IPs: []string{ip1}, Name: "alice", NoValidator: true})
	if p.Nodes[0].Validator {
		t.Error("--no-validator creates none")
	}
}

func TestBuildPlan_fullProfileServicesAndRoles(t *testing.T) {
	n := planFor(t, Options{IPs: []string{ip1}, Name: "alice"}).Nodes[0]
	if got := strings.Join(n.ServiceNames(), ","); got != "chain,ipfs,provider" {
		t.Errorf("services %s", got)
	}
	if len(n.Roles) != 1 || n.Roles[0] != clusterreg.RoleStorage {
		t.Errorf("roles %v", n.Roles)
	}
	if n.Profile != install.ProfileFull || n.StorageGB != DefaultStorageGB {
		t.Errorf("profile %s storage %d", n.Profile, n.StorageGB)
	}
}

func TestBuildPlan_relayOnlyWithTheTorFileExitOnlyOnRequest(t *testing.T) {
	relay := planFor(t, Options{IPs: []string{ip1}, Name: "alice", TorNetwork: "t.json"}).Nodes[0]
	if got := strings.Join(relay.ServiceNames(), ","); got != "chain,ipfs,provider,relay" || relay.Exit {
		t.Errorf("services %s exit=%v: a relay by default, never an exit", got, relay.Exit)
	}
	if !contains(strings.Fields(strings.Join(relay.ServiceNames(), " ")), "relay") {
		t.Error("relay missing")
	}
	exit := planFor(t, Options{IPs: []string{ip1}, Name: "alice", TorNetwork: "t.json", Exit: true, Yes: true}).Nodes[0]
	if got := strings.Join(exit.ServiceNames(), ","); got != "chain,ipfs,provider,relay,exit" {
		t.Errorf("services %s", got)
	}
	hasExit := false
	for _, r := range exit.Roles {
		hasExit = hasExit || r == clusterreg.RoleExit
	}
	if !hasExit {
		t.Errorf("roles %v lack exit", exit.Roles)
	}
}

func TestBuildPlan_noTorFileMeansNoRelayAndSaysSo(t *testing.T) {
	p := planFor(t, Options{IPs: []string{ip1}, Name: "alice"})
	if len(p.Notes) != 1 || !strings.Contains(p.Notes[0], "pins no Tor network file") || !strings.Contains(p.Notes[0], "--tor-network") {
		t.Fatalf("notes %v", p.Notes)
	}
}

// A network whose manifest pins a Tor network file gives its nodes a relay with no flag.
func TestBuildPlan_aPinnedTorNetworkMeansARelayWithoutAFlag(t *testing.T) {
	pinned := testManifestFor()
	pinned.TorNetworkSHA256 = strings.Repeat("ab", 32)
	o := Options{IPs: []string{ip1}, Name: "alice"}
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(PlanInput{Options: o, Network: pinned, Env: "e"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Nodes[0].ServiceNames(), ","); got != "chain,ipfs,provider,relay" {
		t.Errorf("services %s, want the relay", got)
	}
	if len(p.Notes) != 0 {
		t.Errorf("notes %v: nothing was left out", p.Notes)
	}
}

func TestBuildPlan_noRelayLeavesItOutOfAPinningNetwork(t *testing.T) {
	pinned := testManifestFor()
	pinned.TorNetworkSHA256 = strings.Repeat("ab", 32)
	o := Options{IPs: []string{ip1}, Name: "alice", NoRelay: true}
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(PlanInput{Options: o, Network: pinned, Env: "e"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Nodes[0].ServiceNames(), ","); got != "chain,ipfs,provider" {
		t.Errorf("services %s, want no relay", got)
	}
	if len(p.Notes) != 1 || !strings.Contains(p.Notes[0], "--no-relay") {
		t.Errorf("notes %v", p.Notes)
	}
}

func TestBuildPlan_aTorFileOnTheCommandLineGivesARelay(t *testing.T) {
	// --tor-network is an override: the relay is planned the same way, with the operator's file.
	p := planFor(t, Options{IPs: []string{ip1}, Name: "alice", TorNetwork: "mine.json"})
	if got := strings.Join(p.Nodes[0].ServiceNames(), ","); got != "chain,ipfs,provider,relay" {
		t.Errorf("services %s", got)
	}
}

func TestBuildPlan_anExitNeedsATorNetworkFromTheFlagOrThePin(t *testing.T) {
	o := Options{IPs: []string{ip1}, Name: "alice", Exit: true, Yes: true}
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	_, err := BuildPlan(PlanInput{Options: o, Network: testManifestFor(), Env: "e"})
	if err == nil || !strings.Contains(err.Error(), "--tor-network") {
		t.Fatalf("an exit with no Tor network file = %v", err)
	}
	pinned := testManifestFor()
	pinned.TorNetworkSHA256 = strings.Repeat("ab", 32)
	p, err := BuildPlan(PlanInput{Options: o, Network: pinned, Env: "e"})
	if err != nil {
		t.Fatalf("an exit on a network that pins the file was refused: %v", err)
	}
	if got := strings.Join(p.Nodes[0].ServiceNames(), ","); got != "chain,ipfs,provider,relay,exit" {
		t.Errorf("services %s", got)
	}
}

func TestBuildPlan_clusterOnly(t *testing.T) {
	p := planFor(t, Options{IPs: []string{ip1, ip2}, ClusterOnly: true})
	for _, n := range p.Nodes {
		if n.Full() || len(n.Services) != 0 || len(n.Roles) != 0 || n.Validator {
			t.Errorf("a cluster-only node got %+v", n)
		}
	}
	if len(p.Notes) != 0 {
		t.Errorf("a cluster-only plan has nothing to note, got %v", p.Notes)
	}
	if p.Nodes[0].Name != "" {
		t.Errorf("no name given, none made up: %q", p.Nodes[0].Name)
	}
}

func TestBuildPlan_aLongNameIsRefusedBeforeItsSuffix(t *testing.T) {
	o := Options{IPs: []string{ip1, ip2}, Name: strings.Repeat("a", nodeNameMax)}
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	_, err := BuildPlan(PlanInput{Options: o, Network: testManifestFor(), Env: "e"})
	if err == nil || !strings.Contains(err.Error(), "-2") {
		t.Fatalf("got %v, want the second name's length refused", err)
	}
}

// A cluster-only node claims no name, so the chain's grammar does not bind its name.
func TestBuildPlan_aClusterOnlyNameNeedNotBeAClaimableName(t *testing.T) {
	p := planFor(t, Options{IPs: []string{ip1}, Name: "ab", ClusterOnly: true})
	if p.Nodes[0].Name != "ab" {
		t.Errorf("name %q", p.Nodes[0].Name)
	}
}

func TestBuildPlan_aFullNodeNameThePlanDerivesIsHeldToTheChainsRules(t *testing.T) {
	o := Options{IPs: []string{ip1}, Name: "node"}
	o.StorageGB = 10
	_, err := BuildPlan(PlanInput{Options: o, Network: testManifestFor(), Env: "e"})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("got %v, want the reserved name refused", err)
	}
}

func TestBuildPlan_summaryNamesEachMachine(t *testing.T) {
	p := planFor(t, Options{IPs: []string{ip1, ip2}, Name: "alice", Domain: "cluster.example.org"})
	text := strings.Join(p.Summary(), "\n")
	for _, want := range []string{"stagenet", testChainID, "alice (" + ip1 + ")", "creates the cluster", "alice-2 (" + ip2 + ")", "joins the cluster", "validator", "cluster.example.org"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary lacks %q:\n%s", want, text)
		}
	}
}

func TestBuildPlan_noMachinesIsAnError(t *testing.T) {
	if _, err := BuildPlan(PlanInput{Network: testManifestFor()}); err == nil {
		t.Fatal("an empty plan must be refused")
	}
	if _, err := BuildPlan(PlanInput{Options: Options{IPs: []string{ip1}}}); err == nil {
		t.Fatal("a plan with no network must be refused")
	}
}

func TestBuildPlan_summaryWarnsWhenItAddsToACluster(t *testing.T) {
	p := planFor(t, Options{IPs: []string{ip2}, Name: "alice"}, ip1)
	if text := strings.Join(p.Summary(), "\n"); !strings.Contains(text, `adds to the cluster already recorded as "stagenet-alice"`) || !strings.Contains(text, "--env") {
		t.Errorf("summary:\n%s", text)
	}
	if text := strings.Join(planFor(t, Options{IPs: []string{ip1}, Name: "alice"}).Summary(), "\n"); strings.Contains(text, "already recorded") {
		t.Errorf("a new cluster is not an addition:\n%s", text)
	}
}
