package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

var fiveIPs = []string{"203.0.113.11", "203.0.113.12", "203.0.113.13", "203.0.113.14", "203.0.113.15"}

func TestRunCreate_fiveSeatsFromScratch(t *testing.T) {
	h := newCreateHarness(t)
	res := h.mustCreate(t, h.createOpts(fiveIPs...))
	w, log := h.w, strings.Join(h.w.entries(), "\n")

	if w.index("cluster "+fiveIPs[0]+" create") < 0 || w.count("cluster ") != 5 {
		t.Errorf("the first machine creates the cluster, the others join:\n%s", log)
	}
	if got := w.count("init-chain "); got != 5 {
		t.Fatalf("%d chain homes made, want 5", got)
	}
	if w.count("build-genesis ") != 2 || w.index("build-genesis "+fiveIPs[0]) < 0 || w.index("build-genesis "+fiveIPs[1]) < 0 {
		t.Errorf("the genesis is built on the first machine, and again on the second as a witness:\n%s", log)
	}
	if lastInit := lastIndex(w.entries(), "init-chain "); w.index("build-genesis") < lastInit {
		t.Error("the genesis was built before every seat had its keys")
	}
	steps := h.seats.seats[fiveIPs[0]].built
	if got := strings.Join(steps[2], " "); !strings.HasSuffix(got, "--min-committee-size 5") {
		t.Errorf("the committee size is the number of machines: %q", got)
	}
	if w.count("put-genesis ") != 5 {
		t.Errorf("all five machines take the genesis (the first holds only the placeholder):\n%s", log)
	}
	for _, ip := range fiveIPs {
		if w.index("wire "+ip) < w.index("put-genesis "+ip) {
			t.Errorf("%s was wired before it had the genesis", ip)
		}
	}
	checkPeers(t, h, fiveIPs)
	checkStartsOneByOne(t, w, fiveIPs)
	if w.index("restart ") < w.index("startglobal "+fiveIPs[4]) {
		t.Error("the cluster nodes are restarted after the chains are started")
	}
	if w.index("epoch ") < 0 || w.index("epoch ") > w.index("tx register-operator") {
		t.Errorf("the operator is registered after the chain reached epoch %d:\n%s", createMinEpoch, log)
	}
	if w.count("tx register-node ") != 5 || w.count("tx create-validator") != 0 {
		t.Errorf("five nodes are registered and no validator is created (the seats are validators already):\n%s", log)
	}
	checkPublished(t, h, res)
}

func lastIndex(entries []string, prefix string) int {
	last := -1
	for i, e := range entries {
		if strings.HasPrefix(e, prefix) {
			last = i
		}
	}
	return last
}

func checkPeers(t *testing.T, h *createHarness, ips []string) {
	t.Helper()
	for i, ip := range ips {
		peers := strings.Split(h.seats.seats[ip].wired, ",")
		if len(peers) != len(ips)-1 {
			t.Errorf("%s has %d peers, want %d", ip, len(peers), len(ips)-1)
		}
		self := fakeSeat(i).NodeID + "@" + ip
		for _, p := range peers {
			if strings.HasPrefix(p, self) {
				t.Errorf("%s dials itself: %s", ip, p)
			}
			if !strings.HasSuffix(p, ":31000") {
				t.Errorf("peer %q is not on the chain's p2p port", p)
			}
		}
	}
}

// checkStartsOneByOne: each chain is started after the previous one answered.
func checkStartsOneByOne(t *testing.T, w *world, ips []string) {
	t.Helper()
	for i := 1; i < len(ips); i++ {
		start, previousStart := w.index("startglobal "+ips[i]), w.index("startglobal "+ips[i-1])
		if previousStart < 0 || start < previousStart {
			t.Fatalf("chains are started in the order of the machines")
		}
		gated := false
		for _, e := range w.entries()[previousStart:start] {
			gated = gated || e == "health "+ips[i-1]
		}
		if !gated {
			t.Errorf("%s was started before %s answered", ips[i], ips[i-1])
		}
	}
}

func checkPublished(t *testing.T, h *createHarness, res *Result) {
	t.Helper()
	dir := filepath.Join(h.publishDir, "stagenet")
	for _, f := range []string{"manifest.json", "genesis.json", "release-root.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("the creation did not write %s: %v", f, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := netregistry.ParseManifest(data)
	if err != nil {
		t.Fatalf("the manifest written does not parse: %v", err)
	}
	genesis, _ := os.ReadFile(filepath.Join(dir, "genesis.json"))
	if err := m.VerifyGenesis(genesis); err != nil {
		t.Errorf("the manifest does not pin the genesis written: %v", err)
	}
	if m.ChainID != "orama-stagenet-6" || !m.Faucet || m.Channel != "nightly" || len(m.Seeds) != 5 || m.Seeds[0] != "seed1.stagenet.orama.network" {
		t.Errorf("manifest = %+v", m)
	}
	if res.Created == nil || res.Created.Dir != dir || res.Created.Manifest.GenesisSHA256 != m.GenesisSHA256 {
		t.Errorf("Result.Created = %+v", res.Created)
	}
	if !strings.Contains(string(genesis), `"max_gas": "100000000"`) || !strings.Contains(string(genesis), `"vote_extensions_enable_height": "2"`) {
		t.Errorf("the published genesis lacks the consensus parameters:\n%s", genesis)
	}
}

func TestRunCreate_rerunKeepsTheGenesisAndTheKeys(t *testing.T) {
	h := newCreateHarness(t)
	genesis, err := ApplyConsensusParams(finalGenesis(0, 1, 2))
	if err != nil {
		t.Fatal(err)
	}
	ips := fiveIPs[:3]
	h.enroll.facts = map[string]Facts{}
	for _, ip := range ips {
		h.seats.carries(ip, genesis, true)
		h.enroll.facts[ip] = Facts{Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true, ChainActive: true, ManifestSHA256: testManifest, CLISHA256: testCLISHA}
	}
	res := h.mustCreate(t, h.createOpts(ips...))
	for _, prefix := range []string{"init-chain ", "put-genesis ", "wire ", "startglobal ", "cluster ", "restart "} {
		if n := h.w.count(prefix); n != 0 {
			t.Errorf("a run on machines that are done did %q %d times:\n%s", prefix, n, strings.Join(h.w.entries(), "\n"))
		}
	}
	if h.w.count("build-genesis ") != 0 {
		t.Errorf("a chain has run on this genesis, so it is history and no witness is asked:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.count("read-genesis ") != 1 {
		t.Errorf("the genesis is read back from one machine once")
	}
	published, _ := os.ReadFile(filepath.Join(h.publishDir, "stagenet", "genesis.json"))
	if string(published) != string(genesis) {
		t.Error("the genesis published is not the one the machines carry")
	}
	if res.Created == nil {
		t.Error("a re-run still reports what to publish")
	}
}

func TestRunCreate_machinesWithAChainHomeKeepTheirKeys(t *testing.T) {
	h := newCreateHarness(t)
	h.enroll.facts = map[string]Facts{fiveIPs[0]: {Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true, ManifestSHA256: testManifest, CLISHA256: testCLISHA}}
	h.seats.carries(fiveIPs[0], placeholderGenesis("orama-stagenet-6"), false)
	h.mustCreate(t, h.createOpts(fiveIPs[:2]...))
	if h.w.index("init-chain "+fiveIPs[0]) >= 0 {
		t.Error("a machine with a chain home was initialised again: its keys would be lost")
	}
	if h.w.index("init-chain "+fiveIPs[1]) < 0 {
		t.Error("the fresh machine got no chain home")
	}
}

func TestRunCreate_halfDistributedGenesisIsCompleted(t *testing.T) {
	h := newCreateHarness(t)
	genesis := finalGenesis(0, 1)
	h.enroll.facts = map[string]Facts{
		fiveIPs[0]: {Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true},
		fiveIPs[1]: {Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true},
	}
	h.seats.carries(fiveIPs[0], genesis, false)
	h.seats.carries(fiveIPs[1], placeholderGenesis("orama-stagenet-6"), false)
	h.mustCreate(t, h.createOpts(fiveIPs[:2]...))
	if h.w.count("build-genesis ") != 1 || h.w.index("build-genesis "+fiveIPs[1]) < 0 {
		t.Error("a genesis that is on a machine is built once, by the other machine, as a witness")
	}
	if h.w.index("put-genesis "+fiveIPs[1]) < 0 || h.w.index("put-genesis "+fiveIPs[0]) >= 0 {
		t.Errorf("only the machine without the genesis takes it:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRunCreate_differentGenesisFilesAreRefused(t *testing.T) {
	h := newCreateHarness(t)
	h.enroll.facts = map[string]Facts{
		fiveIPs[0]: {Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true},
		fiveIPs[1]: {Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true},
	}
	h.seats.carries(fiveIPs[0], finalGenesis(0, 1), false)
	h.seats.carries(fiveIPs[1], finalGenesis(1, 0), false)
	_, err := Run(context.Background(), h.createOpts(fiveIPs[:2]...), h.deps)
	if err == nil || !strings.Contains(err.Error(), "different genesis") {
		t.Fatalf("two different genesis files were accepted: %v", err)
	}
	if h.w.count("startglobal ") != 0 {
		t.Error("a chain was started")
	}
}

func TestRunCreate_aGenesisWithoutThisCommitteeIsRefused(t *testing.T) {
	h := newCreateHarness(t)
	h.enroll.facts = map[string]Facts{fiveIPs[0]: {Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true}}
	h.seats.carries(fiveIPs[0], finalGenesis(7), false)
	_, err := Run(context.Background(), h.createOpts(fiveIPs[0]), h.deps)
	if err == nil || !strings.Contains(err.Error(), "should start") {
		t.Fatalf("a genesis built for another committee was accepted: %v", err)
	}
}

func TestRunCreate_forceNewGenesisRebuildsOnlyBeforeAChainRan(t *testing.T) {
	facts := map[string]Facts{fiveIPs[0]: {Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true}}

	fresh := newCreateHarness(t)
	fresh.enroll.facts = facts
	fresh.seats.carries(fiveIPs[0], finalGenesis(7), false)
	opts := fresh.createOpts(fiveIPs[0])
	opts.Create.ForceNewGenesis = true
	fresh.mustCreate(t, opts)
	if fresh.w.count("build-genesis ") != 1 || !strings.Contains(fresh.report.text(), "WARNING: --force-new-genesis") {
		t.Errorf("a forced rebuild builds once and warns loudly:\n%s", fresh.report.text())
	}

	ran := newCreateHarness(t)
	ran.enroll.facts = facts
	ran.seats.carries(fiveIPs[0], finalGenesis(0), true)
	opts = ran.createOpts(fiveIPs[0])
	opts.Create.ForceNewGenesis = true
	_, err := Run(context.Background(), opts, ran.deps)
	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "new chain id") {
		t.Errorf("a new genesis over a chain that ran must be refused with the way out: %v", err)
	}
	if ran.w.count("build-genesis ") != 0 {
		t.Error("a genesis was built over a chain that ran")
	}
}

func TestRunCreate_aPublishedChainIdKeepsItsGenesisAndNoChainStarts(t *testing.T) {
	h := newCreateHarness(t)
	dir := filepath.Join(h.publishDir, "stagenet")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	other := []byte(`{"chain_id":"orama-stagenet-6","other":true}`)
	if _, err := netregistry.Publish(netregistry.PublishInput{
		Dir: h.publishDir, Name: "stagenet", ChainID: "orama-stagenet-6", Genesis: other, ReleaseRoot: []byte(`{"a":1}`),
		Seeds: []string{"seed1.stagenet.orama.network"}, Channel: "nightly", MinVersion: "0.3.0", ReleaseRepo: "https://releases.example",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), h.createOpts(fiveIPs[0]), h.deps)
	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "another --chain-id") {
		t.Fatalf("publishing another genesis under a published chain id must be refused: %v", err)
	}
	if h.w.count("wire ") != 0 || h.w.count("startglobal ") != 0 {
		t.Errorf("the chain was set up although the network cannot be published:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}
