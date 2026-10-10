package setup

import (
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// Deps are the ports a run uses. Funder and Domain may be nil: without a faucet
// an unfunded account is reported, and without a domain waiter a --domain run
// only prints the records.
type Deps struct {
	Networks NetworkSource
	Releases ReleaseSource
	Trust    TrustSource
	Wallet   Wallet
	Enroll   Enroller
	Chain    ChainOpener
	Funder   Funder
	// CreateFunder funds the operator of a network being created, whose seeds serve
	// no public faucet yet; nil falls back to Funder.
	CreateFunder Funder
	Names        NameClaimer
	ASN          ASNLookup
	Record       Recorder
	Domain       DomainWaiter
	Report       Reporter
	Timing       Timing
	// Confirm is asked once with the plan when the run is not --yes. Declining
	// ends the run with nothing changed.
	Confirm func(*Plan) (bool, error)
}

// Result is what a run did.
type Result struct {
	Plan *Plan
	Env  string
	// Operator is the operator account on the chain, empty for cluster-only.
	Operator string
	// Skipped lists, by IP, the steps the machine already had.
	Skipped map[string][]Step
	// Created is set when the run created the network.
	Created *CreatedNetwork
}

// nodeRun is one machine of the run.
type nodeRun struct {
	plan  NodePlan
	m     Machine
	facts Facts
	// globalNew says this run installed the global layer, so the cluster node
	// has to be restarted to see the chain's listeners.
	globalNew bool
}

type runner struct {
	opts    Options
	d       Deps
	net     *netregistry.Network
	genesis []byte
	plan    *Plan
	evm     string
	oper    string
	runs    []*nodeRun
	// via is a machine already in the cluster, where invites are minted.
	via     Machine
	closers []func()
	// clusterSize is how many nodes the cluster has once this run is done.
	clusterSize int
	res         *Result
	// create is what a network creation keeps between its phases.
	create *createState
}

// Run sets up every machine of opts. It returns early, with nothing changed,
// when a check fails before the first machine is touched: the wallet, the
// network, the genesis, the hardware.
func Run(ctx context.Context, opts Options, d Deps) (*Result, error) {
	if err := ResolveAnnounced(ctx, &opts, d); err != nil {
		return nil, err
	}
	if err := opts.Normalize(); err != nil {
		return nil, err
	}
	r := &runner{opts: opts, d: d, res: &Result{Skipped: map[string][]Step{}}}
	defer r.close()
	if opts.Create != nil {
		return r.runCreate(ctx)
	}
	return r.run(ctx)
}

func (r *runner) run(ctx context.Context) (*Result, error) {
	if err := r.preflight(ctx); err != nil {
		return nil, err
	}
	if err := r.connect(ctx); err != nil {
		return nil, err
	}
	if err := r.stageReleases(ctx); err != nil {
		return nil, err
	}
	if err := r.clusterPhase(ctx); err != nil {
		return nil, err
	}
	r.announceDomain(ctx)
	if r.hasFull() {
		for _, phase := range []func(context.Context) error{r.globalPhase, r.restartPhase, r.onchainPhase} {
			if err := phase(ctx); err != nil {
				return nil, err
			}
		}
	}
	if err := r.waitDomain(ctx); err != nil {
		return nil, err
	}
	return r.res, r.recordOperator()
}

func (r *runner) close() {
	for i := len(r.closers) - 1; i >= 0; i-- {
		r.closers[i]()
	}
}

func (r *runner) emit(ip string, step Step, state State, detail string) {
	r.d.Report.Emit(Event{Node: ip, Step: step, State: state, Detail: detail})
}

func (r *runner) skip(ip string, step Step, detail string) {
	r.res.Skipped[ip] = append(r.res.Skipped[ip], step)
	r.emit(ip, step, StateSkipped, detail)
}

func (r *runner) hasFull() bool {
	for _, n := range r.runs {
		if n.plan.Full() {
			return true
		}
	}
	return false
}

// preflight checks everything that needs no machine: the wallet, the network,
// its genesis, and the plan the operator confirms.
func (r *runner) preflight(ctx context.Context) error {
	if err := r.d.Wallet.Unlocked(ctx); err != nil {
		return err
	}
	var err error
	if r.net, err = r.d.Networks.Resolve(ctx, r.opts.Network); err != nil {
		return err
	}
	if err = checkJoinable(r.net); err != nil {
		return err
	}
	if r.evm, err = r.d.Wallet.EVMAddress(ctx); err != nil {
		return err
	}
	if !r.opts.ClusterOnly {
		if r.oper, err = r.d.Wallet.OramaAddress(ctx); err != nil {
			return err
		}
		if r.genesis, err = r.d.Networks.Genesis(ctx, r.net); err != nil {
			return fmt.Errorf("network %s: %w", r.net.Manifest.Name, err)
		}
	}
	plan, err := planForNetwork(r.opts, r.d, r.net)
	if err != nil {
		return err
	}
	r.plan = plan
	r.res.Plan, r.res.Env, r.res.Operator = plan, plan.Env, r.oper
	return r.confirm()
}

// planForNetwork builds the plan of opts on network n: the environment the cluster is
// recorded under (the one --env names, else the active one when it runs on this
// network, else a new <network>-<name>; <network>-cluster without a name) and
// whether it already has nodes.
func planForNetwork(opts Options, d Deps, n *netregistry.Network) (*Plan, error) {
	env := opts.Env
	if env == "" {
		env = d.Record.ActiveFor(n.Manifest.Name)
	}
	if env == "" {
		name := opts.Name
		if name == "" {
			name = "cluster"
		}
		env = n.Manifest.Name + "-" + name
	}
	return BuildPlan(PlanInput{Options: opts, Network: n.Manifest, Env: env, ExistingHosts: hostsOf(d.Record.Hosts(env))})
}

// PlanFor resolves the network and builds the plan of opts, touching no machine.
// The wizard shows it before it asks to go ahead.
func PlanFor(ctx context.Context, opts Options, d Deps) (*Plan, error) {
	if err := ResolveAnnounced(ctx, &opts, d); err != nil {
		return nil, err
	}
	if err := opts.Normalize(); err != nil {
		return nil, err
	}
	if opts.Create != nil {
		return planForCreate(ctx, opts, d)
	}
	n, err := d.Networks.Resolve(ctx, opts.Network)
	if err != nil {
		return nil, err
	}
	if err := checkJoinable(n); err != nil {
		return nil, err
	}
	return planForNetwork(opts, d, n)
}

func hostsOf(nodes []RecordedNode) []string {
	hosts := make([]string, len(nodes))
	for i, n := range nodes {
		hosts[i] = n.Host
	}
	return hosts
}

func (r *runner) confirm() error {
	for _, line := range r.plan.Summary() {
		r.d.Report.Linef("  %s", line)
	}
	if r.opts.Exit {
		r.d.Report.Linef("  ! %s", ExitWarning)
	}
	if r.opts.Yes || r.d.Confirm == nil {
		return nil
	}
	ok, err := r.d.Confirm(r.plan)
	if err != nil {
		return err
	}
	if !ok {
		return clierr.Aborted("not confirmed: nothing was changed")
	}
	return nil
}

// connect enrolls every machine and reads its facts, and refuses a machine
// below its profile's floor, all before the first install: a run that cannot
// finish does not start.
func (r *runner) connect(ctx context.Context) error {
	var refused []error
	for _, n := range r.plan.Nodes {
		r.emit(n.IP, StepEnroll, StateRunning, "")
		m, err := r.d.Enroll.Enroll(ctx, r.machineRequest(n.IP))
		if err != nil {
			r.emit(n.IP, StepEnroll, StateFailed, err.Error())
			return fmt.Errorf("machine %s: %w", n.IP, err)
		}
		r.closers = append(r.closers, m.Close)
		facts, err := m.Probe(ctx)
		if err != nil {
			r.emit(n.IP, StepEnroll, StateFailed, err.Error())
			return fmt.Errorf("machine %s: read its hardware and what it has installed: %w", n.IP, err)
		}
		r.emit(n.IP, StepEnroll, StateDone, "")
		run := &nodeRun{plan: n, m: m, facts: facts}
		r.runs = append(r.runs, run)
		if err := r.checkHardware(run); err != nil {
			refused = append(refused, fmt.Errorf("machine %s: %w", n.IP, err))
		}
	}
	return errors.Join(refused...)
}

func (r *runner) machineRequest(ip string) MachineRequest {
	hostKey := r.opts.HostKeys[ip]
	if hostKey == "" {
		hostKey = r.opts.HostKeys[""]
	}
	return MachineRequest{
		IP: ip, User: r.opts.User, HostKey: hostKey, BootstrapKey: r.opts.BootstrapKey,
		Password: r.opts.Password, UsePassword: r.opts.UsePassword, Env: r.plan.Env,
	}
}

// checkHardware refuses a machine that still has to be installed on and is below
// the floor of its profile.
func (r *runner) checkHardware(n *nodeRun) error {
	if !n.needsInstall() {
		r.skip(n.plan.IP, StepHardware, "already installed")
		return nil
	}
	if err := install.CheckHardware(n.plan.Profile, n.plan.StorageGB, n.facts.Hardware); err != nil {
		r.emit(n.plan.IP, StepHardware, StateFailed, err.Error())
		return err
	}
	r.emit(n.plan.IP, StepHardware, StateDone, fmt.Sprintf("%d vCPU, %s", n.facts.Hardware.CPUCores, gibText(n.facts.Hardware.RAMBytes)))
	return nil
}

func gibText(b uint64) string { return fmt.Sprintf("%.1f GiB RAM", float64(b)/bytesPerGiB) }

// needsInstall says there is still something to install on the machine.
func (n *nodeRun) needsInstall() bool {
	return !n.facts.ClusterInstalled || (n.plan.Full() && !n.facts.GlobalInstalled)
}
