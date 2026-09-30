package rollout

import (
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// Every refused flag combination is a usage error, decided before anything
// builds (stagenet e2e, 2026-09-30: two of them exited 1).
func TestValidate_refusalsAreUsage(t *testing.T) {
	for name, f := range map[string]Flags{
		"no env":                 {},
		"no-build without build": {Env: "devnet", NoBuild: true},
		"archive while building": {Env: "devnet", Archive: "/tmp/a.tar.gz"},
	} {
		err := f.validate()
		if clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("%s: err %v (code %d), want usage", name, err, clierr.CodeOf(err))
		}
	}
	ok := Flags{Env: "devnet", NoBuild: true, Archive: "/tmp/a.tar.gz"}
	if err := ok.validate(); err != nil {
		t.Errorf("a valid combination was refused: %v", err)
	}
}
