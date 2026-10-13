package app_test

import (
	"testing"

	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	housetypes "github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// x/houses validates a proposed split at submission and x/emission re-validates it at every
// epoch close, where a refusal halts the chain. The two must accept exactly the same splits.
func TestSplitBounds_housesAndEmissionAgree(t *testing.T) {
	for v := uint32(0); v <= 100; v += 5 {
		for s := uint32(0); s <= 100-v; s += 5 {
			for r := uint32(0); r <= 100-v-s; r += 5 {
				d := 100 - v - s - r
				content := housetypes.ProposalContent{EmissionSplit: &housetypes.EmissionSplitChange{
					ValidatorPercent: v, StoragePercent: s, RelayPercent: r, DevelopmentPercent: d,
				}}
				housesOK := content.ValidateBasic() == nil
				emissionOK := emissiontypes.SplitPercents{Validator: v, Storage: s, Relay: r, Development: d}.Validate() == nil
				if housesOK != emissionOK {
					t.Fatalf("split %d/%d/%d/%d: houses=%v emission=%v", v, s, r, d, housesOK, emissionOK)
				}
			}
		}
	}
}
