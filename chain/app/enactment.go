package app

import (
	"context"
	"fmt"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	upgradekeeper "github.com/cosmos/cosmos-sdk/x/upgrade/keeper"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"

	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	houseskeeper "github.com/DeBrosOfficial/network/chain/x/houses/keeper"
	housetypes "github.com/DeBrosOfficial/network/chain/x/houses/types"
	relaykeeper "github.com/DeBrosOfficial/network/chain/x/relay/keeper"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// This file wires x/houses' enacted outcomes to the modules that act on them
// (docs/CHAIN.md, "Governance enactment"):
//   - x/emission reads the emission split when it closes an epoch;
//   - x/houses changes x/relay's reporter set through x/relay's own guarded message path;
//   - x/houses schedules a passed software upgrade as an x/upgrade plan;
//   - x/wasmpolicy reads the code-upload allow-list (wired in wasm_policy.go).

// emissionSplitSource reports the split x/houses enacted, or the canonical split.
type emissionSplitSource struct {
	houses houseskeeper.Keeper
}

func (s emissionSplitSource) EmissionSplit(ctx context.Context) (emissiontypes.SplitPercents, error) {
	enacted, err := s.houses.EnactedEmissionSplit(ctx)
	if err != nil {
		return emissiontypes.SplitPercents{}, err
	}
	if enacted == nil {
		return emissiontypes.CanonicalSplitPercents(), nil
	}
	return emissiontypes.SplitPercents{
		Validator:   enacted.ValidatorPercent,
		Storage:     enacted.StoragePercent,
		Relay:       enacted.RelayPercent,
		Development: enacted.DevelopmentPercent,
	}, nil
}

// relayReporterEnactor applies a passed reporter change through x/relay's
// MsgUpdateReporters handler, which refuses unless the context carries the flag only this
// adapter sets (relaykeeper.WithAllowReporterChange). The signer is the houses module account:
// it is not an authority, x/relay ignores it beyond address validation.
type relayReporterEnactor struct {
	relay relaykeeper.Keeper
}

func (r relayReporterEnactor) ChangeReporters(ctx sdk.Context, add, remove []string) error {
	current := map[string]struct{}{}
	err := r.relay.Reporters.Walk(ctx, nil, func(addr string, _ bool) (bool, error) {
		current[addr] = struct{}{}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("failed to read the reporter set: %w", err)
	}
	for _, addr := range remove {
		canonical, err := relaytypes.CanonicalAddress(addr)
		if err != nil {
			return err
		}
		delete(current, canonical)
	}
	for _, addr := range add {
		canonical, err := relaytypes.CanonicalAddress(addr)
		if err != nil {
			return err
		}
		current[canonical] = struct{}{}
	}
	next := make([]string, 0, len(current))
	for addr := range current {
		next = append(next, addr)
	}
	sort.Strings(next)

	signer := authtypes.NewModuleAddress(housetypes.ModuleName).String()
	msg := &relaytypes.MsgUpdateReporters{Signer: signer, Reporters: next}
	_, err = relaykeeper.NewMsgServerImpl(r.relay).UpdateReporters(relaykeeper.WithAllowReporterChange(ctx), msg)
	return err
}

// upgradeScheduler stores a passed software upgrade as an x/upgrade plan. At the plan
// height x/upgrade's PreBlocker runs the registered handler, or halts the node with
// "UPGRADE NEEDED" so its operator (cosmovisor) swaps the binary.
type upgradeScheduler struct {
	upgrades *upgradekeeper.Keeper
}

func (u upgradeScheduler) ScheduleUpgrade(ctx sdk.Context, name string, height int64) error {
	return u.upgrades.ScheduleUpgrade(ctx, upgradetypes.Plan{Name: name, Height: height})
}
