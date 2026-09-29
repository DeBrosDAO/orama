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
	DepositChunks      []DepositChunk `json:"deposit_chunks"`
}

// DefaultGenesisState returns P6's sunset, an empty genesis code set, P3's per-byte price and an
// empty deposit ledger.
func DefaultGenesisState() GenesisState {
	return GenesisState{
		UploadSunsetHeight: DefaultUploadSunsetHeight,
		GenesisCodeIDs:     []uint64{},
		DepositPerByte:     math.NewInt(DefaultDepositPerByte),
		DepositChunks:      []DepositChunk{},
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
	chunks := make(map[string]struct{}, len(gs.DepositChunks))
	for _, c := range gs.DepositChunks {
		if err := c.Validate(); err != nil {
			return err
		}
		key := fmt.Sprintf("%s/%d", c.Contract, c.Seq)
		if _, ok := chunks[key]; ok {
			return fmt.Errorf("duplicate deposit chunk %s", key)
		}
		chunks[key] = struct{}{}
	}
	return nil
}

// DepositChunk is one state-deposit charge: Bytes of a contract's storage growth that Payer
// locked at PerByte each, under the x/fees deposit DepositID. The chunk is the unit a later
// shrink of the same contract releases, newest first.
type DepositChunk struct {
	Contract  string   `json:"contract"`
	Seq       uint64   `json:"seq"`
	Payer     string   `json:"payer"`
	Bytes     uint64   `json:"bytes"`
	PerByte   math.Int `json:"per_byte"`
	DepositID string   `json:"deposit_id"`
}

// Amount is the deposit this chunk holds: Bytes * PerByte.
func (c DepositChunk) Amount() math.Int {
	return math.NewIntFromUint64(c.Bytes).Mul(c.PerByte)
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
