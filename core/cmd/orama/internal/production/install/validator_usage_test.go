package install

import (
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

func TestValidateFlags_missingVPSIPIsUsage(t *testing.T) {
	err := NewValidator(&Flags{}, t.TempDir()).ValidateFlags()
	if clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("%v (code %d), want usage", err, clierr.CodeOf(err))
	}
}
