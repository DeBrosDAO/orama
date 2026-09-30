//go:build e2e_fleet

package releasetuf

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tuf"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Paths on the extra server. Everything the test uploads lives under
// workDir; the node's own paths are the product's.
const (
	workDir     = "/root/e2e-tuf"
	archivePath = workDir + "/orama.tar.gz"
	cliPath     = "/usr/local/bin/orama"
	optOrama    = "/opt/orama"
	// archiveCLI is the CLI inside a build archive (the entry `orama node
	// setup` extracts, core/cmd/orama/internal/production/setup).
	archiveCLI = "bin/orama"
	// transferBudget bounds uploading the archive.
	transferBudget = 10 * time.Minute
	// Snapshot versions: the accepted release, an older one (a replay) and
	// a newer one every other refusal is published at.
	acceptedVersion = 5
	olderVersion    = 3
	newerVersion    = 6
	// exitOK is the CLI's success.
	exitOK = 0
)

// node is the extra server a test stages on.
type node struct {
	f *fleet.Fleet
	n fleet.Node
}

// newNode creates an extra server, uploads the run's signed archive and
// installs the CLI it carries as the node's orama: the same linux build the
// run installs everywhere. Nothing else is installed, and no release root is
// adopted yet.
func newNode(t *testing.T, name string) node {
	t.Helper()
	f := harness.Fleet(t)
	loc := f.State.Nodes[0].Location
	extra := harness.ExtraNode(t, name, loc)
	nd := node{f: f, n: extra.Node}
	nd.must(t, "install -d -m 0700 "+workDir+" && install -d -m 0755 -o root -g root "+optOrama+" /etc/orama")
	nd.put(t, archivePath, archiveBytes(t, f), 0o600)
	nd.must(t, fmt.Sprintf("tar --no-same-owner -xzf %s -C %s %s && install -m 0755 %s/%s %s",
		archivePath, workDir, archiveCLI, workDir, archiveCLI, cliPath))
	return nd
}

// archiveBytes is the run's signed HEAD archive.
func archiveBytes(t *testing.T, f *fleet.Fleet) []byte {
	t.Helper()
	raw, err := os.ReadFile(f.State.ArchivePath)
	if err != nil {
		t.Fatalf("read the run's archive: %v", err)
	}
	return raw
}

func (nd node) must(t *testing.T, cmd string) fleet.Output {
	t.Helper()
	return nd.f.MustExec(t, nd.n, cmd)
}

// put writes a file on the node. The server is deleted when the test ends,
// so nothing is restored.
func (nd node) put(t *testing.T, p string, data []byte, mode os.FileMode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), transferBudget)
	defer cancel()
	if err := nd.f.SSHFor(t, nd.n).Put(ctx, p, data, mode); err != nil {
		t.Fatalf("upload %s to %s: %v", p, nd.n.Name, err)
	}
}

// adopt installs repo's root as the node's release root.
func (nd node) adopt(t *testing.T, repo *tuf.Repo) {
	t.Helper()
	nd.put(t, tuf.RootPath, repo.Root(), 0o644)
}

// upload writes one metadata set to a directory of its own and returns it.
func (nd node) upload(t *testing.T, name string, meta map[string][]byte) string {
	t.Helper()
	dir := workDir + "/meta-" + name
	nd.must(t, "install -d -m 0700 "+dir)
	for _, file := range tuf.Files {
		nd.put(t, path.Join(dir, file), meta[file], 0o600)
	}
	return dir
}

// orama runs the node's CLI as root and returns its output, both streams.
func (nd node) orama(t *testing.T, args string) (int, string) {
	t.Helper()
	out := nd.f.Exec(t, nd.n, cliPath+" "+args)
	return out.Exit, out.Stdout + out.Stderr
}

// seen is the node's rollback record, "" when there is none.
func (nd node) seen(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(nd.must(t, "cat "+tuf.SeenPath+" 2>/dev/null || true").Stdout)
}

// snapshot fingerprints /opt/orama: every path, mode, owner, size and mtime,
// and whether a staging directory was left behind.
func (nd node) snapshot(t *testing.T) string {
	t.Helper()
	return nd.must(t, "find "+optOrama+" -xdev -printf '%P %m %U %G %s %T@ %y\\n' | LC_ALL=C sort | sha256sum").Stdout
}

// wantRefused asserts exit != 0 and every fragment in the output.
func wantRefused(t *testing.T, exit int, out string, fragments ...string) {
	t.Helper()
	if exit == exitOK {
		t.Fatalf("accepted, want a refusal naming %q:\n%s", fragments, out)
	}
	for _, frag := range fragments {
		if !strings.Contains(out, frag) {
			t.Errorf("exit %d without %q:\n%s", exit, frag, out)
		}
	}
}

// refusal is one metadata set that must be refused, and the words saying why.
type refusal struct {
	name  string
	meta  map[string][]byte
	wants []string
}

// refusals are the TUF failures, each a real signed repository but one:
// target is the name the node is asked for, content its bytes.
func refusals(t *testing.T, repo *tuf.Repo, target string, content []byte) []refusal {
	t.Helper()
	valid := func(v int64, c []byte) map[string][]byte {
		return repo.Metadata(t, v, time.Time{}, map[string][]byte{target: c})
	}
	return []refusal{
		{"rollback", valid(olderVersion, content), []string{tuf.ErrRollback,
			fmt.Sprintf("snapshot version %d is lower than %d already seen", olderVersion, acceptedVersion)}},
		{"expired-timestamp", repo.Metadata(t, newerVersion, time.Now().Add(-time.Hour), map[string][]byte{target: content}),
			[]string{tuf.ErrFreeze}},
		{"below-threshold", tuf.Unsigned(t, valid(newerVersion, content), tuf.Files[0]), []string{tuf.ErrThreshold, "timestamp"}},
		{"hash-mismatch", valid(newerVersion, tuf.Flipped(content)), []string{tuf.ErrTargetHash, "sha256 does not match"}},
		{"length-mismatch", valid(newerVersion, tuf.Longer(content)), []string{tuf.ErrTargetHash,
			fmt.Sprintf("is not %d bytes long", len(content)+1)}},
		{"wrong-target", repo.Metadata(t, newerVersion, time.Time{}, map[string][]byte{"other-" + target: content}),
			[]string{fmt.Sprintf("targets metadata does not name %q", target)}},
	}
}
