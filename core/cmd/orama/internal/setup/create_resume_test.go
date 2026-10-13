package setup

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestRunCreate_anInstallThatDiedInsideTheChainHomeIsFinishedWithoutInitChain(t *testing.T) {
	h := newCreateHarness(t)
	// The chain home exists (the install died before it wrote the unit), so the
	// machine has keys that --init-chain would refuse to touch.
	h.enroll.facts = map[string]Facts{fiveIPs[0]: {Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true}}
	h.seats.carries(fiveIPs[0], placeholderGenesis("orama-stagenet-6"), false)
	h.mustCreate(t, h.createOpts(fiveIPs[0]))
	if h.w.index("init-chain "+fiveIPs[0]) >= 0 {
		t.Errorf("--init-chain was run on a chain home that exists:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.count("wire "+fiveIPs[0]) != 2 {
		t.Errorf("the install is finished, then wired to its peers: %d wires", h.w.count("wire "+fiveIPs[0]))
	}
}

func TestRunCreate_fiveMachinesDoneAlreadyAreSkippedInParallel(t *testing.T) {
	h := newCreateHarness(t)
	genesis := finalGenesis(0, 1, 2, 3, 4)
	h.enroll.facts = map[string]Facts{}
	for _, ip := range fiveIPs {
		h.seats.carries(ip, genesis, true)
		h.enroll.facts[ip] = Facts{Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true, ChainActive: true, ManifestSHA256: testManifest, CLISHA256: testCLISHA}
	}
	res := h.mustCreate(t, h.createOpts(fiveIPs...))
	for _, ip := range fiveIPs {
		if !slices.Contains(res.Skipped[ip], StepGlobal) {
			t.Errorf("%s: the global step was not skipped: %v", ip, res.Skipped[ip])
		}
	}
}

func TestRunCreate_aKeptGenesisForALargerCommitteeIsRefused(t *testing.T) {
	h := newCreateHarness(t)
	h.enroll.facts = map[string]Facts{fiveIPs[0]: {Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true}}
	h.seats.carries(fiveIPs[0], finalGenesis(0, 1), false)
	_, err := Run(context.Background(), h.createOpts(fiveIPs[0]), h.deps)
	if err == nil || !strings.Contains(err.Error(), "names 2 bootstrap validators, want the 1 seats") {
		t.Fatalf("a genesis with a validator no machine holds the key of would never produce a block: %v", err)
	}
	if h.w.count("startglobal ") != 0 {
		t.Error("a chain was started")
	}
}

func TestRunCreate_twoMachinesWithTheSameKeysAreRefused(t *testing.T) {
	h := newCreateHarness(t)
	h.deps.Enroll = enrollerFunc(func(ctx context.Context, req MachineRequest) (Machine, error) {
		m, err := h.seats.Enroll(ctx, req)
		if err == nil {
			m.(*seatFake).idx = 0 // a cloned disk: every machine reports the first one's keys
		}
		return m, err
	})
	_, err := Run(context.Background(), h.createOpts(fiveIPs[:2]...), h.deps)
	if err == nil || !strings.Contains(err.Error(), "one validator, not two") {
		t.Fatalf("two machines with one set of keys were accepted: %v", err)
	}
	if h.w.count("build-genesis ") != 0 {
		t.Error("a genesis was built from keys that two machines share")
	}
}
