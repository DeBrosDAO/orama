package push

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/spf13/cobra"
)

// NodeOramaBinary is the copy of the CLI install and upgrade verified and put
// on the node's PATH. Push runs the node-side step with it, never with
// anything from the archive being pushed.
const NodeOramaBinary = "/usr/local/bin/orama"

const (
	// stagingPrefix names the directory an archive is extracted into and
	// verified in, inside /opt/orama so the final renames stay on one
	// filesystem. Any left by an interrupted run is removed by the next.
	stagingPrefix = ".archive-staging-"
	// stagedNew and stagedOld hold, inside a staging directory, the verified
	// archive and the paths it replaces (kept until the swap has succeeded).
	stagedNew = "new"
	stagedOld = "old"
	// binPerm is /opt/orama/bin and every binary in it: root writes, the
	// orama group runs them, nobody else reads them (as lockOramaBinDir).
	binPerm = 0o750
	// oramaGroup owns /opt/orama/bin.
	oramaGroup = "orama"
	// writableByOthers are the mode bits that would let someone other than
	// the owner change entries of a directory.
	writableByOthers = 0o022
)

// StageOptions are the node-side stage-archive flags.
type StageOptions struct {
	Archive string
	// TrustSigners creates a missing trust anchor — a node installed before
	// archives were signed — once the archive has verified against it. It
	// never changes an existing anchor.
	TrustSigners []string
}

// stageTarget is where an archive is staged and how the node's trust anchor is
// reached. A test stages into a temporary directory with its own anchor.
type stageTarget struct {
	base string
	// readSigners is the anchor, or an error wrapping archivetrust.ErrNoAnchor.
	readSigners func() ([]string, error)
	// verify checks an extracted archive against the anchor, including its
	// architecture and a rotation it carries.
	verify func(dir string) (*archivetrust.Verified, error)
	// createSigners writes a missing anchor.
	createSigners func([]string) error
	// chownBin gives /opt/orama/bin and its files to root and the orama group.
	chownBin func(path string) error
	// arch is the architecture this node runs; verify checks it itself.
	arch string
}

// nodeTarget is /opt/orama and the node's real anchor.
func nodeTarget() stageTarget {
	return stageTarget{
		base:        install.OramaBase,
		readSigners: func() ([]string, error) { return archivetrust.ReadAnchor(archivetrust.AnchorPath) },
		verify: func(dir string) (*archivetrust.Verified, error) {
			return archivetrust.Verify(archivetrust.AnchorPath, dir, runtime.GOARCH)
		},
		createSigners: func(s []string) error { return archivetrust.CreateAnchorIfMissing(archivetrust.AnchorPath, s) },
		chownBin:      chownToOramaGroup,
		arch:          runtime.GOARCH,
	}
}

// NewStageArchiveCmd is `orama node stage-archive`, the step `orama push` runs
// on each node with the node's installed CLI.
func NewStageArchiveCmd() *cobra.Command {
	var opts StageOptions
	cmd := &cobra.Command{
		Use:   "stage-archive",
		Short: "Verify a pushed build archive and put it in place (run by 'orama push')",
		Long: `Verify a build archive against this node's trust anchor, /etc/orama/archive-signers,
and only then replace the archive files under /opt/orama with it.

'orama push' runs this on every node with the node's installed orama. The
archive is extracted into a private directory, its manifest signature must
recover to a trusted signer and every file must match the signed manifest;
anything else leaves /opt/orama untouched. The replacement is undone if any
step of it fails, and holds the lock install and upgrade take on /opt/orama.

--trust-signers creates the anchor on a node installed before archives were
signed, and only after the archive has verified against those addresses. It
never changes an existing anchor.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return Stage(opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Archive, "archive", "", "The pushed archive on this node [required]")
	f.StringSliceVar(&opts.TrustSigners, "trust-signers", nil,
		"Create a missing trust anchor with these addresses (nodes installed before archive signing only)")
	return cmd
}

// Stage verifies the archive and puts it in place under /opt/orama.
func Stage(opts StageOptions) error {
	if opts.Archive == "" {
		return clierr.Usage("--archive is required")
	}
	if err := clierr.RequireRoot("staging a build archive"); err != nil {
		return err
	}
	if err := checkBaseOwnedByRoot(install.OramaBase); err != nil {
		return err
	}
	return stageArchive(nodeTarget(), opts)
}

// checkBaseOwnedByRoot refuses a /opt/orama anyone but root could change:
// everything staged below it would be as untrusted as its owner.
func checkBaseOwnedByRoot(base string) error {
	info, err := os.Lstat(base)
	if err != nil {
		return fmt.Errorf("stat %s: %w", base, err)
	}
	owner, err := ownerUID(info)
	if err != nil {
		return err
	}
	if !info.IsDir() || owner != 0 || info.Mode().Perm()&writableByOthers != 0 {
		return fmt.Errorf("%s must be a directory owned by root and writable only by root (it is %s, uid %d); "+
			"refusing to stage a build there", base, info.Mode(), owner)
	}
	return nil
}

func stageArchive(t stageTarget, opts StageOptions) (err error) {
	unlock, err := archivetrust.LockArchiveDir(t.base)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()

	if err := removeLeftoverStaging(t.base); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(t.base, stagingPrefix)
	if err != nil {
		return fmt.Errorf("create a staging directory in %s: %w", t.base, err)
	}
	defer func() {
		if rmErr := os.RemoveAll(staging); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove the staging directory %s: %w", staging, rmErr))
		}
	}()

	newDir := filepath.Join(staging, stagedNew)
	if err := os.Mkdir(newDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", newDir, err)
	}
	if err := archivetrust.Extract(opts.Archive, newDir); err != nil {
		return fmt.Errorf("extract %s: %w", opts.Archive, err)
	}
	verified, err := verifyStaged(t, opts, newDir)
	if err != nil {
		return fmt.Errorf("refusing %s, nothing under %s was changed: %w", opts.Archive, t.base, err)
	}
	if err := lockBinDir(t, filepath.Join(newDir, "bin")); err != nil {
		return err
	}
	if err := swapArchive(t.base, newDir, filepath.Join(staging, stagedOld)); err != nil {
		return err
	}
	fmt.Printf("  ✓ v%s (%s) verified, signed by %s\n", verified.Manifest.Version, verified.Manifest.Commit, verified.Signer)
	return nil
}

// verifyStaged verifies the extracted archive. On a node without an anchor,
// --trust-signers is what it verifies against, and the anchor is written only
// once the archive has passed: a mistyped address then fails this push and
// leaves nothing behind to undo.
func verifyStaged(t stageTarget, opts StageOptions, dir string) (*archivetrust.Verified, error) {
	var trust []string
	if len(opts.TrustSigners) > 0 {
		normalized, err := archivetrust.NormalizeSigners(opts.TrustSigners)
		if err != nil {
			return nil, fmt.Errorf("--trust-signers: %w", err)
		}
		trust = normalized
	}
	existing, err := t.readSigners()
	missing := errors.Is(err, archivetrust.ErrNoAnchor)
	if err != nil && !(missing && trust != nil) {
		return nil, err
	}
	var verified *archivetrust.Verified
	if missing {
		if verified, err = archivetrust.VerifyTree(dir, trust); err != nil {
			return nil, err
		}
		if verified.Manifest.Arch != t.arch {
			return nil, fmt.Errorf("the archive is built for linux/%s and this node is linux/%s", verified.Manifest.Arch, t.arch)
		}
		if err := t.createSigners(trust); err != nil {
			return nil, err
		}
	} else {
		if trust != nil && !archivetrust.SameSigners(existing, trust) {
			return nil, fmt.Errorf("--trust-signers only creates a missing anchor, and this node already trusts %v; "+
				"change who signs builds with `orama build --signers`", existing)
		}
		if verified, err = t.verify(dir); err != nil {
			return nil, err
		}
	}
	return verified, nil
}

// LeftoverPrefixes name the directories under /opt/orama that a staging run,
// or setup's CLI check, leaves behind when it is killed.
var LeftoverPrefixes = []string{stagingPrefix, SetupCLIPrefix}

// SetupCLIPrefix names the root-only directory `orama node setup` extracts a
// fresh node's CLI into before it runs it (setup.stageArchiveCommand).
const SetupCLIPrefix = ".archive-cli-"

// removeLeftoverStaging removes what an interrupted run left under base. It
// runs under the archive lock, so no staging directory is in use; the setup
// CLI directory this process runs from, if any, is kept.
func removeLeftoverStaging(base string) error {
	self := runningFrom()
	for _, prefix := range LeftoverPrefixes {
		leftovers, err := filepath.Glob(filepath.Join(base, prefix+"*"))
		if err != nil {
			return fmt.Errorf("list leftover directories: %w", err)
		}
		for _, dir := range leftovers {
			if dir == self {
				continue
			}
			if err := os.RemoveAll(dir); err != nil {
				return fmt.Errorf("remove the leftover directory %s: %w", dir, err)
			}
		}
	}
	return nil
}

// runningFrom is the directory this binary was extracted into when it runs as
// <dir>/bin/orama; "" when that cannot be told.
func runningFrom() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(filepath.Dir(exe))
}

// lockBinDir makes bin root:orama 0750, and its files the same, before it goes
// live: the services run as orama, and nothing but root may change what they
// run. Set explicitly, so the node's umask does not decide it.
func lockBinDir(t stageTarget, bin string) error {
	entries, err := os.ReadDir(bin)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list %s: %w", bin, err)
	}
	for _, p := range append([]string{bin}, entryPaths(bin, entries)...) {
		if err := os.Chmod(p, binPerm); err != nil {
			return fmt.Errorf("chmod %s: %w", p, err)
		}
		if err := t.chownBin(p); err != nil {
			return err
		}
	}
	return nil
}

func entryPaths(dir string, entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out
}

// chownToOramaGroup gives path to root and the orama group. On a machine
// being set up for the first time the group does not exist yet: bin stays
// root:root 0750, which only root can use, until the install creates the
// orama user and lockOramaBinDir hands bin to its group.
func chownToOramaGroup(path string) error {
	gid := 0
	g, err := user.LookupGroup(oramaGroup)
	var unknown user.UnknownGroupError
	switch {
	case errors.As(err, &unknown):
	case err != nil:
		return fmt.Errorf("look up the %s group, which runs the binaries in /opt/orama/bin: %w", oramaGroup, err)
	default:
		if gid, err = strconv.Atoi(g.Gid); err != nil {
			return fmt.Errorf("the %s group has a non-numeric gid %q: %w", oramaGroup, g.Gid, err)
		}
	}
	if err := os.Lchown(path, 0, gid); err != nil {
		return fmt.Errorf("chown %s to root:%d: %w", path, gid, err)
	}
	return nil
}
