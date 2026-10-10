package releasepub

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

// Where releases go. Both are overridable by flag; they are the owner's
// release host and the public repository whose releases carry the archives.
const (
	// DefaultGitHubRepo holds the archives as release assets.
	DefaultGitHubRepo = "DeBrosDAO/orama"
	// DefaultMetadataDest is the rsync destination of the metadata: the
	// directory the release host's nginx serves (website/deploy/releases.orama.network.nginx.conf).
	DefaultMetadataDest = "releases.orama.network:/opt/orama-releases/"
	// releaseNotFound is what `gh release view` prints for a tag with no release.
	releaseNotFound = "release not found"
)

// Runner runs a command and returns what it printed. A test replaces it.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner runs the command for real, passing its output on to out.
func ExecRunner(out io.Writer) Runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		var buf bytes.Buffer
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stdout, cmd.Stderr = io.MultiWriter(&buf, out), io.MultiWriter(&buf, out)
		err := cmd.Run()
		return buf.Bytes(), err
	}
}

// PublishParams is one upload.
type PublishParams struct {
	Repo         Repo
	GitHubRepo   string
	MetadataDest string
	// DryRun checks the directory and prints the commands without running them.
	DryRun   bool
	Now      time.Time
	Run      Runner
	Progress io.Writer
}

// Publish uploads what the last cut or refresh left in the repository
// directory. The order keeps a client from ever seeing metadata that points at
// something absent: the archives go to GitHub first, then the root, targets and
// snapshot, and the timestamp, the file a client reads first, last. Before
// anything is sent the directory is held to what a client does with it, and
// each archive to the hash the signed targets name.
func Publish(ctx context.Context, p PublishParams) error {
	if p.Progress == nil {
		p.Progress = io.Discard
	}
	if err := p.checkLocal(); err != nil {
		return err
	}
	pending, err := p.Repo.ReadPending()
	if err != nil {
		return err
	}
	if pending != nil {
		if err := p.uploadAssets(ctx, pending); err != nil {
			return err
		}
	}
	files, err := p.metadataFiles()
	if err != nil {
		return err
	}
	if err := p.sync(ctx, files[:len(files)-1]); err != nil {
		return err
	}
	if err := p.sync(ctx, files[len(files)-1:]); err != nil {
		return err
	}
	if p.DryRun {
		return nil
	}
	return p.Repo.clearPending()
}

// checkLocal verifies the directory as a client would, and the pending
// archives against it.
func (p PublishParams) checkLocal() error {
	rootBytes, _, err := p.Repo.ReadRoot()
	if err != nil {
		return err
	}
	meta := releaseverify.Metadata{Root: rootBytes}
	for name, dst := range map[string]*[]byte{TimestampFile: &meta.Timestamp, SnapshotFile: &meta.Snapshot, TargetsFile: &meta.Targets} {
		if *dst, err = os.ReadFile(p.Repo.path(name)); err != nil {
			return fmt.Errorf("read %s: %w (cut a release first)", p.Repo.path(name), err)
		}
	}
	verified, err := releaseverify.Verify(meta, releaseverify.Seen{}, p.Now)
	if err != nil {
		return fmt.Errorf("the metadata in %s would not verify on a client; nothing was uploaded: %w", p.Repo.Dir, err)
	}
	pending, err := p.Repo.ReadPending()
	if err != nil || pending == nil {
		return err
	}
	for _, a := range pending.Assets {
		target, ok := verified.Targets[a.Target]
		if !ok {
			return fmt.Errorf("the signed targets do not list %s, which the pending release would upload", a.Target)
		}
		got, err := fileSHA256(a.Path)
		if err != nil {
			return fmt.Errorf("read the archive %s: %w", a.Path, err)
		}
		if want := fmt.Sprintf("%x", target.Hashes["sha256"]); got != want {
			return fmt.Errorf("%s has sha256 %s, not the %s the signed targets name; it changed after the cut", a.Path, got, want)
		}
	}
	return nil
}

// uploadAssets creates the GitHub release for the tag if there is none and
// uploads the archives to it. An asset that is already there is an error:
// a published archive is never overwritten.
func (p PublishParams) uploadAssets(ctx context.Context, pending *Pending) error {
	fmt.Fprintf(p.Progress, "Uploading %d archive(s) to %s release %s...\n", len(pending.Assets), p.GitHubRepo, pending.Tag)
	create := []string{"release", "create", pending.Tag, "--repo", p.GitHubRepo,
		"--title", pending.Channel + " " + pending.Version,
		"--notes", fmt.Sprintf("Orama %s %s. Install it through the TUF metadata at the release host; this page is only where the bytes live.", pending.Channel, pending.Version)}
	if pending.Channel != ChannelMain {
		create = append(create, "--prerelease")
	}
	upload := []string{"release", "upload", pending.Tag, "--repo", p.GitHubRepo}
	for _, a := range pending.Assets {
		upload = append(upload, a.Path)
	}
	if p.DryRun {
		p.print("gh", []string{"release", "view", pending.Tag, "--repo", p.GitHubRepo}, "(create it if it is not there:)")
		p.print("gh", create, "")
		p.print("gh", upload, "")
		return nil
	}
	out, err := p.Run(ctx, "gh", "release", "view", pending.Tag, "--repo", p.GitHubRepo)
	switch {
	case err == nil:
	case strings.Contains(string(out), releaseNotFound):
		if _, err := p.Run(ctx, "gh", create...); err != nil {
			return fmt.Errorf("create the GitHub release %s: %w", pending.Tag, err)
		}
	default:
		return fmt.Errorf("look up the GitHub release %s (is gh installed and logged in?): %w", pending.Tag, err)
	}
	if _, err := p.Run(ctx, "gh", upload...); err != nil {
		return fmt.Errorf("upload the archives to %s: %w", pending.Tag, err)
	}
	return nil
}

// metadataFiles are the files the host serves, the timestamp last: every
// version of the root, the targets and the snapshot.
func (p PublishParams) metadataFiles() ([]string, error) {
	roots, err := filepath.Glob(p.Repo.path("*.root.json"))
	if err != nil {
		return nil, fmt.Errorf("list the root versions: %w", err)
	}
	sort.Strings(roots)
	files := append(roots, p.Repo.path(RootFile), p.Repo.path(TargetsFile), p.Repo.path(SnapshotFile), p.Repo.path(TimestampFile))
	return files, nil
}

// sync copies files to the metadata destination with rsync over ssh.
func (p PublishParams) sync(ctx context.Context, files []string) error {
	args := append([]string{"-a", "--chmod=F644", "-e", "ssh -o BatchMode=yes"}, files...)
	args = append(args, p.MetadataDest)
	if p.DryRun {
		p.print("rsync", args, "")
		return nil
	}
	fmt.Fprintf(p.Progress, "Sending %s to %s...\n", summarize(baseNames(files)), p.MetadataDest)
	if _, err := p.Run(ctx, "rsync", args...); err != nil {
		return fmt.Errorf("send the metadata to %s: %w", p.MetadataDest, err)
	}
	return nil
}

func (p PublishParams) print(name string, args []string, note string) {
	if note != "" {
		fmt.Fprintln(p.Progress, note)
	}
	fmt.Fprintf(p.Progress, "  %s %s\n", name, strings.Join(args, " "))
}

func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}
