package push

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
)

const (
	// uploadMetadataDir and uploadRootName are where a release's metadata and
	// root sit inside the node's upload directory.
	uploadMetadataDir = "meta"
	uploadRootName    = "root.json"
	// maxMetadataFiles bounds the metadata a release carries; a real one is a
	// handful of files.
	maxMetadataFiles = 256
)

// releaseTargetPattern is a target name in the signed targets metadata
// ("nightly/orama-0.3.0-linux-amd64.tar.gz"); it goes into a root shell script.
var releaseTargetPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,255}$`)

// metadataNamePattern is one path element of a metadata file.
var metadataNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ReleaseFiles is a release already fetched and verified on this machine
// (releasefetch.Fetch).
type ReleaseFiles struct {
	// Archive is the release archive.
	Archive string
	// MetadataDir holds the TUF metadata the archive verified against.
	MetadataDir string
	// Target is the archive's name in the signed targets.
	Target string
	// Root is the TUF root the release verified under.
	Root []byte
}

// ReleaseToNode stages a verified release on one node: it uploads the archive,
// the metadata and the root to a private directory, adopts the root when the
// node has none or rotates to it when it is the next version of the node's, and has the node's installed orama verify the archive against
// the adopted root (`node stage-archive --release-only`) before it replaces
// anything under /opt/orama. A node that trusts a different root refuses the
// release and says so. Nothing is restarted. It returns what the node's stage
// printed, and writes nothing to the terminal, so several nodes can be staged
// at once.
func ReleaseToNode(node inspector.Node, rel ReleaseFiles) (out string, err error) {
	if !releaseTargetPattern.MatchString(rel.Target) {
		return "", fmt.Errorf("release target %q is not a name in the signed targets", rel.Target)
	}
	files, err := metadataFiles(rel.MetadataDir)
	if err != nil {
		return "", err
	}
	run := func(cmd string) (string, error) { return remotessh.RunSSHOutput(node, cmd) }
	dir, err := makeUploadDir(run)
	if err != nil {
		return "", fmt.Errorf("upload to %s failed: %w", node.Host, err)
	}
	defer func() {
		if _, rmErr := run(remotessh.SudoPrefix(node) + "rm -rf " + dir); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove the upload %s on %s: %w", dir, node.Host, rmErr))
		}
	}()
	if err := uploadRelease(node, dir, rel, files); err != nil {
		return "", fmt.Errorf("upload to %s failed: %w", node.Host, err)
	}
	out, err = run(releaseStageCommand(remotessh.SudoPrefix(node), dir, rel.Target))
	if err != nil {
		return "", fmt.Errorf("stage the release on %s failed: %w\n  %s", node.Host, err, releaseStageHint)
	}
	return out, nil
}

// releaseStageHint says what a refused release stage most often means.
const releaseStageHint = "the node's installed orama verifies the release against the release root it adopted " +
	"(" + releaseverify.RootPath + "); a node that trusts another root, one more than a version behind the pushed root " +
	"(it takes one rotation per push), that was never installed or does not know 'node trust add-root --rotate', or whose release has no --release-only stage " +
	"(it needs one signed push first: orama maint push --trust-signers) refuses it"

// metadataFiles lists the files under dir as slash-separated relative paths,
// each element of which is safe in a remote shell command.
func metadataFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, el := range strings.Split(rel, "/") {
			if !metadataNamePattern.MatchString(el) {
				return fmt.Errorf("metadata file %q has a name that cannot go in a remote command", rel)
			}
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("metadata file %q is not a regular file", rel)
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the release metadata in %s: %w", dir, err)
	}
	if len(files) == 0 || len(files) > maxMetadataFiles {
		return nil, fmt.Errorf("%s holds %d metadata files; a release has between 1 and %d", dir, len(files), maxMetadataFiles)
	}
	return files, nil
}

// uploadRelease copies the archive, the metadata and the root into dir.
func uploadRelease(node inspector.Node, dir string, rel ReleaseFiles, files []string) error {
	if _, err := remotessh.RunSSHOutput(node, "mkdir -p "+strings.Join(metadataDirs(dir, files), " ")); err != nil {
		return err
	}
	if err := remotessh.UploadFile(node, rel.Archive, uploadPath(dir)); err != nil {
		return err
	}
	for _, f := range files {
		if err := remotessh.UploadFile(node, filepath.Join(rel.MetadataDir, filepath.FromSlash(f)), path.Join(dir, uploadMetadataDir, f)); err != nil {
			return err
		}
	}
	return uploadRoot(node, dir, rel.Root)
}

// uploadRoot writes the root to a local temporary file and copies it to the node.
func uploadRoot(node inspector.Node, dir string, root []byte) error {
	if len(root) == 0 {
		return errors.New("the release carries no root to adopt")
	}
	return remotessh.UploadBytes(node, root, path.Join(dir, uploadRootName))
}

// metadataDirs are the remote directories the metadata files need.
func metadataDirs(dir string, files []string) []string {
	seen := map[string]bool{path.Join(dir, uploadMetadataDir): true}
	for _, f := range files {
		if d := path.Dir(f); d != "." {
			seen[path.Join(dir, uploadMetadataDir, d)] = true
		}
	}
	dirs := make([]string, 0, len(seen))
	for d := range seen {
		dirs = append(dirs, d)
	}
	return dirs
}

// releaseStageCommand is the node-side step for an uploaded release. The node's
// installed orama adopts the root when the node has none, follows it when it is the next version of
// the root the node holds (a rotation checked against that root, never taken on the pusher's word),
// then stages the archive on the release root's checks alone. The script travels base64-encoded
// into `bash -s`; every value in it is checked here or is a fixed path.
func releaseStageCommand(sudo, dir, target string) string {
	script := strings.Join([]string{
		"set -eu",
		"[ -f " + releaseverify.RootPath + " ] || " + NodeOramaBinary + " node trust add-root " + path.Join(dir, uploadRootName),
		"cmp -s " + path.Join(dir, uploadRootName) + " " + releaseverify.RootPath + " || " + NodeOramaBinary + " node trust add-root --rotate " + path.Join(dir, uploadRootName),
		NodeOramaBinary + " node stage-archive --archive " + uploadPath(dir) +
			" --release-metadata " + path.Join(dir, uploadMetadataDir) +
			" --release-target " + target + " --release-only",
	}, "\n") + "\n"
	return remotessh.ScriptCommand(sudo, script)
}
