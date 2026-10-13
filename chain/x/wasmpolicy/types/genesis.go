package types

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// GenesisState is wasmpolicy's genesis. upload_sunset_height and deposit_per_byte are written
// only here. deposit_chunks is the contract state-deposit ledger (empty at a fresh genesis).
type GenesisState struct {
	UploadSunsetHeight uint64         `json:"upload_sunset_height"`
	GenesisCodeIDs     []uint64       `json:"genesis_code_ids"`
	DepositPerByte     math.Int       `json:"deposit_per_byte"`
	MaxDepositPerTx    math.Int       `json:"max_deposit_per_tx"`
	MaxReleaseRows     uint64         `json:"max_release_rows"`
	ChunkOverheadBytes uint64         `json:"chunk_overhead_bytes"`
	DepositChunks      []DepositChunk `json:"deposit_chunks"`
	// ReleaseCarry lists the bytes per contract whose deposits a shrink has not released yet.
	ReleaseCarry []ReleaseCarry `json:"release_carry"`
}

// ReleaseCarry is the bytes of Contract that a shrink freed and whose deposits wait for the contract's
// next calls to release, at most max_release_rows rows each.
type ReleaseCarry struct {
	Contract string `json:"contract"`
	Bytes    uint64 `json:"bytes"`
}

// Limits are the state-deposit bounds written once at genesis.
type Limits struct {
	MaxDepositPerTx    math.Int `json:"max_deposit_per_tx"`
	MaxReleaseRows     uint64   `json:"max_release_rows"`
	ChunkOverheadBytes uint64   `json:"chunk_overhead_bytes"`
}

// Limits returns the genesis deposit bounds.
func (gs GenesisState) Limits() Limits {
	return Limits{MaxDepositPerTx: gs.MaxDepositPerTx, MaxReleaseRows: gs.MaxReleaseRows, ChunkOverheadBytes: gs.ChunkOverheadBytes}
}

// DefaultGenesisState returns P6's sunset, an empty genesis code set, P3's per-byte price and an
// empty deposit ledger.
func DefaultGenesisState() GenesisState {
	return GenesisState{
		UploadSunsetHeight: DefaultUploadSunsetHeight,
		GenesisCodeIDs:     []uint64{},
		DepositPerByte:     math.NewInt(DefaultDepositPerByte),
		MaxDepositPerTx:    math.NewInt(DefaultMaxDepositPerTx),
		MaxReleaseRows:     DefaultMaxReleaseRows,
		ChunkOverheadBytes: DefaultChunkOverheadBytes,
		DepositChunks:      []DepositChunk{},
		ReleaseCarry:       []ReleaseCarry{},
	}
}

// Validate rejects a zero code id, duplicate code ids, a non-positive deposit_per_byte and a
// malformed or duplicated deposit chunk. Height zero is legal: it means store is open at height zero.
func (gs GenesisState) Validate() error {
	seen := make(map[uint64]struct{}, len(gs.GenesisCodeIDs))
	for _, id := range gs.GenesisCodeIDs {
		if id == 0 {
			return fmt.Errorf("genesis code id 0 is not a stored code")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("duplicate genesis code id %d", id)
		}
		seen[id] = struct{}{}
	}
	if gs.DepositPerByte.IsNil() || !gs.DepositPerByte.IsPositive() {
		return fmt.Errorf("deposit_per_byte must be a positive integer")
	}
	if gs.MaxDepositPerTx.IsNil() || !gs.MaxDepositPerTx.IsPositive() {
		return fmt.Errorf("max_deposit_per_tx must be a positive integer")
	}
	if gs.MaxReleaseRows == 0 {
		return fmt.Errorf("max_release_rows must be at least 1")
	}
	for _, c := range gs.ReleaseCarry {
		if _, err := sdk.AccAddressFromBech32(c.Contract); err != nil {
			return fmt.Errorf("release carry contract %q: %w", c.Contract, err)
		}
		if c.Bytes == 0 {
			return fmt.Errorf("release carry of %s holds zero bytes", c.Contract)
		}
	}
	chunks := make(map[string]struct{}, len(gs.DepositChunks))
	payers := make(map[string]struct{}, len(gs.DepositChunks))
	for _, c := range gs.DepositChunks {
		if err := c.Validate(); err != nil {
			return err
		}
		// Validate has parsed both; the canonical forms key the uniqueness checks, so an address
		// written in upper case cannot slip a second chunk past the one-payer-one-chunk rule.
		contract, payer := canonicalBech32(c.Contract), canonicalBech32(c.Payer)
		payerKey := contract + "/" + payer
		if _, ok := payers[payerKey]; ok {
			return fmt.Errorf("payer %s has two deposit chunks of contract %s: one payer is one chunk", c.Payer, c.Contract)
		}
		payers[payerKey] = struct{}{}
		key := fmt.Sprintf("%s/%d", contract, c.Seq)
		if _, ok := chunks[key]; ok {
			return fmt.Errorf("duplicate deposit chunk %s", key)
		}
		chunks[key] = struct{}{}
	}
	return nil
}

// canonicalBech32 is the canonical string of a bech32 account address that has already been
// validated.
func canonicalBech32(addr string) string {
	acc, err := sdk.AccAddressFromBech32(addr)
	if err != nil {
		return addr
	}
	return acc.String()
}

// DepositChunk is one state-deposit charge: Bytes of a contract's storage growth that Payer
// locked at PerByte each, under the x/fees deposit DepositID. The chunk is the unit a later
// shrink of the same contract releases, newest first.
type DepositChunk struct {
	Contract  string   `json:"contract"`
	Seq       uint64   `json:"seq"`
	Payer     string   `json:"payer"`
	Bytes     uint64   `json:"bytes"`
	Overhead  uint64   `json:"overhead"`
	PerByte   math.Int `json:"per_byte"`
	DepositID string   `json:"deposit_id"`
}

// Amount is the deposit this chunk holds: (Bytes + Overhead) * PerByte. Overhead prices the chunk's
// own ledger rows and is released only with the whole chunk.
func (c DepositChunk) Amount() math.Int {
	return math.NewIntFromUint64(c.Bytes + c.Overhead).Mul(c.PerByte)
}

// Validate checks the chunk's addresses, its size and its price.
func (c DepositChunk) Validate() error {
	if _, err := sdk.AccAddressFromBech32(c.Contract); err != nil {
		return fmt.Errorf("deposit chunk contract %q: %w", c.Contract, err)
	}
	if _, err := sdk.AccAddressFromBech32(c.Payer); err != nil {
		return fmt.Errorf("deposit chunk payer %q: %w", c.Payer, err)
	}
	if c.Bytes == 0 {
		return fmt.Errorf("deposit chunk %s/%d holds zero bytes", c.Contract, c.Seq)
	}
	if c.PerByte.IsNil() || !c.PerByte.IsPositive() {
		return fmt.Errorf("deposit chunk %s/%d per_byte must be positive", c.Contract, c.Seq)
	}
	if c.DepositID != DepositID(c.Contract, c.Seq) {
		return fmt.Errorf("deposit chunk %s/%d has deposit id %q, want %q", c.Contract, c.Seq, c.DepositID, DepositID(c.Contract, c.Seq))
	}
	return nil
}

// DepositID is the x/fees deposit id of chunk seq of contract.
func DepositID(contract string, seq uint64) string {
	return fmt.Sprintf("wasm/%s/%d", contract, seq)
}
