package app

import (
	"errors"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Conservation: the chain checks, inside the block, that no coin was created or lost.
//
// Until this check existed every invariant of the chain was a gRPC query somebody had to ask for
// (x/token's CheckInvariants, x/fees', and so on). There is no crisis module in SDK 0.54, and the only
// in-block reconciliation, x/emission's ReconcileBurns, treated any drop in the norama supply as a burn
// and did nothing about a rise. A bug that minted norama outside x/emission, or moved coins out of a
// module account without moving its ledger, would have run on every validator, unseen, until an
// operator ran a query.
//
// Two checks run at the end of every block, after every module's EndBlock, so the state they read is
// the state that is about to be committed:
//
//   - every block, O(1): the norama supply equals the ledger x/emission keeps (genesis supply plus
//     every mint it recorded minus every burn it reconciled), and cumulative_minted is the schedule's.
//     This is the supply-direction rule: norama is created only by x/emission's mint paths (Minter
//     permission on x/emission alone, TestModuleAccountPermissions), and destroyed only by a module
//     holding Burner, whose drop ReconcileBurns books as a burn in the same block.
//   - every min_blocks_per_epoch blocks (a production epoch's floor, 14,400 blocks, about a day; every
//     block on a test chain whose epochs are one block long), O(accounts): the full pass in
//     CheckConservationFull.
//
// A failed check returns an error from EndBlocker. FinalizeBlock then fails on every validator that
// computed it, which is a halt: the chain stops at the block that broke the rule instead of committing
// it. Every input is committed state (store reads in key order, integer arithmetic, no time, no map
// iteration), so all validators reach the same verdict at the same height.
//
// The full pass iterates every balance once, so its cost grows with the number of accounts: the pass
// time is linear in the account count (benchmarked by BenchmarkConservationFull), and it stalls the
// one block that runs it. If accounts reach the point where that stall is too long, raise the interval
// (a single constant, fullPassBlocks) rather than dropping the pass.

// fullPassBlocks is how many blocks apart two full passes are: the emission parameter
// min_blocks_per_epoch, so a production chain runs it about once a day and a test chain with
// one-block epochs runs it every block.
func (app *OramaApp) fullPassBlocks(ctx sdk.Context) (uint64, error) {
	p, err := app.EmissionKeeper.Params.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to read the emission params: %w", err)
	}
	if p.MinBlocksPerEpoch == 0 {
		return 1, nil
	}
	return p.MinBlocksPerEpoch, nil
}

// checkConservation is the end-of-block check. It returns an error when the block must not commit.
func (app *OramaApp) checkConservation(ctx sdk.Context) error {
	if err := app.EmissionKeeper.AssertSupplyConserved(ctx); err != nil {
		return fmt.Errorf("conservation violated at height %d: %w", ctx.BlockHeight(), err)
	}
	every, err := app.fullPassBlocks(ctx)
	if err != nil {
		return err
	}
	if uint64(ctx.BlockHeight())%every != 0 {
		return nil
	}
	if err := app.CheckConservationFull(ctx); err != nil {
		return fmt.Errorf("conservation violated at height %d: %w", ctx.BlockHeight(), err)
	}
	return nil
}

// conservationCheck is one named check of the full pass.
type conservationCheck struct {
	name string
	run  func(ctx sdk.Context) error
}

// CheckConservationFull runs every check of the full pass, in a fixed order, and returns one error
// naming each that failed. The export of a genesis runs it too: a state that breaks conservation must
// not be written out as the start of a new chain.
func (app *OramaApp) CheckConservationFull(ctx sdk.Context) error {
	checks := []conservationCheck{
		{"emission supply", app.EmissionKeeper.AssertSupplyConserved},
		{"bank balances", app.checkBankConservation},
		{"staking pools", app.checkStakingPools},
	}
	checks = append(checks, app.moduleLedgerChecks()...)

	var failures []error
	for _, c := range checks {
		if err := c.run(ctx); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
		}
	}
	return errors.Join(failures...)
}
