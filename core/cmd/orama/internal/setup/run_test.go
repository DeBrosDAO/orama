package setup

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

const (
	ip1 = "203.0.113.11"
	ip2 = "203.0.113.12"
	ip3 = "203.0.113.13"
)

func run(t *testing.T, h *harness, opts Options) (*Result, error) {
	t.Helper()
	return Run(context.Background(), opts, h.deps)
}

func mustRun(t *testing.T, h *harness, opts Options) *Result {
	t.Helper()
	res, err := run(t, h, opts)
	if err != nil {
		t.Fatalf("Run: %v\nlog:\n%s", err, strings.Join(h.w.entries(), "\n"))
	}
	return res
}

func TestRun_threeFullNodesFromScratch(t *testing.T) {
	h := newHarness()
	res := mustRun(t, h, h.opts(ip1, ip2, ip3))

	w := h.w
	// The first machine creates the cluster; the others join through a node that is in.
	if got := w.count("cluster "); got != 3 {
		t.Fatalf("%d cluster installs, want 3", got)
	}
	if w.index("cluster "+ip1+" create") < 0 || w.index("cluster "+ip2+" join") < 0 || w.index("cluster "+ip3+" join") < 0 {
		t.Errorf("wrong cluster roles:\n%s", strings.Join(w.entries(), "\n"))
	}
	if w.index("invite on "+ip1) < 0 || w.count("invite on ") != 2 {
		t.Errorf("both invites are minted on the node that created the cluster: %v", w.entries())
	}
	if !(w.index("cluster "+ip1) < w.index("cluster "+ip2) && w.index("cluster "+ip2) < w.index("cluster "+ip3)) {
		t.Error("cluster installs must run one after the other, in the order given")
	}
	if w.index("trust point") < w.index("cluster "+ip3) || w.count("trust point") != 1 {
		t.Error("the trust point is read once, after the cluster exists")
	}
	if w.count("global ") != 3 {
		t.Errorf("%d global installs, want 3", w.count("global "))
	}
	if !strings.Contains(strings.Join(w.entries(), "\n"), "global "+ip1+" chain,ipfs,provider") {
		t.Errorf("services: %v", w.entries())
	}
	// Restarts are one at a time and follow every global install.
	if w.maxRestarting != 1 || w.count("restart ") != 3 {
		t.Errorf("restarts: %d at once, %d total; want 1 and 3", w.maxRestarting, w.count("restart "))
	}
	lastGlobal := w.index("global " + ip3)
	if w.index("restart ") < lastGlobal {
		t.Error("a node was restarted before the global layer was installed everywhere")
	}
	if res.Operator != testOperator || h.rec.oper[res.Env] != testOperator {
		t.Errorf("the operator %q was not recorded on %q", res.Operator, res.Env)
	}
}

func TestRun_aClusterWithFewerThanThreeVotersIsForcedOnlyWhenTheNodeSaysSo(t *testing.T) {
	small := newHarness()
	small.enroll.quorumRefusal = quorumBreak(2)
	mustRun(t, small, small.opts(ip1, ip2))
	first, forced := small.w.index("restart-force=false "+ip1), small.w.index("restart-force=true "+ip1)
	if first < 0 || forced < 0 || first > forced {
		t.Errorf("the node is asked first, then forced:\n%s", strings.Join(small.w.entries(), "\n"))
	}
	if !strings.Contains(small.report.text(), "the cluster has 2 voter(s), too few to keep a quorum") {
		t.Errorf("forcing is announced with the node's own count:\n%s", small.report.text())
	}

	willing := newHarness()
	mustRun(t, willing, willing.opts(ip1, ip2))
	if willing.w.index("restart-force=true") >= 0 {
		t.Errorf("a node that does not refuse is never forced:\n%s", strings.Join(willing.w.entries(), "\n"))
	}
}

func TestRun_aStaleRecordNeverForcesALargerCluster(t *testing.T) {
	// The CLI recorded two nodes; the node itself counts four voters, one of them down.
	h := newHarness()
	h.enroll.quorumRefusal = quorumBreak(4)
	_, err := run(t, h, h.opts(ip1, ip2))
	if err == nil || !strings.Contains(err.Error(), "4 configured voters") {
		t.Fatalf("got %v: the node's count decides, not the CLI's record", err)
	}
	if h.w.index("restart-force=true") >= 0 {
		t.Error("a cluster the node counts at four voters was forced")
	}
}

func TestRun_aRefusalThatCountsNoVotersIsNeverForced(t *testing.T) {
	h := newHarness()
	h.enroll.quorumRefusal = quorumUnreadable
	_, err := run(t, h, h.opts(ip1))
	if err == nil || !strings.Contains(err.Error(), "Cannot verify quorum safety") {
		t.Fatalf("got %v", err)
	}
	if h.w.index("restart-force=true") >= 0 {
		t.Error("a check that could not read the cluster was overridden")
	}
}

func TestRun_aClusterOfThreeIsNeverForced(t *testing.T) {
	h := newHarness()
	h.enroll.quorumRefusal = quorumBreak(3)
	_, err := run(t, h, h.opts(ip1, ip2, ip3))
	if err == nil || !strings.Contains(err.Error(), "restart the cluster node") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("got %v: the node's refusal on a cluster that can keep a quorum is the answer", err)
	}
	if h.w.index("restart-force=true") >= 0 {
		t.Error("a cluster of three is restarted under the node's own check, never forced")
	}
	if h.w.count("restart-force=false") != 1 {
		t.Errorf("the next machine is not touched after a refusal, got %d restarts", h.w.count("restart-force=false"))
	}
}

func TestRefusedForTooFewVoters(t *testing.T) {
	for _, tc := range []struct {
		msg    string
		voters int
		ok     bool
	}{
		{quorumBreak(1), 1, true}, {quorumBreak(2), 2, true}, {quorumBreak(3), 0, false}, {quorumBreak(5), 0, false},
		{quorumUnreadable, 0, false}, {"exit status 1", 0, false},
		{"would break RQLite quorum: 0 of 0 configured voters", 0, false},
	} {
		voters, ok := refusedForTooFewVoters(errors.New(tc.msg))
		if ok != tc.ok || voters != tc.voters {
			t.Errorf("%.60q: got %d, %v; want %d, %v", tc.msg, voters, ok, tc.voters, tc.ok)
		}
	}
}

func TestRun_chainRegistrationPerNode(t *testing.T) {
	h := newHarness()
	mustRun(t, h, h.opts(ip1, ip2))
	w := h.w
	if w.count("tx register-operator") != 1 {
		t.Errorf("the operator registers once, got %d", w.count("tx register-operator"))
	}
	for _, name := range []string{"alice", "alice-2"} {
		if w.index("tx register-node "+name+" roles=[2] asn=24940") < 0 {
			t.Errorf("node %s was not registered with the storage role and its ASN:\n%s", name, strings.Join(w.entries(), "\n"))
		}
		if w.index("tx capacity "+name+" 10000000000") < 0 {
			t.Errorf("node %s did not declare 10 GB", name)
		}
	}
	// 10 GB is 10 GiB rounded up: 10 ORAMA of storage bond at 1 ORAMA/GiB.
	if w.index("tx bond alice role=2 amount=10000000000") < 0 {
		t.Errorf("storage bond:\n%s", strings.Join(w.entries(), "\n"))
	}
	if w.count("tx create-validator") != 1 || w.index("tx create-validator alice") < 0 {
		t.Errorf("one validator, on the first node: %v", w.entries())
	}
	if !(w.index("tx register-node alice ") < w.index("tx bond alice") && w.index("tx bond alice") < w.index("tx capacity alice") && w.index("tx capacity alice") < w.index("start "+ip1)) {
		t.Errorf("a node registers, bonds, declares capacity, then starts its services:\n%s", strings.Join(w.entries(), "\n"))
	}
	if w.index("tx create-validator") < w.index("start "+ip1) {
		t.Error("the validator is created once the node's services run")
	}
}

func TestRun_clusterOnlyTouchesNoGlobalLayerAndNoChain(t *testing.T) {
	h := newHarness()
	opts := h.opts(ip1, ip2)
	opts.ClusterOnly, opts.StorageGB, opts.Name = true, 0, ""
	res := mustRun(t, h, opts)
	for _, forbidden := range []string{"global ", "trust point", "open chain", "tx ", "restart ", "chainstate "} {
		if h.w.index(forbidden) >= 0 {
			t.Errorf("a cluster-only run did %q:\n%s", forbidden, strings.Join(h.w.entries(), "\n"))
		}
	}
	if h.w.count("cluster ") != 2 {
		t.Errorf("both nodes get the cluster")
	}
	if res.Operator != "" {
		t.Errorf("cluster-only registers no operator, got %q", res.Operator)
	}
}

func TestRun_hardwareRefusedBeforeAnyMachineIsChanged(t *testing.T) {
	h := newHarness()
	small := freshFacts()
	small.Hardware.RAMBytes = 4 << 30
	h.enroll.facts = map[string]Facts{ip2: small}
	_, err := run(t, h, h.opts(ip1, ip2))
	if err == nil || !strings.Contains(err.Error(), ip2) || !strings.Contains(err.Error(), "8.0 GiB") || !strings.Contains(err.Error(), "--cluster-only") {
		t.Fatalf("got %v, want the small machine named with its floor and the way out", err)
	}
	for _, changed := range []string{"stage ", "cluster ", "fetch release"} {
		if h.w.index(changed) >= 0 {
			t.Errorf("%q ran although a machine was refused", changed)
		}
	}
	if h.w.count("close ") != 2 {
		t.Errorf("every enrolled machine is closed, got %d", h.w.count("close "))
	}
}

func TestRun_alreadyInstalledMachinesAreSkipped(t *testing.T) {
	h := newHarness()
	installed := freshFacts()
	installed.ClusterInstalled, installed.GlobalInstalled = true, true
	installed.ManifestSHA256, installed.CLISHA256, installed.ChainActive = testManifest, testCLISHA, true
	h.enroll.facts = map[string]Facts{ip1: installed}
	h.w.operatorRegistered, h.w.validator = true, true
	h.w.nodes["alice"] = &RegisteredNode{
		Roles: []int{clusterreg.RoleStorage}, CapacityBytes: 10_000_000_000,
		Bonds: map[int]*big.Int{clusterreg.RoleStorage: big.NewInt(10 * noramaPerOrama)},
	}
	res := mustRun(t, h, h.opts(ip1))
	for _, forbidden := range []string{"stage ", "cluster ", "global ", "restart ", "fetch release", "trust point", "tx "} {
		if h.w.index(forbidden) >= 0 {
			t.Errorf("a finished machine was given %q again:\n%s", forbidden, strings.Join(h.w.entries(), "\n"))
		}
	}
	for _, step := range []Step{StepHardware, StepRelease, StepCluster, StepGlobal, StepRestart} {
		if !h.report.has(ip1, step, StateSkipped) {
			t.Errorf("step %s was not reported as skipped", step)
		}
	}
	// The chain is still asked whether it has caught up: a finished machine answers at once.
	if !h.report.has(ip1, StepSync, StateDone) || h.w.index("chainstate "+ip1) < 0 {
		t.Error("a re-run still waits for the chain to be synced before it registers anything")
	}
	if len(res.Skipped[ip1]) == 0 {
		t.Error("the result lists no skipped steps")
	}
}

func TestRun_addingANodeToAnExistingCluster(t *testing.T) {
	h := newHarness()
	h.rec.hosts["stagenet-alice"] = []RecordedNode{{Host: ip1, User: "root", Role: "node"}}
	h.rec.active = "stagenet-alice"
	res := mustRun(t, h, h.opts(ip2))
	if res.Env != "stagenet-alice" {
		t.Errorf("env %q, want the active environment on this network", res.Env)
	}
	if h.w.index("cluster "+ip2+" join") < 0 || h.w.index("cluster "+ip2+" create") >= 0 {
		t.Errorf("the new node must join:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.index("reach "+ip1) < 0 || h.w.index("invite on "+ip1) < 0 {
		t.Error("the invite is minted on a node already in the cluster, reached without a prompt")
	}
	if got := h.rec.gateway["stagenet-alice"]; got != "" {
		t.Errorf("an existing environment keeps its gateway, setup passed %q", got)
	}
	if len(h.rec.cluster["stagenet-alice"]) != 2 {
		t.Errorf("recorded nodes %v, want the old one and the new one", h.rec.cluster["stagenet-alice"])
	}
}

func TestRun_notFundedStopsWithTheExactShortfall(t *testing.T) {
	h := newHarness()
	h.w.balance = big.NewInt(3 * noramaPerOrama)
	_, err := run(t, h, h.opts(ip1))
	var nf *NotFundedError
	if !errors.As(err, &nf) {
		t.Fatalf("got %v, want a NotFundedError", err)
	}
	// 10 ORAMA storage bond + 1000 self-bond + 2 + 2 reserve = 1014; the account holds 3.
	for _, want := range []string{testOperator, "holds 3 ORAMA", "needs 1014 ORAMA", "send at least 1011 ORAMA", "resumes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if h.w.index("tx ") >= 0 {
		t.Error("no transaction is sent before the account is funded")
	}
	if h.w.index("cluster "+ip1) < 0 {
		t.Error("the cluster is installed first, so the re-run only has the chain steps left")
	}
}

func TestRun_faucetFundsAUnfundedOperator(t *testing.T) {
	h := newHarness()
	h.networks.faucet = true
	h.deps.Networks = h.networks
	h.deps.Funder = fakeFunder{h.w}
	h.w.balance = new(big.Int)
	h.w.faucetPays = new(big.Int).Mul(big.NewInt(2000), big.NewInt(noramaPerOrama))
	mustRun(t, h, h.opts(ip1))
	if h.w.faucetCalls != 1 || h.w.index("faucet "+testOperator+" 1014000000000") < 0 {
		t.Errorf("faucet calls: %v", h.w.entries())
	}
	if h.w.index("faucet") > h.w.index("tx register-operator") {
		t.Error("the faucet pays before the first transaction")
	}
}

func TestRun_faucetThatRefusesIsReportedWithTheShortfall(t *testing.T) {
	h := newHarness()
	h.networks.faucet = true
	h.deps.Networks = h.networks
	h.deps.Funder = fakeFunder{h.w}
	h.w.balance = new(big.Int)
	_, err := run(t, h, h.opts(ip1))
	var nf *NotFundedError
	if !errors.As(err, &nf) || !strings.Contains(err.Error(), "cooldown") {
		t.Fatalf("got %v, want the faucet's reason inside a NotFundedError", err)
	}
}

func TestRun_noFaucetOnThisNetworkNeverCallsOne(t *testing.T) {
	h := newHarness()
	h.deps.Funder = fakeFunder{h.w}
	h.w.balance = new(big.Int)
	_, _ = run(t, h, h.opts(ip1))
	if h.w.faucetCalls != 0 {
		t.Error("a network whose manifest has no faucet was asked for one")
	}
}

func TestRun_resumedRunBondsOnlyTheDifference(t *testing.T) {
	h := newHarness()
	h.w.operatorRegistered = true
	h.w.nodes["alice"] = &RegisteredNode{
		Roles: []int{clusterreg.RoleStorage},
		Bonds: map[int]*big.Int{clusterreg.RoleStorage: big.NewInt(4 * noramaPerOrama)},
	}
	mustRun(t, h, h.opts(ip1))
	if h.w.count("tx register-node") != 0 || h.w.count("tx register-operator") != 0 {
		t.Errorf("already registered, got:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.index("tx bond alice role=2 amount=6000000000") < 0 {
		t.Errorf("only the missing 6 ORAMA are bonded:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_chainThatNeverCatchesUpIsAnError(t *testing.T) {
	h := newHarness()
	h.enroll.state = ChainState{Running: true, Height: 10, CatchingUp: true}
	h.deps.Timing.SyncDeadline = 20 * 1_000_000
	_, err := run(t, h, h.opts(ip1))
	if err == nil || !strings.Contains(err.Error(), "gave up waiting") || !strings.Contains(err.Error(), ip1) {
		t.Fatalf("got %v", err)
	}
	if h.w.index("restart ") >= 0 || h.w.index("tx ") >= 0 {
		t.Error("nothing follows a chain that has not synced")
	}
}

func TestRun_chainUnitDownEndsTheWaitWithItsLog(t *testing.T) {
	h := newHarness()
	h.enroll.state = ChainState{Running: false, Detail: "ERR UPGRADE \"v2\" NEEDED at height 4000"}
	_, err := run(t, h, h.opts(ip1))
	if err == nil || !strings.Contains(err.Error(), "    | ERR UPGRADE") || !strings.Contains(err.Error(), "known gaps") {
		t.Fatalf("got %v, want the chain's own log", err)
	}
}

func TestRun_declinedConfirmationChangesNothing(t *testing.T) {
	h := newHarness()
	h.deps.Confirm = func(*Plan) (bool, error) { return false, nil }
	opts := h.opts(ip1)
	opts.Yes = false
	_, err := run(t, h, opts)
	if clierr.CodeOf(err) != clierr.CodeAborted {
		t.Fatalf("got %v (code %d), want the aborted code", err, clierr.CodeOf(err))
	}
	if h.w.index("enroll") >= 0 {
		t.Error("no machine is reached before the plan is confirmed")
	}
}

func TestRun_lockedWalletStopsBeforeAnythingElse(t *testing.T) {
	h := newHarness()
	h.deps.Wallet = fakeWallet{err: errors.New("the RootWallet agent is locked")}
	if _, err := run(t, h, h.opts(ip1)); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("got %v", err)
	}
	if len(h.w.entries()) != 0 {
		t.Errorf("touched the world: %v", h.w.entries())
	}
}

func TestRun_unpublishedGenesisStopsBeforeAnyMachine(t *testing.T) {
	h := newHarness()
	h.networks.genesisErr = errors.New("the network has no genesis published yet")
	h.deps.Networks = h.networks
	if _, err := run(t, h, h.opts(ip1)); err == nil || !strings.Contains(err.Error(), "no genesis") {
		t.Fatalf("got %v", err)
	}
	if h.w.index("enroll") >= 0 {
		t.Error("a machine was reached for a network that is not live")
	}
}

func TestRun_asnLookupFailureNamesTheFlag(t *testing.T) {
	h := newHarness()
	h.deps.ASN = func(context.Context, string) (uint32, error) { return 0, errors.New("dns timeout") }
	_, err := run(t, h, h.opts(ip1))
	if err == nil || !strings.Contains(err.Error(), "--asn") {
		t.Fatalf("got %v", err)
	}
	opts := h.opts(ip1)
	opts.ASN, opts.ASNSet = 0, true
	h.w.operatorRegistered = false
	mustRun(t, h, opts)
	if h.w.index("tx register-node alice roles=[2] asn=0") < 0 {
		t.Errorf("--asn 0 leaves it undeclared: %v", h.w.entries())
	}
}

func TestRun_nameClaimNeedsAClaimerElseSaysSo(t *testing.T) {
	h := newHarness()
	mustRun(t, h, h.opts(ip1))
	if !h.report.has(ip1, StepName, StateSkipped) {
		t.Error("without a claimer the step must say the claim is not available")
	}
	h2 := newHarness()
	h2.deps.Names = fakeNames{w: h2.w}
	mustRun(t, h2, h2.opts(ip1, ip2))
	if h2.w.index("claim alice alice") < 0 || h2.w.index("claim alice-2 alice-2") < 0 {
		t.Errorf("each node claims its own name: %v", h2.w.entries())
	}
}

func TestRun_nameClaimFailureIsAnError(t *testing.T) {
	h := newHarness()
	h.deps.Names = fakeNames{w: h.w, err: errors.New("name taken")}
	if _, err := run(t, h, h.opts(ip1)); err == nil || !strings.Contains(err.Error(), "name taken") {
		t.Fatalf("got %v", err)
	}
}

func TestRun_domainPrintsRecordsThenWaits(t *testing.T) {
	h := newHarness()
	h.deps.Domain = fakeDomain{w: h.w}
	opts := h.opts(ip1)
	opts.Domain = "cluster.example.org"
	opts.ClusterOnly, opts.StorageGB = true, 0
	mustRun(t, h, opts)
	if !strings.Contains(h.report.text(), "ns1.cluster.example.org.\tIN\tA\t203.0.113.10") {
		t.Errorf("the records are printed:\n%s", h.report.text())
	}
	if h.w.index("domain records") > h.w.index("domain wait") || h.w.index("domain wait") < 0 {
		t.Error("records first, wait last")
	}
	if got := h.rec.gateway["stagenet-alice"]; got != "https://cluster.example.org" {
		t.Errorf("gateway %q, want the domain", got)
	}
	if h.enroll.created[ip1].installed.Domain != "cluster.example.org" {
		t.Error("the cluster node is installed on the domain")
	}
}

func TestRun_domainThatNeverDelegatesGivesAResumeCommand(t *testing.T) {
	h := newHarness()
	h.deps.Domain = fakeDomain{w: h.w, waitErr: errors.New("gave up waiting for NS records")}
	opts := h.opts(ip1)
	opts.Domain, opts.ClusterOnly, opts.StorageGB = "cluster.example.org", true, 0
	res, err := run(t, h, opts)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"orama setup --network stagenet --env stagenet-alice", "--ip " + ip1, "--domain cluster.example.org", "--cluster-only", "--yes", "resumes at this step"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q:\n%v", want, err)
		}
	}
	if res != nil {
		t.Error("an error returns no result")
	}
}

func TestRun_enrollFailureNamesTheMachine(t *testing.T) {
	h := newHarness()
	h.enroll.fail = map[string]error{ip2: errors.New("host key not confirmed")}
	_, err := run(t, h, h.opts(ip1, ip2))
	if err == nil || !strings.Contains(err.Error(), ip2) || !strings.Contains(err.Error(), "host key") {
		t.Fatalf("got %v", err)
	}
	if h.w.index("cluster ") >= 0 {
		t.Error("nothing is installed when a machine cannot be reached")
	}
}

func TestRun_clusterInstallFailureStopsTheRunAndKeepsWhatWasRecorded(t *testing.T) {
	h := newHarness()
	h.deps.Enroll = &failSecond{fakeEnroller: h.enroll}
	_, err := run(t, h, h.opts(ip1, ip2))
	if err == nil || !strings.Contains(err.Error(), ip2) {
		t.Fatalf("got %v", err)
	}
	if h.w.index("global ") >= 0 {
		t.Error("the global layer is not started after a failed cluster install")
	}
	if len(h.rec.cluster["stagenet-alice"]) != 1 {
		t.Errorf("the node that did install is recorded, got %v", h.rec.cluster["stagenet-alice"])
	}
}

// failSecond makes the machine at ip2 fail its cluster install.
type failSecond struct{ *fakeEnroller }

func (f *failSecond) Enroll(ctx context.Context, req MachineRequest) (Machine, error) {
	m, err := f.fakeEnroller.Enroll(ctx, req)
	if err == nil && req.IP == ip2 {
		m.(*fakeMachine).installClusterErr = errors.New("install exploded")
	}
	return m, err
}

func TestInspect_reportsEachMachineAndChangesNothing(t *testing.T) {
	h := newHarness()
	small := freshFacts()
	small.Hardware.CPUCores = 2
	installed := freshFacts()
	installed.ClusterInstalled, installed.GlobalInstalled = true, true
	h.enroll.facts = map[string]Facts{ip2: small, ip3: installed}
	h.enroll.fail = map[string]error{"203.0.113.14": errors.New("host key not confirmed")}
	opts := h.opts(ip1, ip2, ip3, "203.0.113.14")
	got, err := Inspect(context.Background(), opts, h.enroll)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].Err != nil || !strings.Contains(got[0].Summary(), "enough") {
		t.Fatalf("%+v", got)
	}
	if got[1].Err == nil || !strings.Contains(got[1].Summary(), "2 vCPU (needs 4)") {
		t.Errorf("the small machine: %s", got[1].Summary())
	}
	if !got[2].Installed || !strings.Contains(got[2].Summary(), "already installed") {
		t.Errorf("the installed machine: %s", got[2].Summary())
	}
	if got[3].Err == nil || !strings.Contains(got[3].Summary(), "cannot reach it") {
		t.Errorf("the unreachable machine: %s", got[3].Summary())
	}
	for _, changed := range []string{"stage ", "cluster ", "global ", "tx ", "fetch release"} {
		if h.w.index(changed) >= 0 {
			t.Errorf("an inspection did %q", changed)
		}
	}
	if h.w.count("close ") != 3 {
		t.Errorf("every machine reached is closed, got %d", h.w.count("close "))
	}
}

func TestInspect_refusesBadOptions(t *testing.T) {
	h := newHarness()
	opts := h.opts()
	if _, err := Inspect(context.Background(), opts, h.enroll); err == nil {
		t.Fatal("no machines, no inspection")
	}
}

func TestPlanFor_resolvesTheNetworkAndTouchesNoMachine(t *testing.T) {
	h := newHarness()
	plan, err := PlanFor(context.Background(), h.opts(ip1, ip2), h.deps)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Network != "stagenet" || plan.Env != "stagenet-alice" || len(plan.Nodes) != 2 || plan.Nodes[0].Cluster != ClusterCreate {
		t.Fatalf("%+v", plan)
	}
	if len(h.w.entries()) != 0 {
		t.Errorf("planning touched the world: %v", h.w.entries())
	}
}

func TestPlanFor_anExistingClusterOfTheEnvironmentIsJoined(t *testing.T) {
	h := newHarness()
	h.rec.hosts["mine"] = []RecordedNode{{Host: ip1, User: "root"}}
	opts := h.opts(ip2)
	opts.Env = "mine"
	plan, err := PlanFor(context.Background(), opts, h.deps)
	if err != nil || !plan.JoinsExisting || plan.Env != "mine" {
		t.Fatalf("%+v, %v", plan, err)
	}
}

func TestPlanFor_badOptionsAreRefused(t *testing.T) {
	h := newHarness()
	opts := h.opts(ip1)
	opts.Name = ""
	if _, err := PlanFor(context.Background(), opts, h.deps); err == nil {
		t.Fatal("a full node needs a name")
	}
}

func TestNewDeps_wiresEveryPortExceptTheNameClaimer(t *testing.T) {
	d := NewDeps(&bufReporter{}, nil)
	if d.Networks == nil || d.Releases == nil || d.Trust == nil || d.Wallet == nil || d.Enroll == nil || d.Chain == nil ||
		d.Funder == nil || d.ASN == nil || d.Record == nil || d.Domain == nil || d.Report == nil {
		t.Fatalf("a port is missing: %+v", d)
	}
	if d.Names != nil {
		t.Error("the name claim is not implemented yet and must say so, not pretend")
	}
	if d.Timing.SyncDeadline <= 0 || d.Timing.DNSDeadline <= 0 {
		t.Errorf("timing %+v", d.Timing)
	}
}

// installedFacts is a machine a first run finished: everything installed, the
// chain running, the release in place and the cluster node started after the chain.
func installedFacts() Facts {
	f := freshFacts()
	f.ClusterInstalled, f.GlobalInstalled, f.ChainActive = true, true, true
	f.ManifestSHA256, f.CLISHA256 = testManifest, testCLISHA
	return f
}

func TestRun_aRunStoppedBetweenTheInstallAndTheStartStartsTheChain(t *testing.T) {
	h := newHarness()
	f := installedFacts()
	f.ChainActive = false
	h.enroll.facts = map[string]Facts{ip1: f}
	mustRun(t, h, h.opts(ip1))
	if h.w.index("startglobal "+ip1+" chain,ipfs,provider") < 0 {
		t.Errorf("an installed machine whose chain is down is started:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.index("global "+ip1) >= 0 {
		t.Error("it is not installed again")
	}
	if h.w.index("startglobal") > h.w.index("chainstate") {
		t.Error("the start comes before the wait for the chain")
	}
}

func TestRun_aRunStoppedWhileTheChainWasSyncingWaitsAgainBeforeItRegisters(t *testing.T) {
	h := newHarness()
	h.enroll.facts = map[string]Facts{ip1: installedFacts()}
	h.enroll.state = ChainState{Running: true, Height: 10, CatchingUp: true}
	h.deps.Timing.SyncDeadline = 20 * 1_000_000
	_, err := run(t, h, h.opts(ip1))
	if err == nil || !strings.Contains(err.Error(), "gave up waiting") {
		t.Fatalf("got %v: the chain has not caught up, and nothing may be sent on its stale answers", err)
	}
	if h.w.index("tx ") >= 0 || h.w.index("open chain") >= 0 {
		t.Errorf("the chain was used before it had caught up:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_aClusterNodeStartedBeforeTheChainUnitIsRestarted(t *testing.T) {
	h := newHarness()
	f := installedFacts()
	f.RestartPending = true
	h.enroll.facts = map[string]Facts{ip1: f}
	mustRun(t, h, h.opts(ip1))
	if h.w.count("restart ") != 1 {
		t.Errorf("a node whose gateway predates the chain unit restarts even though this run installed nothing:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_anInstalledClusterNodeThatDoesNotAnswerIsAnError(t *testing.T) {
	h := newHarness()
	h.enroll.facts = map[string]Facts{ip1: installedFacts()}
	h.deps.Enroll = &failWait{fakeEnroller: h.enroll}
	_, err := run(t, h, h.opts(ip1))
	if err == nil || !strings.Contains(err.Error(), ip1) || !strings.Contains(err.Error(), "not carrying its share") {
		t.Fatalf("got %v", err)
	}
	if h.w.index("global ") >= 0 || h.w.index("tx ") >= 0 {
		t.Error("nothing follows a cluster node that does not answer")
	}
}

type failWait struct{ *fakeEnroller }

func (f *failWait) Enroll(ctx context.Context, req MachineRequest) (Machine, error) {
	m, err := f.fakeEnroller.Enroll(ctx, req)
	if err == nil {
		m.(*fakeMachine).waitErr = errors.New("rqlite is not a voter yet")
	}
	return m, err
}

func TestRun_aReleaseWithADifferentCLIIsStagedEvenWithTheSameManifest(t *testing.T) {
	for name, cliSum := range map[string]string{"a different CLI": strings.Repeat("0", 64), "the release's CLI": testCLISHA} {
		h := newHarness()
		f := installedFacts()
		f.GlobalInstalled, f.ChainActive, f.CLISHA256 = false, false, cliSum
		h.enroll.facts = map[string]Facts{ip1: f}
		mustRun(t, h, h.opts(ip1))
		staged := h.w.index("stage "+ip1) >= 0
		if staged != (cliSum != testCLISHA) {
			t.Errorf("%s: staged=%v: a copied manifest does not make a build, the binary beside it does:\n%s", name, staged, strings.Join(h.w.entries(), "\n"))
		}
	}
}

func TestRun_aChainThatCannotBePolledEndsTheWaitEarly(t *testing.T) {
	h := newHarness()
	h.enroll.state = ChainState{}
	h.deps.Enroll = &pollBroken{fakeEnroller: h.enroll}
	h.deps.Timing.SyncDeadline = 30 * 1_000_000_000
	_, err := run(t, h, h.opts(ip1))
	if err == nil || !strings.Contains(err.Error(), "could not be polled") || !strings.Contains(err.Error(), "ssh: handshake failed") {
		t.Fatalf("got %v, want the poll error after a few in a row, not after the 30s deadline", err)
	}
}

type pollBroken struct{ *fakeEnroller }

func (p *pollBroken) Enroll(ctx context.Context, req MachineRequest) (Machine, error) {
	m, err := p.fakeEnroller.Enroll(ctx, req)
	if err == nil {
		m.(*fakeMachine).stateErr = errors.New("ssh: handshake failed")
	}
	return m, err
}

func TestGlobalParallelism(t *testing.T) {
	for size, want := range map[int]int{0: 1, 1: 1, 2: 1, 3: 1, 4: 1, 5: 2, 7: 3, 9: 4, 20: 4} {
		if got := globalParallelism(size); got != want {
			t.Errorf("a cluster of %d installs %d at a time, want %d", size, got, want)
		}
	}
}

func TestRun_aBudgetBeyondAnyRealNetworkIsRefusedBeforeASignature(t *testing.T) {
	h := newHarness()
	h.chain.params.BondPerGiB = new(big.Int).Mul(big.NewInt(10_000_000), big.NewInt(noramaPerOrama))
	_, err := run(t, h, h.opts(ip1))
	if err == nil || !strings.Contains(err.Error(), "over the") || !strings.Contains(err.Error(), "ORAMA setup will sign") {
		t.Fatalf("got %v", err)
	}
	if h.w.index("tx ") >= 0 || h.w.index("faucet") >= 0 {
		t.Error("nothing is signed or requested when the figures are not believable")
	}
}

func TestRun_theBudgetIsPrintedBeforeAnythingIsSigned(t *testing.T) {
	h := newHarness()
	mustRun(t, h, h.opts(ip1))
	if !strings.Contains(h.report.text(), "this setup bonds 10 ORAMA, self-bonds 1000 ORAMA") {
		t.Errorf("lines:\n%s", h.report.text())
	}
}

func TestRun_aBondTheChainReportsAboveTheTargetIsNotSigned(t *testing.T) {
	h := newHarness()
	h.w.operatorRegistered = true
	// 20 ORAMA already bonded against a 10 ORAMA target is not a reason to bond more.
	h.w.nodes["alice"] = &RegisteredNode{Roles: []int{clusterreg.RoleStorage}, CapacityBytes: 10_000_000_000,
		Bonds: map[int]*big.Int{clusterreg.RoleStorage: big.NewInt(20 * noramaPerOrama)}}
	mustRun(t, h, h.opts(ip1))
	if h.w.count("tx bond") != 0 {
		t.Errorf("an over-bonded node is left as it is:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestQuoteLog_noLineOfARemoteLogPassesForOurs(t *testing.T) {
	got := quoteLog("first\n[203.0.113.9] cluster done\n")
	want := "    | first\n    | [203.0.113.9] cluster done"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
