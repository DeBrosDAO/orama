package types

import (
	"bytes"
	"fmt"

	"cosmossdk.io/math"
)

// DefaultGenesisState returns an empty relay module: placeholder params, no
// reporters, rewards not yet active. A production genesis writes the dirauth
// reporter set into Reporters.
func DefaultGenesisState() *GenesisState {
	return &GenesisState{
		Params:       DefaultParams(),
		Reporters:    []string{},
		Relays:       []Relay{},
		Activation:   Activation{},
		EpochResults: []EpochResult{},
		Payouts:      []RelayPayout{},
		Chunks:       []ReportChunk{},
		Reports:      []CompleteReport{},
	}
}

// Validate performs genesis-state sanity checks.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	reporters, err := NormalizeReporters(gs.Reporters, true)
	if err != nil {
		return err
	}

	if err := gs.Activation.validate(); err != nil {
		return err
	}
	relays, err := validateRelays(gs.Relays)
	if err != nil {
		return err
	}
	for _, reporter := range reporters {
		for _, relay := range gs.Relays {
			if relay.Operator == reporter {
				return fmt.Errorf("reporter %s operates relay %s: %w", reporter, relay.NodeId, ErrReporterOperatesRelay)
			}
		}
	}
	results, err := validateEpochResults(gs.EpochResults, gs.Activation)
	if err != nil {
		return err
	}
	if err := validatePayouts(gs.Payouts, results); err != nil {
		return err
	}
	if err := validateChunks(gs.Chunks, relays); err != nil {
		return err
	}
	return validateReports(gs.Reports, gs.Chunks, relays)
}

func (a Activation) validate() error {
	if a.Active {
		if a.ActivationEpoch == 0 {
			return fmt.Errorf("activation_epoch must be set once rewards are active")
		}
		return nil
	}
	if a.ActivationEpoch != 0 {
		return fmt.Errorf("activation_epoch must be 0 before rewards activate")
	}
	return nil
}

func validateRelays(relays []Relay) (map[string]Relay, error) {
	byFP := make(map[string]Relay, len(relays))
	nodes := make(map[string]struct{}, len(relays))
	for _, relay := range relays {
		if relay.NodeId == "" {
			return nil, fmt.Errorf("relay has an empty node id")
		}
		if len(relay.RsaFingerprint) != RSAFingerprintLen {
			return nil, fmt.Errorf("relay %s fingerprint must be %d bytes", relay.NodeId, RSAFingerprintLen)
		}
		if len(relay.Ed25519Id) != Ed25519PubLen {
			return nil, fmt.Errorf("relay %s ed25519 id must be %d bytes", relay.NodeId, Ed25519PubLen)
		}
		operator, err := CanonicalAddress(relay.Operator)
		if err != nil {
			return nil, fmt.Errorf("relay %s: %w", relay.NodeId, err)
		}
		if operator != relay.Operator {
			return nil, fmt.Errorf("relay %s operator must be canonical", relay.NodeId)
		}
		canonical := relay.Prefix16
		if relay.Prefix16 != UnidentifiedPrefix16 {
			canonical, err = CanonicalPrefix16(relay.Prefix16)
			if err != nil {
				return nil, fmt.Errorf("relay %s: %w", relay.NodeId, err)
			}
		}
		if canonical != relay.Prefix16 {
			return nil, fmt.Errorf("relay %s prefix16 must be canonical, got %q", relay.NodeId, relay.Prefix16)
		}
		fp := string(relay.RsaFingerprint)
		if _, ok := byFP[fp]; ok {
			return nil, fmt.Errorf("duplicate relay fingerprint")
		}
		if _, ok := nodes[relay.NodeId]; ok {
			return nil, fmt.Errorf("duplicate relay node id %q", relay.NodeId)
		}
		byFP[fp] = relay
		nodes[relay.NodeId] = struct{}{}
	}
	return byFP, nil
}

func validateEpochResults(results []EpochResult, activation Activation) (map[uint64]EpochResult, error) {
	byEpoch := make(map[uint64]EpochResult, len(results))
	var activating uint64
	for _, result := range results {
		if result.Epoch == 0 {
			return nil, fmt.Errorf("epoch result has epoch 0")
		}
		if _, ok := byEpoch[result.Epoch]; ok {
			return nil, fmt.Errorf("duplicate epoch result %d", result.Epoch)
		}
		if result.Ceiling.IsNil() || result.Ceiling.IsNegative() || result.Minted.IsNil() || result.Minted.IsNegative() {
			return nil, fmt.Errorf("epoch %d ceiling and minted must be non-negative", result.Epoch)
		}
		if result.Minted.GT(result.Ceiling) {
			return nil, fmt.Errorf("epoch %d minted %s exceeds ceiling %s", result.Epoch, result.Minted, result.Ceiling)
		}
		if result.Activating {
			if activating != 0 {
				return nil, fmt.Errorf("more than one activating epoch")
			}
			activating = result.Epoch
			if !result.QuorumMet || !result.Minted.IsZero() {
				return nil, fmt.Errorf("activating epoch %d must meet quorum and mint nothing", result.Epoch)
			}
			if !activation.Active || activation.ActivationEpoch != result.Epoch {
				return nil, fmt.Errorf("activating epoch %d does not match activation state", result.Epoch)
			}
		}
		if !result.QuorumMet && !result.Minted.IsZero() {
			return nil, fmt.Errorf("epoch %d minted without quorum", result.Epoch)
		}
		byEpoch[result.Epoch] = result
	}
	return byEpoch, nil
}

func validatePayouts(payouts []RelayPayout, results map[uint64]EpochResult) error {
	sums := make(map[uint64]math.Int, len(results))
	for epoch, result := range results {
		sums[epoch] = math.ZeroInt()
		_ = result
	}
	seen := make(map[string]struct{}, len(payouts))
	for _, payout := range payouts {
		result, ok := results[payout.Epoch]
		if !ok {
			return fmt.Errorf("payout for unknown epoch %d", payout.Epoch)
		}
		if result.Activating || !result.QuorumMet {
			return fmt.Errorf("epoch %d has a payout but did not mint", payout.Epoch)
		}
		if len(payout.RsaFingerprint) != RSAFingerprintLen {
			return fmt.Errorf("epoch %d payout fingerprint must be %d bytes", payout.Epoch, RSAFingerprintLen)
		}
		if _, err := CanonicalAddress(payout.Operator); err != nil {
			return fmt.Errorf("epoch %d payout: %w", payout.Epoch, err)
		}
		if payout.Amount.IsNil() || !payout.Amount.IsPositive() {
			return fmt.Errorf("epoch %d payout amount must be positive", payout.Epoch)
		}
		key := fmt.Sprintf("%d/%x", payout.Epoch, payout.RsaFingerprint)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate payout %s", key)
		}
		seen[key] = struct{}{}
		sums[payout.Epoch] = sums[payout.Epoch].Add(payout.Amount)
	}
	for epoch, result := range results {
		if !sums[epoch].Equal(result.Minted) {
			return fmt.Errorf("epoch %d payouts %s != minted %s", epoch, sums[epoch], result.Minted)
		}
	}
	return nil
}

func validateChunks(chunks []ReportChunk, relays map[string]Relay) error {
	type meta struct {
		count uint32
		root  []byte
		seen  map[uint32]struct{}
	}
	groups := map[string]*meta{}
	for _, chunk := range chunks {
		if err := validateChunkShape(chunk, relays); err != nil {
			return err
		}
		key := fmt.Sprintf("%d/%s", chunk.Epoch, chunk.Reporter)
		group, ok := groups[key]
		if !ok {
			group = &meta{count: chunk.ChunkCount, root: chunk.InputsRoot, seen: map[uint32]struct{}{}}
			groups[key] = group
		}
		if group.count != chunk.ChunkCount || !bytes.Equal(group.root, chunk.InputsRoot) {
			return fmt.Errorf("chunks for epoch %d reporter %s disagree on metadata", chunk.Epoch, chunk.Reporter)
		}
		if _, ok := group.seen[chunk.ChunkIndex]; ok {
			return fmt.Errorf("duplicate chunk %d for epoch %d reporter %s", chunk.ChunkIndex, chunk.Epoch, chunk.Reporter)
		}
		group.seen[chunk.ChunkIndex] = struct{}{}
	}
	return nil
}

func validateChunkShape(chunk ReportChunk, relays map[string]Relay) error {
	if chunk.Epoch == 0 || chunk.Epoch > MaxReportEpoch {
		return fmt.Errorf("chunk epoch %d must be in [1, %d]", chunk.Epoch, MaxReportEpoch)
	}
	if _, err := CanonicalAddress(chunk.Reporter); err != nil {
		return fmt.Errorf("chunk: %w", err)
	}
	if chunk.ChunkCount == 0 || chunk.ChunkCount > MaxChunkCount || chunk.ChunkIndex >= chunk.ChunkCount {
		return fmt.Errorf("chunk index %d count %d is out of range", chunk.ChunkIndex, chunk.ChunkCount)
	}
	if len(chunk.InputsRoot) != InputsRootLen {
		return fmt.Errorf("chunk inputs_root must be %d bytes", InputsRootLen)
	}
	if len(chunk.Entries) > MaxEntriesPerChunk {
		return fmt.Errorf("chunk has %d entries, max %d", len(chunk.Entries), MaxEntriesPerChunk)
	}
	return validateEntries(chunk.Entries, relays)
}

func validateReports(reports []CompleteReport, chunks []ReportChunk, relays map[string]Relay) error {
	pending := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		pending[fmt.Sprintf("%d/%s", chunk.Epoch, chunk.Reporter)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(reports))
	for _, report := range reports {
		if report.Epoch == 0 || report.Epoch > MaxReportEpoch {
			return fmt.Errorf("report epoch %d must be in [1, %d]", report.Epoch, MaxReportEpoch)
		}
		canonical, err := CanonicalAddress(report.Reporter)
		if err != nil {
			return fmt.Errorf("report: %w", err)
		}
		if canonical != report.Reporter {
			return fmt.Errorf("report reporter must be canonical")
		}
		key := fmt.Sprintf("%d/%s", report.Epoch, report.Reporter)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate report for epoch %d reporter %s", report.Epoch, report.Reporter)
		}
		seen[key] = struct{}{}
		if _, ok := pending[key]; ok {
			return fmt.Errorf("epoch %d reporter %s has both chunks and a complete report", report.Epoch, report.Reporter)
		}
		if len(report.Entries) > MaxEntriesPerReport() {
			return fmt.Errorf("report has %d entries, max %d", len(report.Entries), MaxEntriesPerReport())
		}
		if err := validateEntries(report.Entries, relays); err != nil {
			return fmt.Errorf("report epoch %d: %w", report.Epoch, err)
		}
		root, err := InputsRoot(report.Entries)
		if err != nil {
			return fmt.Errorf("report epoch %d: %w", report.Epoch, err)
		}
		if !bytes.Equal(root, report.InputsRoot) {
			return fmt.Errorf("report epoch %d reporter %s: %w", report.Epoch, report.Reporter, ErrInputsRootMismatch)
		}
	}
	return nil
}

func validateEntries(entries []RelayObservation, relays map[string]Relay) error {
	seen := make(map[string]struct{}, len(entries))
	for i, entry := range entries {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("entry %d: %w", i, err)
		}
		fp := string(entry.RsaFingerprint)
		if _, ok := seen[fp]; ok {
			return fmt.Errorf("entry %d duplicates a fingerprint", i)
		}
		seen[fp] = struct{}{}
		relay, ok := relays[fp]
		if !ok {
			return fmt.Errorf("entry %d relay is not registered", i)
		}
		if !bytes.Equal(relay.Ed25519Id, entry.Ed25519Id) {
			return fmt.Errorf("entry %d: %w", i, ErrEd25519Mismatch)
		}
	}
	return nil
}
