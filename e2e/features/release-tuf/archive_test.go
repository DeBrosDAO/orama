//go:build e2e_fleet

package releasetuf

import (
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tuf"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// archiveTarget is the name the archive has in the release targets.
const archiveTarget = "orama-linux-amd64.tar.gz"

// refusedPrefix is how stage-archive refuses before extracting
// (core/cmd/orama/internal/production/push/stage.go stageArchive, release.go).
var refusedPrefix = fmt.Sprintf("refusing %s, nothing under %s was changed: release root: ", archivePath, optOrama)

func stageArgs(metaDir string) string {
	return fmt.Sprintf("node stage-archive --archive %s --release-metadata %s --release-target %s", archivePath, metaDir, archiveTarget)
}

// TestStageArchive_releaseRootRefusalsChangeNothing stages the run's archive
// under a generated release root, then offers it with every kind of bad
// metadata: each is refused with its reason, /opt/orama is byte-for-byte
// what the accepted stage left, and the rollback record stays at the
// accepted snapshot.
func TestStageArchive_releaseRootRefusalsChangeNothing(t *testing.T) {
	f := harness.Fleet(t)
	nd := newNode(t, "tuf-archive")
	repo := tuf.New(t)
	content := archiveBytes(t, f)
	valid := nd.upload(t, "valid", repo.Metadata(t, acceptedVersion, time.Time{}, map[string][]byte{archiveTarget: content}))

	exit, out := nd.orama(t, stageArgs(valid))
	wantRefused(t, exit, out, refusedPrefix, tuf.ErrNoRoot, tuf.RootPath)

	nd.adopt(t, repo)
	exit, out = nd.orama(t, stageArgs(valid)+" --trust-signers "+f.State.OperatorAddress)
	if exit != exitOK {
		t.Fatalf("a valid archive under the adopted root was refused (exit %d):\n%s", exit, out)
	}
	if got, want := nd.seen(t), fmt.Sprintf(`{"snapshot_version":%d}`, acceptedVersion); got != want {
		t.Fatalf("rollback record %q, want %q", got, want)
	}
	before := nd.snapshot(t)
	for _, c := range refusals(t, repo, archiveTarget, content) {
		t.Run(c.name, func(t *testing.T) {
			exit, out := nd.orama(t, stageArgs(nd.upload(t, c.name, c.meta)))
			wantRefused(t, exit, out, append([]string{refusedPrefix}, c.wants...)...)
			if after := nd.snapshot(t); after != before {
				t.Errorf("/opt/orama changed after a refusal: %s -> %s", before, after)
			}
			if got := nd.seen(t); got != fmt.Sprintf(`{"snapshot_version":%d}`, acceptedVersion) {
				t.Errorf("the rollback record moved to %q on a refusal", got)
			}
		})
	}
}
