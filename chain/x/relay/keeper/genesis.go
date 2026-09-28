package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// InitGenesis sets x/relay's state from a GenesisState. The reporter set is
// written here and afterwards changes only through MsgUpdateReporters.
func (k Keeper) InitGenesis(ctx sdk.Context, genState types.GenesisState) error {
	if err := genState.Validate(); err != nil {
		return fmt.Errorf("invalid relay genesis state: %w", err)
	}
	if err := k.Params.Set(ctx, genState.Params); err != nil {
		return fmt.Errorf("failed to set relay params: %w", err)
	}
	reporters, err := types.NormalizeReporters(genState.Reporters, true)
	if err != nil {
		return fmt.Errorf("failed to normalize reporters: %w", err)
	}
	for _, reporter := range reporters {
		if err := k.Reporters.Set(ctx, reporter, true); err != nil {
			return fmt.Errorf("failed to set reporter %s: %w", reporter, err)
		}
	}
	for _, relay := range genState.Relays {
		if err := k.Relays.Set(ctx, relay.RsaFingerprint, relay); err != nil {
			return fmt.Errorf("failed to set relay %s: %w", relay.NodeId, err)
		}
		if err := k.NodeIndex.Set(ctx, relay.NodeId, relay.RsaFingerprint); err != nil {
			return fmt.Errorf("failed to index relay %s: %w", relay.NodeId, err)
		}
	}
	if err := k.Activation.Set(ctx, genState.Activation); err != nil {
		return fmt.Errorf("failed to set relay activation: %w", err)
	}
	for _, result := range genState.EpochResults {
		if err := k.EpochResults.Set(ctx, result.Epoch, result); err != nil {
			return fmt.Errorf("failed to set epoch %d result: %w", result.Epoch, err)
		}
	}
	for _, payout := range genState.Payouts {
		if err := k.Payouts.Set(ctx, payoutKey(payout.Epoch, payout.RsaFingerprint), payout); err != nil {
			return fmt.Errorf("failed to set epoch %d payout: %w", payout.Epoch, err)
		}
	}
	for _, chunk := range genState.Chunks {
		key := chunkKey(chunk.Epoch, chunk.Reporter, chunk.ChunkIndex)
		if err := k.Chunks.Set(ctx, key, chunk); err != nil {
			return fmt.Errorf("failed to set chunk %d for epoch %d: %w", chunk.ChunkIndex, chunk.Epoch, err)
		}
	}
	for _, report := range genState.Reports {
		if err := k.Reports.Set(ctx, reportKey(report.Epoch, report.Reporter), report); err != nil {
			return fmt.Errorf("failed to set report for epoch %d: %w", report.Epoch, err)
		}
	}
	return nil
}

// ExportGenesis reads x/relay's current state back into a GenesisState.
func (k Keeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get relay params: %w", err)
	}
	activation, err := k.Activation.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get relay activation: %w", err)
	}

	var reporters []string
	if err := k.Reporters.Walk(ctx, nil, func(addr string, _ bool) (bool, error) {
		reporters = append(reporters, addr)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk reporters: %w", err)
	}

	var relays []types.Relay
	if err := k.Relays.Walk(ctx, nil, func(_ []byte, relay types.Relay) (bool, error) {
		relays = append(relays, relay)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk relays: %w", err)
	}

	var results []types.EpochResult
	if err := k.EpochResults.Walk(ctx, nil, func(_ uint64, result types.EpochResult) (bool, error) {
		results = append(results, result)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk epoch results: %w", err)
	}

	var payouts []types.RelayPayout
	if err := k.Payouts.Walk(ctx, nil, func(_ payoutMapKey, payout types.RelayPayout) (bool, error) {
		payouts = append(payouts, payout)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk payouts: %w", err)
	}

	var chunks []types.ReportChunk
	if err := k.Chunks.Walk(ctx, nil, func(_ chunkMapKey, chunk types.ReportChunk) (bool, error) {
		chunks = append(chunks, chunk)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk chunks: %w", err)
	}

	var reports []types.CompleteReport
	if err := k.Reports.Walk(ctx, nil, func(_ reportMapKey, report types.CompleteReport) (bool, error) {
		reports = append(reports, report)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk reports: %w", err)
	}

	if reporters == nil {
		reporters = []string{}
	}
	if relays == nil {
		relays = []types.Relay{}
	}
	if results == nil {
		results = []types.EpochResult{}
	}
	if payouts == nil {
		payouts = []types.RelayPayout{}
	}
	if chunks == nil {
		chunks = []types.ReportChunk{}
	}
	if reports == nil {
		reports = []types.CompleteReport{}
	}

	return &types.GenesisState{
		Params:       params,
		Reporters:    reporters,
		Relays:       relays,
		Activation:   activation,
		EpochResults: results,
		Payouts:      payouts,
		Chunks:       chunks,
		Reports:      reports,
	}, nil
}
