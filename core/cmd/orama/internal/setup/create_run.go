package setup

import (
	"context"
	"fmt"
	"sync"
)

// createState is what a creation keeps between its phases.
type createState struct {
	boots map[*nodeRun]Bootstrapper
	// homes is the chain home of each machine, as the genesis phase found it.
	homes map[*nodeRun]HomeState
	// seats is filled by machines in parallel.
	mu    sync.Mutex
	seats map[*nodeRun]Seat
	// torRuns are the machines that run a Tor role, authorities first, and tor
	// finds one by its machine; both are empty when the run makes no Tor network.
	torRuns []*torRun
	tor     map[*nodeRun]*torRun
}

// runCreate creates the network: the same machines, release and cluster as a
// join, then the chain from nothing. The seats' keys are made first, the
// genesis is built from them on the first machine and handed to the others, the
// chains are started one at a time, and the on-chain registration is the join's.
// The network's own Tor network (torPhase) comes last, because it takes hours and
// nothing else waits for it. Every phase asks the machines what they already
// have, so a run that stopped is run again as it was.
func (r *runner) runCreate(ctx context.Context) (*Result, error) {
	if err := r.createPreflight(ctx); err != nil {
		return nil, err
	}
	if err := r.connect(ctx); err != nil {
		return nil, err
	}
	if err := r.bootstrappers(); err != nil {
		return nil, err
	}
	if err := r.stageReleases(ctx); err != nil {
		return nil, err
	}
	if err := r.clusterPhase(ctx); err != nil {
		return nil, err
	}
	r.announceAllNameservers(ctx)
	for _, phase := range []func(context.Context) error{
		r.chainInitPhase, r.genesisPhase, r.chainStartPhase, r.restartPhase, r.epochPhase, r.onchainPhase,
	} {
		if err := phase(ctx); err != nil {
			return nil, err
		}
	}
	if err := r.waitDomain(ctx); err != nil {
		return nil, err
	}
	if err := r.recordOperator(); err != nil {
		return nil, err
	}
	// The Tor network comes after everything the network is usable without, and a failure
	// in it still returns the result: the network is created and published, and the caller
	// prints what makes it joinable beside the error.
	if err := r.torPhase(ctx); err != nil {
		return r.res, fmt.Errorf("the network is created and its description is written, but its Tor network is not finished: %w\n"+
			"  run the same command again to finish it: it resumes where it stopped", err)
	}
	return r.res, nil
}

// createPreflight checks everything that needs no machine: the wallet, the
// release root, the manifest as far as it is known, and the plan the operator
// confirms.
func (r *runner) createPreflight(ctx context.Context) error {
	if err := r.d.Wallet.Unlocked(ctx); err != nil {
		return err
	}
	var env string
	var err error
	if r.net, env, err = createNetwork(r.opts); err != nil {
		return err
	}
	if r.evm, err = r.d.Wallet.EVMAddress(ctx); err != nil {
		return err
	}
	if r.oper, err = r.d.Wallet.OramaAddress(ctx); err != nil {
		return err
	}
	if err = checkRecordedHosts(env, r.d.Record.Hosts(env), r.opts.IPs); err != nil {
		return err
	}
	if r.plan, err = BuildCreatePlan(r.opts, r.net.Manifest, env); err != nil {
		return err
	}
	r.res.Plan, r.res.Env, r.res.Operator = r.plan, env, r.oper
	r.create = &createState{boots: map[*nodeRun]Bootstrapper{}, seats: map[*nodeRun]Seat{}, homes: map[*nodeRun]HomeState{}, tor: map[*nodeRun]*torRun{}}
	return r.confirm()
}

// bootstrappers checks every machine can be a seat. Those setup enrols over SSH
// all can; a machine that cannot is a bug of the Enroller, named before anything
// is installed.
func (r *runner) bootstrappers() error {
	for _, n := range r.runs {
		b, ok := n.m.(Bootstrapper)
		if !ok {
			return fmt.Errorf("machine %s cannot be a seat of a new network: its connection (%T) does not make a chain home or build a genesis", n.plan.IP, n.m)
		}
		r.create.boots[n] = b
	}
	var err error
	r.create.torRuns, err = r.torRuns()
	return err
}
