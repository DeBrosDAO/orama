package setup

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// chainInitPhase makes the chain home and the keys of every machine that has
// none, then reads each seat: its node id, its consensus key and its account. A
// machine whose chain home exists keeps its keys; they are never made again.
func (r *runner) chainInitPhase(ctx context.Context) error {
	nodes := r.fullRuns()
	var todo []*nodeRun
	for _, n := range nodes {
		st, err := r.create.boots[n].HomeState(ctx)
		if err != nil {
			return fmt.Errorf("machine %s: read its chain home: %w", n.plan.IP, err)
		}
		r.create.homes[n] = st
		if n.facts.GlobalInstalled {
			r.skip(n.plan.IP, StepGlobal, "the chain home exists; its keys are kept")
			continue
		}
		todo = append(todo, n)
	}
	if err := r.parallel(ctx, todo, func(ctx context.Context, n *nodeRun) error { return r.initChain(ctx, n) }); err != nil {
		return err
	}
	if err := r.parallel(ctx, nodes, func(ctx context.Context, n *nodeRun) error { return r.readSeat(ctx, n) }); err != nil {
		return err
	}
	return checkSeatsDistinct(r.committee())
}

// initChain installs the global layer on a machine that has no chain unit. A
// machine whose chain home exists already, because an earlier run died inside the
// install, keeps that home and its keys: the install is finished without
// --init-chain, which refuses a home that has a genesis.
func (r *runner) initChain(ctx context.Context, n *nodeRun) error {
	ip, b := n.plan.IP, r.create.boots[n]
	var err error
	if r.create.homes[n].Genesis {
		r.emit(ip, StepGlobal, StateRunning, "finishing an install an earlier run left half done; the keys are kept")
		err = b.WireChain(ctx, WireInput{Node: n.plan, IP: ip, User: r.opts.User, Contact: r.contact()})
	} else {
		r.emit(ip, StepGlobal, StateRunning, "making the chain home and the node's keys")
		err = r.initFreshChain(ctx, n)
	}
	if err != nil {
		r.emit(ip, StepGlobal, StateFailed, err.Error())
		return fmt.Errorf("machine %s: install the global layer: %w", ip, err)
	}
	n.globalNew = true
	r.emit(ip, StepGlobal, StateDone, "")
	return nil
}

func (r *runner) initFreshChain(ctx context.Context, n *nodeRun) error {
	in := InitChainInput{Node: n.plan, IP: n.plan.IP, User: r.opts.User, ChainID: r.net.Manifest.ChainID, Contact: r.contact()}
	if n.plan.HasService(install.GlobalServiceRelay) {
		tor, err := readTorNetwork(r.opts.TorNetwork)
		if err != nil {
			return err
		}
		in.TorNetwork = tor
	}
	return r.create.boots[n].InitChain(ctx, in)
}

func (r *runner) readSeat(ctx context.Context, n *nodeRun) error {
	seat, err := r.create.boots[n].Seat(ctx)
	if err != nil {
		r.emit(n.plan.IP, StepGenesis, StateFailed, err.Error())
		return fmt.Errorf("machine %s: read the seat's keys: %w", n.plan.IP, err)
	}
	seat.Moniker = n.plan.Name
	r.create.mu.Lock()
	r.create.seats[n] = seat
	r.create.mu.Unlock()
	return nil
}

// committee is the seats in the order of the machines.
func (r *runner) committee() []Seat {
	var seats []Seat
	for _, n := range r.fullRuns() {
		seats = append(seats, r.create.seats[n])
	}
	return seats
}

// genesisPhase settles the genesis: the one the machines already carry, or a new
// one built on the first machine from every seat; checks it is the committee's
// and nothing else; gives it to every machine that does not hold it; and writes
// it for publishing before any chain starts. Publishing comes last so that a run
// that dies before every machine holds the genesis builds nothing twice: the
// genesis a machine carries is kept, and each build has a new genesis time.
func (r *runner) genesisPhase(ctx context.Context) error {
	nodes := r.fullRuns()
	for _, n := range nodes {
		st, err := r.create.boots[n].HomeState(ctx)
		if err != nil {
			return fmt.Errorf("machine %s: read its chain home: %w", n.plan.IP, err)
		}
		r.create.homes[n] = st
	}
	genesis, err := r.settleGenesis(ctx, nodes)
	if err != nil {
		return err
	}
	if err := VerifyGenesis(genesis, r.genesisSpec()); err != nil {
		return err
	}
	if err := r.distributeGenesis(ctx, nodes, genesis); err != nil {
		return err
	}
	return r.publish(genesis)
}

// genesisSpec is what the genesis of this creation is built from, and checked against.
func (r *runner) genesisSpec() GenesisSpec {
	c := r.opts.Create
	return GenesisSpec{ChainID: c.ChainID, Seats: r.committee(), TestNetwork: !c.Production(), Faucet: c.Faucet()}
}

// settleGenesis keeps the genesis a machine already carries, or builds one.
func (r *runner) settleGenesis(ctx context.Context, nodes []*nodeRun) ([]byte, error) {
	keep, err := r.finalGenesisHolder(nodes)
	if err != nil {
		return nil, err
	}
	force := r.opts.Create.ForceNewGenesis
	if keep != nil && !force {
		genesis, err := r.create.boots[keep].ReadGenesis(ctx)
		if err != nil {
			return nil, fmt.Errorf("machine %s: read the genesis it carries: %w", keep.plan.IP, err)
		}
		if got := netregistry.Digest(genesis); got != r.create.homes[keep].SHA256 {
			return nil, fmt.Errorf("machine %s: the genesis read back (sha256 %s) is not the one its chain home has (%s)", keep.plan.IP, got, r.create.homes[keep].SHA256)
		}
		if err := r.witnessKept(ctx, nodes, keep, genesis); err != nil {
			return nil, err
		}
		r.emit("", StepGenesis, StateSkipped, "the genesis the machines carry is kept (sha256 "+r.create.homes[keep].SHA256+")")
		return genesis, nil
	}
	if err := r.allowNewGenesis(nodes, keep != nil); err != nil {
		return nil, err
	}
	return r.buildGenesis(ctx, nodes)
}

// finalGenesisHolder is a machine that carries a genesis naming the committee,
// nil when there is none. Machines that carry one must carry the same one.
func (r *runner) finalGenesisHolder(nodes []*nodeRun) (*nodeRun, error) {
	var first *nodeRun
	for _, n := range nodes {
		st := r.create.homes[n]
		switch {
		case !st.Final:
		case first == nil:
			first = n
		case st.SHA256 != r.create.homes[first].SHA256:
			return nil, fmt.Errorf("machines %s and %s carry different genesis files (sha256 %s and %s): the committee was set up twice; "+
				"if no chain has run, rebuild with --force-new-genesis", first.plan.IP, n.plan.IP, r.create.homes[first].SHA256, st.SHA256)
		}
	}
	return first, nil
}

// allowNewGenesis refuses a new genesis once a chain has run on any machine: the
// genesis a chain started from is part of its history, and a machine that ran
// would not start from another. A reset network needs a new chain id.
func (r *runner) allowNewGenesis(nodes []*nodeRun, replacing bool) error {
	for _, n := range nodes {
		if r.create.homes[n].Started {
			return clierr.Conflict("the chain has already run on %s, so it cannot start from a new genesis: a reset network needs a new chain id (--chain-id) and clean machines", n.plan.IP)
		}
	}
	if replacing {
		r.d.Report.Linef("WARNING: --force-new-genesis replaces the genesis the machines carry. No chain has run, so nothing is lost; " +
			"but a genesis that was already published or shared is now wrong, and every machine must take the new one.")
	}
	return nil
}

// publish writes networks/<name>/ before any chain starts: a chain id that was
// published with another genesis is found now, not after the chain ran. Every
// machine already holds the genesis, so a refusal leaves only machines that no
// chain runs on.
func (r *runner) publish(genesis []byte) error {
	c, m := r.opts.Create, r.net.Manifest
	faucet := c.Faucet()
	published, err := netregistry.Publish(netregistry.PublishInput{
		Dir: c.PublishDir, Name: m.Name, ChainID: m.ChainID, Genesis: genesis, ReleaseRoot: r.net.Root,
		Seeds: m.Seeds, Channel: m.Channel, MinVersion: m.MinVersion, ReleaseRepo: m.ReleaseRepo, Faucet: &faucet,
	})
	if errors.Is(err, netregistry.ErrGenesisChange) {
		return clierr.Conflict("%v\n  no chain has started on the machines, so nothing is lost; every reset of a network gets a new chain id: pass another --chain-id with --force-new-genesis, or remove %s if that network was never published", err, filepath.Join(c.PublishDir, m.Name))
	}
	if err != nil {
		return fmt.Errorf("write the network's description to %s: %w", c.PublishDir, err)
	}
	r.res.Created = &CreatedNetwork{Manifest: published, Dir: filepath.Join(c.PublishDir, published.Name), Machines: r.opts.IPs, Announced: c.Announced, Domain: r.opts.Domain}
	return nil
}

// distributeGenesis gives the genesis to every machine that does not hold it and
// checks, on each, that the file there is the one given.
func (r *runner) distributeGenesis(ctx context.Context, nodes []*nodeRun, genesis []byte) error {
	want := netregistry.Digest(genesis)
	return r.parallel(ctx, nodes, func(ctx context.Context, n *nodeRun) error {
		if r.create.homes[n].SHA256 == want {
			return nil
		}
		b := r.create.boots[n]
		if err := b.PutGenesis(ctx, genesis); err != nil {
			r.emit(n.plan.IP, StepGenesis, StateFailed, err.Error())
			return fmt.Errorf("machine %s: put the genesis in its chain home: %w", n.plan.IP, err)
		}
		st, err := b.HomeState(ctx)
		if err != nil {
			r.emit(n.plan.IP, StepGenesis, StateFailed, err.Error())
			return fmt.Errorf("machine %s: read back the genesis it was given: %w", n.plan.IP, err)
		}
		if st.SHA256 != want {
			r.emit(n.plan.IP, StepGenesis, StateFailed, "the genesis on the machine is not the one sent")
			return fmt.Errorf("machine %s: the genesis in its chain home has sha256 %q after it was put there, want %s", n.plan.IP, st.SHA256, want)
		}
		r.emit(n.plan.IP, StepGenesis, StateDone, "genesis "+want)
		return nil
	})
}
