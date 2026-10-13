package setup

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// witnessKept asks a machine other than the one that supplied the genesis to
// build it, and refuses a genesis that is not what these options build. A machine
// that says it carries a genesis is believed no more than one that builds it. With
// one machine there is no other to ask, and once a chain has run on any machine the
// genesis is history: every machine already runs on it, a witness could only fail
// on a release newer than the one that made it, and nothing can be replaced.
func (r *runner) witnessKept(ctx context.Context, nodes []*nodeRun, holder *nodeRun, kept []byte) error {
	for _, n := range nodes {
		if r.create.homes[n].Started {
			return nil
		}
	}
	var witness *nodeRun
	for _, n := range nodes {
		if n != holder {
			witness = n
			break
		}
	}
	if witness == nil {
		return nil
	}
	raw, err := r.buildOn(ctx, witness, r.genesisSpec())
	if err != nil {
		return err
	}
	built, err := ApplyConsensusParams(raw)
	if err != nil {
		return err
	}
	if err := sameGenesis(kept, built); err != nil {
		return fmt.Errorf("the genesis on %s is not the one %s builds from these options: %w\n"+
			"  if the options changed since the genesis was made and no chain has run, build a new one with --force-new-genesis", holder.plan.IP, witness.plan.IP, err)
	}
	return nil
}

// buildGenesis builds the genesis on the first machine and, when there is a
// second, again on the second, and refuses the pair unless they are the same
// document but for the time they were made at. VerifyGenesis reads what a genesis
// must say about the committee, but a builder can fill the rest of it (a scheduled
// upgrade, a relay reporter, a parameter); a second machine running the same
// commands is a witness to all of it, so a lie needs two machines in it.
func (r *runner) buildGenesis(ctx context.Context, nodes []*nodeRun) ([]byte, error) {
	spec := r.genesisSpec()
	r.emit("", StepGenesis, StateRunning, fmt.Sprintf("building the genesis of %s on %s from %d seats", spec.ChainID, nodes[0].plan.IP, len(spec.Seats)))
	raw, err := r.buildOn(ctx, nodes[0], spec)
	if err != nil {
		return nil, err
	}
	if len(nodes) > 1 {
		witness, err := r.buildOn(ctx, nodes[1], spec)
		if err != nil {
			return nil, err
		}
		if err := sameGenesis(raw, witness); err != nil {
			r.emit("", StepGenesis, StateFailed, err.Error())
			return nil, fmt.Errorf("the genesis built on %s is not the one built on %s: %w", nodes[0].plan.IP, nodes[1].plan.IP, err)
		}
	}
	genesis, err := ApplyConsensusParams(raw)
	if err != nil {
		r.emit("", StepGenesis, StateFailed, err.Error())
		return nil, err
	}
	r.emit("", StepGenesis, StateDone, "sha256 "+netregistry.Digest(genesis))
	return genesis, nil
}

func (r *runner) buildOn(ctx context.Context, n *nodeRun, spec GenesisSpec) ([]byte, error) {
	raw, err := r.create.boots[n].BuildGenesis(ctx, GenesisSteps(spec))
	if err != nil {
		r.emit("", StepGenesis, StateFailed, err.Error())
		return nil, fmt.Errorf("machine %s: build the genesis: %w", n.plan.IP, err)
	}
	if err := rejectAmbiguousKeys(raw); err != nil {
		r.emit("", StepGenesis, StateFailed, err.Error())
		return nil, fmt.Errorf("machine %s: the genesis it built is not the JSON oramad writes: %w", n.plan.IP, err)
	}
	return raw, nil
}
