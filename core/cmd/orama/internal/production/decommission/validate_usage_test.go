package decommission

import (
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// A missing required flag is a usage mistake, not a failure (review 2026-09-30:
// --node exited with the generic code).
func TestValidate_missingFlagsAreUsage(t *testing.T) {
	for name, f := range map[string]Flags{"no env": {}, "no node": {Env: "devnet"}} {
		if err := f.validate(); clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("%s: %v (code %d), want usage", name, err, clierr.CodeOf(err))
		}
	}
	if err := (&Flags{Env: "devnet", Node: "203.0.113.1"}).validate(); err != nil {
		t.Errorf("complete flags refused: %v", err)
	}
}
