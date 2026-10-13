package releaseverify

import (
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

const (
	stableTarget  = "stable/orama-0.3.1-linux-amd64.tar.gz"
	nightlyTarget = "nightly/orama-0.4.0-linux-amd64.tar.gz"
)

// channelRepo is a repository whose targets metadata lists archives under the
// stable and nightly channels' prefixes.
type channelRepo struct {
	keys releaserepo.Keys
	root []byte
}

func newChannelRepo(t *testing.T) *channelRepo {
	t.Helper()
	keys, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	root, err := releaserepo.NewRoot(keys, testNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return &channelRepo{keys: keys, root: root}
}

func (r *channelRepo) files(t *testing.T, version int64, targets map[string][]byte) map[string][]byte {
	t.Helper()
	files, err := releaserepo.Build(r.keys, releaserepo.Spec{
		Version: version, RootValidUntil: testNow.Add(24 * time.Hour), Targets: targets,
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func defaultTargets() map[string][]byte {
	return map[string][]byte{stableTarget: []byte("stable archive"), nightlyTarget: []byte("nightly archive")}
}

// metaFor is the metadata a client holds after fetching files.
func (r *channelRepo) metaFor(files map[string][]byte) Metadata {
	return Metadata{Root: r.root, Timestamp: files[TimestampFile], Snapshot: files[SnapshotFile], Targets: files[TargetsFile]}
}
