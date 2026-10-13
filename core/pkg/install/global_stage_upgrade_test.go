package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/planinfo"
)

func TestStageChainUpgrade_refusesBinariesThePlanDoesNotName(t *testing.T) {
	f := installedFixture(t)
	oramad, err := os.ReadFile(filepath.Join(f.staged, "oramad"))
	if err != nil {
		t.Fatal(err)
	}
	right := planinfo.Digests{Oramad: sumHex(oramad), Verifier: f.verifierSum}
	zero := strings.Repeat("0", 64)
	for name, tc := range map[string]struct {
		digests planinfo.Digests
		want    string
	}{
		"another oramad":   {planinfo.Digests{Oramad: zero, Verifier: right.Verifier}, "names an oramad with sha256 000000000000"},
		"another verifier": {planinfo.Digests{Oramad: right.Oramad, Verifier: zero}, "names an orama-orchard-verifier with sha256 000000000000"},
		"no checksums":     {planinfo.Digests{}, "names no usable checksum"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := StageChainUpgrade(f.host, f.refreshOptions(allInstalled...), "v2", tc.digests)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !errors.Is(err, planinfo.ErrMismatch) {
				t.Fatalf("err = %v, want %q wrapping ErrMismatch and nothing staged", err, tc.want)
			}
			if _, statErr := os.Stat(filepath.Join(f.host.ChainHome, "cosmovisor", "upgrades", "v2")); statErr == nil {
				t.Fatal("a binary the plan does not name was staged")
			}
		})
	}
}
