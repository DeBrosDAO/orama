package setup

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

func TestRunCreate_lockedWalletTouchesNothing(t *testing.T) {
	h := newCreateHarness(t)
	h.deps.Wallet = fakeWallet{err: errBoom}
	if _, err := Run(context.Background(), h.createOpts(fiveIPs[0]), h.deps); err == nil {
		t.Fatal("a locked wallet was accepted")
	}
	if got := h.w.entries(); len(got) != 0 {
		t.Errorf("a machine was touched: %v", got)
	}
}

func TestRunCreate_badReleaseRootTouchesNothing(t *testing.T) {
	for name, content := range map[string]string{"not json": "release root", "empty": ""} {
		h := newCreateHarness(t)
		if err := os.WriteFile(h.rootFile, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Run(context.Background(), h.createOpts(fiveIPs[0]), h.deps); clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(h.w.entries()) != 0 {
			t.Errorf("%s: a machine was touched", name)
		}
	}
	h := newCreateHarness(t)
	opts := h.createOpts(fiveIPs[0])
	opts.Create.ReleaseRoot = filepath.Join(t.TempDir(), "missing.json")
	if _, err := Run(context.Background(), opts, h.deps); clierr.CodeOf(err) != clierr.CodeUsage {
		t.Errorf("a missing root file: %v", err)
	}
}

func TestRunCreate_anotherClustersEnvironmentIsRefused(t *testing.T) {
	h := newCreateHarness(t)
	h.rec.hosts["stagenet"] = []RecordedNode{{Host: "198.51.100.9", User: "root", Role: "node"}, {Host: fiveIPs[0], User: "root", Role: "node"}}
	_, err := Run(context.Background(), h.createOpts(fiveIPs[0]), h.deps)
	if clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "198.51.100.9") || !strings.Contains(err.Error(), "--env") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunCreate_theSameMachinesRecordedByAnEarlierRunAreFine(t *testing.T) {
	h := newCreateHarness(t)
	h.rec.hosts["stagenet"] = []RecordedNode{{Host: fiveIPs[0], User: "root", Role: "node"}}
	h.mustCreate(t, h.createOpts(fiveIPs[0]))
	if h.w.index("cluster "+fiveIPs[0]+" create") < 0 {
		t.Error("an environment left by a wiped network must not make the first machine join a cluster that is gone")
	}
}

func TestRunCreate_aChainThatStaysDownStopsWithItsLog(t *testing.T) {
	h := newCreateHarness(t)
	opts := h.createOpts(fiveIPs[0])
	// The machine is enrolled by the run; make its chain unit never come up.
	h.deps.Enroll = enrollerFunc(func(ctx context.Context, req MachineRequest) (Machine, error) {
		m, err := h.seats.Enroll(ctx, req)
		if err == nil {
			m.(*seatFake).health = ChainHealth{Running: false, Detail: "panic: wasm vm not found"}
		}
		return m, err
	})
	_, err := Run(context.Background(), opts, h.deps)
	if err == nil || !strings.Contains(err.Error(), "wasm vm not found") || !strings.Contains(err.Error(), fiveIPs[0]) {
		t.Fatalf("a chain unit that stays down must be reported with the end of its log: %v", err)
	}
}

type enrollerFunc func(ctx context.Context, req MachineRequest) (Machine, error)

func (f enrollerFunc) Enroll(ctx context.Context, req MachineRequest) (Machine, error) {
	return f(ctx, req)
}
func (f enrollerFunc) Reach(ctx context.Context, ip, _ string) (Machine, error) {
	return f(ctx, MachineRequest{IP: ip})
}

func TestRunCreate_theFaucetIsAskedAfterTheEpoch(t *testing.T) {
	h := newCreateHarness(t)
	h.w.balance = new(big.Int)
	h.w.faucetPays = bigOramaMany()
	h.deps.Funder = fakeFunder{h.w}
	h.mustCreate(t, h.createOpts(fiveIPs[0]))
	if h.w.index("faucet ") < 0 || h.w.index("faucet ") < h.w.index("epoch ") {
		t.Errorf("the faucet pays from the seats' earnings, so it is asked after the epoch:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

// A network being created has no public faucet yet: the operator is funded by the funder that signs on
// the seats, and the one that asks the seeds' gateways is not asked.
func TestRunCreate_theOperatorIsFundedBySigningOnTheSeatsNotByAskingTheSeeds(t *testing.T) {
	h := newCreateHarness(t)
	h.w.balance = new(big.Int)
	h.w.faucetPays = bigOramaMany()
	h.deps.CreateFunder = fakeFunder{h.w}
	h.deps.Funder = failingFunder{}
	h.mustCreate(t, h.createOpts(fiveIPs[0]))
	if h.w.count("faucet ") != 1 {
		t.Errorf("the seat-signing funder was asked %d times, want once:\n%s", h.w.count("faucet "), strings.Join(h.w.entries(), "\n"))
	}
}

// failingFunder fails the test run when it is asked.
type failingFunder struct{}

func (failingFunder) Fund(context.Context, *netregistry.Manifest, string, *big.Int) error {
	return errors.New("the public faucet of a network being created was asked")
}

func TestRunCreate_noFaucetNoEpochWait(t *testing.T) {
	h := newCreateHarness(t)
	opts := h.createOpts(fiveIPs[0])
	opts.Create.NoFaucet = true
	h.mustCreate(t, opts)
	if h.w.count("epoch ") != 0 {
		t.Error("the epoch was awaited on a network with no faucet")
	}
	data, _ := os.ReadFile(filepath.Join(h.publishDir, "stagenet", "manifest.json"))
	if m, err := netregistry.ParseManifest(data); err != nil || m.Faucet {
		t.Errorf("manifest faucet = %v (%v), want false", m, err)
	}
	steps := h.seats.seats[fiveIPs[0]].built
	if slices.Contains(steps[1], "--faucet-enabled") {
		t.Errorf("the genesis turns the faucet on: %q", steps[1])
	}
}

func TestRunCreate_theEpochIsPolledUntilItArrives(t *testing.T) {
	h := newCreateHarness(t)
	h.deps.Enroll = enrollerFunc(func(ctx context.Context, req MachineRequest) (Machine, error) {
		m, err := h.seats.Enroll(ctx, req)
		if err == nil {
			m.(*seatFake).epochs = []uint64{1, 1, 2}
		}
		return m, err
	})
	h.mustCreate(t, h.createOpts(fiveIPs[0]))
	if got := h.w.count("epoch "); got != 3 {
		t.Errorf("epoch polled %d times, want 3", got)
	}
}

func TestRunCreate_theDomainResumeCommandCreatesAgain(t *testing.T) {
	h := newCreateHarness(t)
	h.deps.Domain = fakeDomain{w: h.w, waitErr: errBoom}
	opts := h.createOpts(fiveIPs[0])
	opts.Domain = "cluster.example.org"
	_, err := Run(context.Background(), opts, h.deps)
	if err == nil || !strings.Contains(err.Error(), "--create-network stagenet") || strings.Contains(err.Error(), "--network ") {
		t.Fatalf("the command that resumes a creation creates, it does not join: %v", err)
	}
}

func TestRunCreate_theGenesisIsBuiltTwiceAndTheBuildsMustAgree(t *testing.T) {
	h := newCreateHarness(t)
	h.mustCreate(t, h.createOpts(fiveIPs[:3]...))
	if h.w.count("build-genesis ") != 2 || h.w.index("build-genesis "+fiveIPs[1]) < 0 {
		t.Errorf("the second machine must build the same genesis as a witness:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	single := newCreateHarness(t)
	single.mustCreate(t, single.createOpts(fiveIPs[0]))
	if single.w.count("build-genesis ") != 1 {
		t.Error("one machine has no witness to ask")
	}
}

func TestRunCreate_aBuilderThatAddsStateIsCaughtByTheWitness(t *testing.T) {
	h := newCreateHarness(t)
	h.deps.Enroll = enrollerFunc(func(ctx context.Context, req MachineRequest) (Machine, error) {
		m, err := h.seats.Enroll(ctx, req)
		if err == nil && req.IP == fiveIPs[0] {
			m.(*seatFake).tamper = func(doc map[string]any) {
				doc["app_state"].(map[string]any)["houses"] = map[string]any{"enacted": map[string]any{"m_activated": true}}
			}
		}
		return m, err
	})
	_, err := Run(context.Background(), h.createOpts(fiveIPs[:2]...), h.deps)
	if err == nil || !strings.Contains(err.Error(), "app_state.houses") || !strings.Contains(err.Error(), fiveIPs[0]) {
		t.Fatalf("a genesis that one machine changed after the other built it must be refused, naming the module: %v", err)
	}
	if h.w.count("put-genesis ") != 0 || h.w.count("startglobal ") != 0 {
		t.Error("a genesis nobody could agree on was handed out")
	}
	if _, statErr := os.Stat(filepath.Join(h.publishDir, "stagenet")); statErr == nil {
		t.Error("a genesis nobody could agree on was published")
	}
}

func TestRunCreate_aKeptGenesisThatTheWitnessDoesNotBuildIsRefused(t *testing.T) {
	tampered, err := ApplyConsensusParams(genesisFor{chainID: "orama-stagenet-6", seats: fakeSeats(2), allowStake: true, faucet: true}.json())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(tampered, &doc); err != nil {
		t.Fatal(err)
	}
	doc["app_state"].(map[string]any)["houses"] = map[string]any{"enacted": map[string]any{"m_activated": true}}
	tampered, _ = json.Marshal(doc)

	h := newCreateHarness(t)
	h.enroll.facts = map[string]Facts{
		fiveIPs[0]: {Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true},
		fiveIPs[1]: {Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true},
	}
	h.seats.carries(fiveIPs[0], tampered, false)
	h.seats.carries(fiveIPs[1], placeholderGenesis("orama-stagenet-6"), false)
	_, err = Run(context.Background(), h.createOpts(fiveIPs[:2]...), h.deps)
	if err == nil || !strings.Contains(err.Error(), "app_state.houses") || !strings.Contains(err.Error(), "--force-new-genesis") {
		t.Fatalf("a genesis a machine says it carries must be what another machine builds: %v", err)
	}
	if h.w.count("put-genesis ") != 0 || h.w.count("startglobal ") != 0 {
		t.Error("the genesis was handed on")
	}
}

func TestRunCreate_oneMachineHasNoWitnessForAGenesisItCarries(t *testing.T) {
	h := newCreateHarness(t)
	h.enroll.facts = map[string]Facts{fiveIPs[0]: {Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true}}
	h.seats.carries(fiveIPs[0], finalGenesis(0), false)
	h.mustCreate(t, h.createOpts(fiveIPs[0]))
	if h.w.count("build-genesis ") != 0 {
		t.Error("a single machine built a genesis it already carries")
	}
}
