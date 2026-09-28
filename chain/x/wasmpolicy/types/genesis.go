package types

import "fmt"

// GenesisState is wasmpolicy's genesis. upload_sunset_height is written only here.
type GenesisState struct {
	UploadSunsetHeight uint64   `json:"upload_sunset_height"`
	GenesisCodeIDs     []uint64 `json:"genesis_code_ids"`
}

// DefaultGenesisState returns P6's sunset and an empty genesis code set.
func DefaultGenesisState() GenesisState {
	return GenesisState{
		UploadSunsetHeight: DefaultUploadSunsetHeight,
		GenesisCodeIDs:     []uint64{},
	}
}

// Validate rejects a zero code id and duplicate code ids. Height zero is legal:
// it means store is open at height zero.
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
	return nil
}
