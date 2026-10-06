package keeper

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func (k Keeper) reportEpoch(ctx sdk.Context, msg *types.MsgReportEpoch) (bool, error) {
	reporter, err := types.CanonicalAddress(msg.Reporter)
	if err != nil {
		return false, fmt.Errorf("report epoch: %w", err)
	}
	isReporter, err := k.isReporter(ctx, reporter)
	if err != nil {
		return false, err
	}
	if !isReporter {
		return false, fmt.Errorf("report epoch: %w", types.ErrNotReporter)
	}
	if msg.Epoch == 0 {
		return false, fmt.Errorf("report epoch: epoch must be positive")
	}
	settled, err := k.EpochResults.Has(ctx, msg.Epoch)
	if err != nil {
		return false, fmt.Errorf("report epoch: failed to check epoch %d: %w", msg.Epoch, err)
	}
	if settled {
		return false, fmt.Errorf("report epoch: epoch %d is already settled", msg.Epoch)
	}
	if msg.ChunkCount == 0 || msg.ChunkCount > types.MaxChunkCount {
		return false, fmt.Errorf("report epoch: chunk count must be in 1..%d", types.MaxChunkCount)
	}
	if msg.ChunkIndex >= msg.ChunkCount {
		return false, fmt.Errorf("report epoch: chunk index %d is out of range", msg.ChunkIndex)
	}
	if len(msg.InputsRoot) != types.InputsRootLen {
		return false, fmt.Errorf("report epoch: %w", types.ErrInputsRootMismatch)
	}
	if len(msg.Entries) > types.MaxEntriesPerChunk {
		return false, fmt.Errorf("report epoch: chunk has %d entries, max %d", len(msg.Entries), types.MaxEntriesPerChunk)
	}
	seen := make(map[string]struct{}, len(msg.Entries))
	for i, entry := range msg.Entries {
		if err := entry.Validate(); err != nil {
			return false, fmt.Errorf("report epoch: entry %d: %w", i, err)
		}
		if err := k.matchRegistered(ctx, entry); err != nil {
			return false, fmt.Errorf("report epoch: entry %d: %w", i, err)
		}
		fp := string(entry.RsaFingerprint)
		if _, ok := seen[fp]; ok {
			return false, fmt.Errorf("report epoch: duplicate rsa fingerprint in chunk")
		}
		seen[fp] = struct{}{}
	}

	existing, err := k.Reports.Get(ctx, reportKey(msg.Epoch, reporter))
	if err == nil {
		if bytes.Equal(existing.InputsRoot, msg.InputsRoot) {
			return true, nil
		}
		return false, fmt.Errorf("report epoch: %w", types.ErrInputsRootMismatch)
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return false, fmt.Errorf("report epoch: failed to load report: %w", err)
	}

	key := chunkKey(msg.Epoch, reporter, msg.ChunkIndex)
	prev, err := k.Chunks.Get(ctx, key)
	if err == nil {
		if sameChunk(prev, msg) {
			return false, nil
		}
		return false, fmt.Errorf("report epoch: conflicting chunk %d for epoch %d", msg.ChunkIndex, msg.Epoch)
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return false, fmt.Errorf("report epoch: failed to load chunk: %w", err)
	}

	siblings, err := k.chunksFor(ctx, msg.Epoch, reporter)
	if err != nil {
		return false, err
	}
	for _, sibling := range siblings {
		if sibling.ChunkCount != msg.ChunkCount || !bytes.Equal(sibling.InputsRoot, msg.InputsRoot) {
			return false, fmt.Errorf("report epoch: chunk metadata does not match the rest of the report")
		}
	}

	incoming := types.ReportChunk{
		Epoch:      msg.Epoch,
		Reporter:   reporter,
		ChunkIndex: msg.ChunkIndex,
		ChunkCount: msg.ChunkCount,
		InputsRoot: append([]byte(nil), msg.InputsRoot...),
		Entries:    msg.Entries,
	}
	if uint32(len(siblings)+1) < msg.ChunkCount {
		if err := k.Chunks.Set(ctx, key, incoming); err != nil {
			return false, fmt.Errorf("report epoch: failed to store chunk: %w", err)
		}
		return false, nil
	}

	assembled := append(append([]types.ReportChunk{}, siblings...), incoming)
	entries, err := reassemble(assembled)
	if err != nil {
		// Drop the partial report so a corrected resubmission is not stuck
		// behind a chunk that can no longer be replaced.
		if delErr := k.deleteChunks(ctx, msg.Epoch, reporter); delErr != nil {
			return false, delErr
		}
		return false, err
	}
	root, err := types.InputsRoot(entries)
	if err != nil {
		return false, fmt.Errorf("report epoch: %w", err)
	}
	if !bytes.Equal(root, msg.InputsRoot) {
		if err := k.deleteChunks(ctx, msg.Epoch, reporter); err != nil {
			return false, err
		}
		return false, fmt.Errorf("report epoch: %w", types.ErrInputsRootMismatch)
	}
	report := types.CompleteReport{
		Epoch:      msg.Epoch,
		Reporter:   reporter,
		InputsRoot: append([]byte(nil), root...),
		Entries:    entries,
	}
	if err := k.Reports.Set(ctx, reportKey(msg.Epoch, reporter), report); err != nil {
		return false, fmt.Errorf("report epoch: failed to store report: %w", err)
	}
	if err := k.deleteChunks(ctx, msg.Epoch, reporter); err != nil {
		return false, err
	}
	return true, nil
}

func (k Keeper) matchRegistered(ctx sdk.Context, entry types.RelayObservation) error {
	relay, found, err := k.getRelay(ctx, entry.RsaFingerprint)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("relay is not registered")
	}
	if !bytes.Equal(relay.Ed25519Id, entry.Ed25519Id) {
		return types.ErrEd25519Mismatch
	}
	return nil
}

func (k Keeper) chunksFor(ctx sdk.Context, epoch uint64, reporter string) ([]types.ReportChunk, error) {
	var chunks []types.ReportChunk
	err := k.Chunks.Walk(ctx, collections.NewSuperPrefixedTripleRange[uint64, string, uint32](epoch, reporter), func(_ chunkMapKey, chunk types.ReportChunk) (bool, error) {
		chunks = append(chunks, chunk)
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("report epoch: failed to walk chunks: %w", err)
	}
	return chunks, nil
}

func (k Keeper) deleteChunks(ctx sdk.Context, epoch uint64, reporter string) error {
	chunks, err := k.chunksFor(ctx, epoch, reporter)
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if err := k.Chunks.Remove(ctx, chunkKey(chunk.Epoch, chunk.Reporter, chunk.ChunkIndex)); err != nil {
			return fmt.Errorf("report epoch: failed to delete chunk %d: %w", chunk.ChunkIndex, err)
		}
	}
	return nil
}

func reassemble(chunks []types.ReportChunk) ([]types.RelayObservation, error) {
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].ChunkIndex < chunks[j].ChunkIndex })
	var entries []types.RelayObservation
	seen := map[string]struct{}{}
	for i, chunk := range chunks {
		if chunk.ChunkIndex != uint32(i) {
			return nil, fmt.Errorf("report epoch: missing chunk %d", i)
		}
		for _, entry := range chunk.Entries {
			fp := string(entry.RsaFingerprint)
			if _, ok := seen[fp]; ok {
				return nil, fmt.Errorf("report epoch: duplicate rsa fingerprint")
			}
			seen[fp] = struct{}{}
			entries = append(entries, entry)
		}
	}
	if len(entries) > types.MaxEntriesPerReport() {
		return nil, fmt.Errorf("report epoch: report has %d entries, max %d", len(entries), types.MaxEntriesPerReport())
	}
	return entries, nil
}

func sameChunk(stored types.ReportChunk, msg *types.MsgReportEpoch) bool {
	if stored.ChunkIndex != msg.ChunkIndex || stored.ChunkCount != msg.ChunkCount || !bytes.Equal(stored.InputsRoot, msg.InputsRoot) {
		return false
	}
	if len(stored.Entries) != len(msg.Entries) {
		return false
	}
	for i := range stored.Entries {
		if !sameObservation(stored.Entries[i], msg.Entries[i]) {
			return false
		}
	}
	return true
}

func sameObservation(left, right types.RelayObservation) bool {
	return bytes.Equal(left.RsaFingerprint, right.RsaFingerprint) &&
		bytes.Equal(left.Ed25519Id, right.Ed25519Id) &&
		left.ConsensusWeight.Equal(right.ConsensusWeight) &&
		left.Flags == right.Flags &&
		left.UptimeFraction.Equal(right.UptimeFraction)
}
