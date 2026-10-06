//go:build e2e_fleet

package releasetuf

import (
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tuf"
)

// stage-oramad on the extra: the chain account and home it stages into
// (core/pkg/constants/chain.go ChainUser; the home is the test's own, root's
// like the real one), and the binary offered.
const (
	chainUser     = "orama-chain"
	chainHome     = "/var/lib/orama-e2e-tuf/chain"
	oramadPath    = workDir + "/oramad"
	oramadTarget  = "oramad-linux-amd64"
	genesisBinary = chainHome + "/cosmovisor/genesis/bin/oramad"
)

// oramadContent is the binary offered: stage-oramad places bytes, it does not
// run them, so any file the metadata names will do.
var oramadContent = []byte("#!/bin/sh\necho e2e-oramad\n")

func oramadArgs(metaDir, placement string) string {
	return fmt.Sprintf("global stage-oramad --binary %s --release-metadata %s --release-target %s --home %s %s",
		oramadPath, metaDir, oramadTarget, chainHome, placement)
}

// TestStageOramad_releaseRootRefusalsStageNothing stages a genesis oramad
// under a generated release root, then offers an upgrade binary with every
// kind of bad metadata: each is refused as not verified and no binary
// appears for that upgrade.
func TestStageOramad_releaseRootRefusalsStageNothing(t *testing.T) {
	nd := newNode(t, "tuf-oramad")
	nd.must(t, "id -u "+chainUser+" >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin "+chainUser)
	nd.must(t, "install -d -m 0755 -o root -g root "+chainHome)
	nd.put(t, oramadPath, oramadContent, 0o755)
	repo := tuf.New(t)
	valid := nd.upload(t, "oramad-valid", repo.Metadata(t, acceptedVersion, time.Time{}, map[string][]byte{oramadTarget: oramadContent}))

	exit, out := nd.orama(t, oramadArgs(valid, "--genesis"))
	wantRefused(t, exit, out, "did not verify, nothing was staged", tuf.ErrNoRoot)
	nd.must(t, "test ! -e "+genesisBinary)

	nd.adopt(t, repo)
	if exit, out := nd.orama(t, oramadArgs(valid, "--genesis")); exit != exitOK {
		t.Fatalf("a verified oramad was refused (exit %d):\n%s", exit, out)
	}
	nd.must(t, "cmp "+oramadPath+" "+genesisBinary)
	for _, c := range refusals(t, repo, oramadTarget, oramadContent) {
		t.Run(c.name, func(t *testing.T) {
			upgrade := "e2e-" + c.name
			exit, out := nd.orama(t, oramadArgs(nd.upload(t, "oramad-"+c.name, c.meta), "--upgrade "+upgrade))
			wantRefused(t, exit, out, append([]string{oramadPath + " did not verify, nothing was staged: "}, c.wants...)...)
			nd.must(t, "test ! -e "+chainHome+"/cosmovisor/upgrades/"+upgrade+"/bin/oramad")
			if got := nd.seen(t); got != fmt.Sprintf(`{"snapshot_version":%d}`, acceptedVersion) {
				t.Errorf("the rollback record moved to %q on a refusal", got)
			}
		})
	}
}
