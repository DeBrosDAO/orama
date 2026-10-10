package setup

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/netclass"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/version"
)

func createOptions(ips ...string) Options {
	return Options{IPs: ips, Yes: true, Create: &CreateOptions{Name: "stagenet", ChainID: "orama-stagenet-6", ReleaseRoot: "/tmp/release-root.json"}}
}

func nIPs(n int) []string {
	ips := make([]string, n)
	for i := range ips {
		ips[i] = fmt.Sprintf("203.0.113.%d", 10+i)
	}
	return ips
}

func TestNormalize_createDefaults(t *testing.T) {
	o := createOptions(nIPs(5)...)
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	c := o.Create
	if o.Name != "founder" || c.ReleaseRepo != DefaultReleaseRepo || c.PublishDir != "networks" || c.Channel != "nightly" || c.MinVersion != version.Current {
		t.Errorf("defaults: name %q, %+v", o.Name, c)
	}
	if got := c.seedNames(3); strings.Join(got, " ") != "seed1.stagenet.orama.network seed2.stagenet.orama.network seed3.stagenet.orama.network" {
		t.Errorf("seeds = %v", got)
	}
	if !c.Faucet() || c.Production() {
		t.Error("a stagenet has the faucet")
	}
	if err := o.Normalize(); err != nil {
		t.Errorf("normalizing twice failed: %v", err)
	}
}

func TestNormalize_createKeepsWhatWasGiven(t *testing.T) {
	o := createOptions(nIPs(2)...)
	o.Name = "alice"
	o.Create.Seeds, o.Create.Channel, o.Create.PublishDir = []string{"seed.example.org"}, "dev/feature", "/tmp/out"
	o.Create.ReleaseRepo, o.Create.MinVersion, o.Create.NoFaucet = "https://r.example.org", "1.2.3", true
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	c := o.Create
	if o.Name != "alice" || c.Channel != "dev/feature" || c.PublishDir != "/tmp/out" || c.ReleaseRepo != "https://r.example.org" || c.MinVersion != "1.2.3" || c.Faucet() {
		t.Errorf("options = %q %+v", o.Name, c)
	}
	if got := c.seedNames(2); len(got) != 1 || got[0] != "seed.example.org" {
		t.Errorf("seeds = %v", got)
	}
}

func TestNormalize_createRefusesProductionBelowTheFloor(t *testing.T) {
	o := createOptions(nIPs(5)...)
	o.Create.ChainID = "orama-1"
	err := o.Normalize()
	if clierr.CodeOf(err) != clierr.CodeUsage || err == nil || !strings.Contains(err.Error(), "at least 30") || !strings.Contains(err.Error(), "-stagenet-") {
		t.Fatalf("a production chain id with five machines must be refused, with the way out: %v", err)
	}
}

func TestNormalize_aProductionNetworkIsMoreThanOneRunTakes(t *testing.T) {
	o := createOptions(nIPs(MaxNodes + 1)...)
	o.Create.ChainID = "orama-1"
	if err := o.Normalize(); err == nil || !strings.Contains(err.Error(), "in one run") {
		t.Fatalf("err = %v: the floor of 30 is beyond the %d machines one run takes", err, MaxNodes)
	}
}

func TestNormalize_createProductionClass(t *testing.T) {
	c := CreateOptions{Name: "main", ChainID: "orama-1"}
	if !c.Production() || c.Faucet() || defaultChannel(c.ChainID) != netregistry.ChannelMain {
		t.Error("a production chain id has no faucet and the main channel")
	}
}

func TestNormalize_createConflictsAndMissing(t *testing.T) {
	cases := map[string]func(*Options){
		"--network":         func(o *Options) { o.Network = "stagenet" },
		"--cluster-only":    func(o *Options) { o.ClusterOnly = true },
		"--no-validator":    func(o *Options) { o.NoValidator = true },
		"--chain-id":        func(o *Options) { o.Create.ChainID = "" },
		"--release-root":    func(o *Options) { o.Create.ReleaseRoot = "" },
		"a localnet's":      func(o *Options) { o.Create.ChainID = "orama-x-localnet-1" },
		"name":              func(o *Options) { o.Create.Name = "Stage Net" },
		"chain_id":          func(o *Options) { o.Create.ChainID = "Orama_Stagenet" },
		"seed":              func(o *Options) { o.Create.Seeds = []string{"203.0.113.9"} },
		"channel":           func(o *Options) { o.Create.Channel = "beta" },
		"min_version":       func(o *Options) { o.Create.MinVersion = "dev" },
		"must be an https":  func(o *Options) { o.Create.ReleaseRepo = "http://releases.example.org" },
		"node name":         func(o *Options) { o.Name = "A" },
		"two machines each": func(o *Options) { o.IPs = []string{"203.0.113.10", "203.0.113.10"} },
	}
	for want, mutate := range cases {
		o := createOptions(nIPs(2)...)
		mutate(&o)
		err := o.Normalize()
		if err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("%s: err = %v, want a usage error", want, err)
		}
	}
}

func TestCommandLine_repeatsACreation(t *testing.T) {
	o := createOptions("203.0.113.10", "203.0.113.11")
	o.Create.NoFaucet, o.Create.ForceNewGenesis = true, true
	o.Create.Seeds = []string{"a.example.org", "b.example.org"}
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	line := o.CommandLine()
	for _, want := range []string{
		"--create-network stagenet", "--chain-id orama-stagenet-6", "--release-root /tmp/release-root.json",
		"--seed a.example.org --seed b.example.org", "--no-faucet", "--ip 203.0.113.10 --ip 203.0.113.11", "--yes",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("command line lacks %q: %s", want, line)
		}
	}
	for _, absent := range []string{"--force-new-genesis", "--release-repo", "--publish-dir", "--channel", "--network "} {
		if strings.Contains(line, absent) {
			t.Errorf("command line has %q: %s", absent, line)
		}
	}
}

func TestBuildCreatePlan_everyMachineIsASeat(t *testing.T) {
	o := createOptions(nIPs(5)...)
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	m, err := o.Create.manifest(strings.Repeat("a", 64), 5)
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildCreatePlan(o, m, "stagenet")
	if err != nil {
		t.Fatal(err)
	}
	if p.JoinsExisting || p.Env != "stagenet" || p.ChainID != "orama-stagenet-6" || len(p.Nodes) != 5 {
		t.Fatalf("plan = %+v", p)
	}
	for i, n := range p.Nodes {
		wantRole := ClusterJoin
		if i == 0 {
			wantRole = ClusterCreate
		}
		if !n.BindConsensus {
			t.Errorf("node %d: every seat binds its consensus key to the operator", i)
		}
		if !n.Full() || n.Cluster != wantRole || n.Validator {
			t.Errorf("node %d = %+v: every seat is full, only the first creates the cluster, none creates a validator", i, n)
		}
	}
	if p.Nodes[0].Name != "founder" || p.Nodes[4].Name != "founder-5" {
		t.Errorf("names = %q .. %q", p.Nodes[0].Name, p.Nodes[4].Name)
	}
	notes := strings.Join(p.Notes, "\n")
	for _, want := range []string{"CREATES the network stagenet (chain orama-stagenet-6)", "5 machines are its bootstrap committee", "seed1.stagenet.orama.network", "networks/stagenet/", "faucet on", "test keyring"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
}

func TestBuildCreatePlan_notACreation(t *testing.T) {
	if _, err := BuildCreatePlan(Options{IPs: nIPs(1)}, &netregistry.Manifest{}, "x"); err == nil {
		t.Error("a join was planned as a creation")
	}
}

func TestBuildCreatePlan_productionNotes(t *testing.T) {
	c := CreateOptions{Name: "main", ChainID: "orama-1", PublishDir: "networks"}
	notes := strings.Join(createNotes(&c, &netregistry.Manifest{Name: "main", ChainID: "orama-1", Channel: "main", Seeds: []string{"a.b"}}, 30), "\n")
	if !strings.Contains(notes, "production chain id") || strings.Contains(notes, "faucet on") {
		t.Errorf("notes = %s", notes)
	}
}

// A production chain id needs 30 bootstrap validators, and one run takes fewer:
// today no production network can be created here, which keeps the seats' test
// keyring (unencrypted, on each machine) out of one. Raising MaxNodes to the floor
// opens that door, so it must come with seat keys held somewhere safer.
func TestCreate_productionFloorIsBeyondOneRun(t *testing.T) {
	if MaxNodes >= netclass.ProductionMinCommittee {
		t.Fatalf("one run takes %d machines and a production committee is %d: a production network would now be created with seat keys in oramad's test keyring; "+
			"hold the seat keys elsewhere before this limit is raised", MaxNodes, netclass.ProductionMinCommittee)
	}
}
