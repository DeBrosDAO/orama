//go:build e2e_fleet

// Package tuf builds real, signed TUF release metadata for the release-root
// features, with core's own generator (core/pkg/releaseverify/releasetest):
// the same go-tuf library and the same file names the node's
// `orama node stage-archive --release-metadata` and `orama global
// stage-oramad` read. Every key is generated per test; nothing here is a
// production root.
package tuf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releasetest"
)

// Files a metadata directory holds, as the node reads them.
var Files = []string{releaseverify.TimestampFile, releaseverify.SnapshotFile, releaseverify.TargetsFile}

// Paths on a node (core/pkg/releaseverify/file.go).
const (
	// RootPath is the release root a node adopts.
	RootPath = releaseverify.RootPath
	// SeenPath is the rollback record.
	SeenPath = releaseverify.SeenPath
)

// Refusal fragments (core/pkg/releaseverify/verify.go sentinel errors).
var (
	ErrRollback   = releaseverify.ErrRollback.Error()
	ErrFreeze     = releaseverify.ErrFreeze.Error()
	ErrThreshold  = releaseverify.ErrThreshold.Error()
	ErrTargetHash = releaseverify.ErrTargetHash.Error()
	ErrNoRoot     = releaseverify.ErrNoRoot.Error()
)

// Repo is one generated release root.
type Repo struct {
	r *releasetest.Repo
}

// New generates a root valid for a week from now.
func New(t testing.TB) *Repo {
	t.Helper()
	return &Repo{r: releasetest.NewRepo(t, time.Now())}
}

// Root is the signed root.json a node adopts at RootPath.
func (r *Repo) Root() []byte { return r.r.Root() }

// Metadata is one set of timestamp, snapshot and targets metadata at version
// naming targets (name -> the exact bytes), by file name. A zero
// timestampExpires is the root's own expiry; one in the past is a frozen
// repository.
func (r *Repo) Metadata(t testing.TB, version int64, timestampExpires time.Time, targets map[string][]byte) map[string][]byte {
	t.Helper()
	dir := t.TempDir()
	r.r.Publish(t, dir, version, timestampExpires, targets)
	out := map[string][]byte{}
	for _, name := range Files {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read the generated %s: %v", name, err)
		}
		out[name] = raw
	}
	return out
}

// Unsigned returns meta with every signature of file removed: that role is
// then below its threshold (the root requires one signature per role).
func Unsigned(t testing.TB, meta map[string][]byte, file string) map[string][]byte {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(meta[file], &doc); err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	doc["signatures"] = json.RawMessage("[]")
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode %s: %v", file, err)
	}
	out := map[string][]byte{}
	for k, v := range meta {
		out[k] = v
	}
	out[file] = raw
	return out
}

// Flipped is a copy of b with its last byte changed: the same length, other
// bytes, so only the hash check can refuse it.
func Flipped(b []byte) []byte {
	out := append([]byte(nil), b...)
	if len(out) > 0 {
		out[len(out)-1] ^= 0xff
	}
	return out
}

// Longer is a copy of b one byte longer: the length check refuses it.
func Longer(b []byte) []byte {
	return append(append([]byte(nil), b...), 0)
}
