package push

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

const (
	// stagedArchive is the copy of the archive the release root checks,
	// inside the root-only staging directory.
	stagedArchive = "archive.tar.gz"
	// stagedArchivePerm: only root reads the copy.
	stagedArchivePerm = 0o600
)

// releaseArchive is the archive file stageArchive extracts. Without the
// release root it is the one given. With it, the archive is copied into
// staging — a 0700 directory under the root-owned base — and that copy is
// checked through the descriptor that wrote it: the bytes TUF checked are
// the bytes extracted, whatever happens to the original meanwhile. Asking
// for the release root with half the flags is an error, never the wallet
// path.
//
// It also returns the snapshot version the release was accepted at, 0 when
// the release root was not asked for.
func releaseArchive(t stageTarget, opts StageOptions, staging string) (string, int64, error) {
	if opts.ReleaseMetadata == "" && opts.ReleaseTarget == "" {
		return opts.Archive, 0, nil
	}
	if opts.ReleaseMetadata == "" || opts.ReleaseTarget == "" {
		return "", 0, clierr.Usage("--release-metadata and --release-target go together")
	}
	path := filepath.Join(staging, stagedArchive)
	f, err := copyArchive(opts.Archive, path)
	if err != nil {
		return "", 0, err
	}
	snapshot, checkErr := t.checkRelease(f, opts.ReleaseMetadata, opts.ReleaseTarget)
	if err := errors.Join(checkErr, f.Close()); err != nil {
		return "", 0, fmt.Errorf("release root: %w", err)
	}
	return path, snapshot, nil
}

// copyArchive copies src to a new file dst and returns it open.
func copyArchive(src, dst string) (*os.File, error) {
	in, err := os.Open(src)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_EXCL, stagedArchivePerm)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		return nil, errors.Join(fmt.Errorf("copy %s to %s: %w", src, dst, err), out.Close())
	}
	return out, nil
}

// checkReleaseFile is the node's TUF check of an archive file. It returns the
// snapshot version the archive was accepted at. A target in a channel
// ("stable/orama-...") is the delegated role of that name's, which is read
// from the metadata directory with the top-level roles.
func checkReleaseFile(archive *os.File, metadataDir, target string) (int64, error) {
	verified, err := releaseverify.CheckFile(releaseverify.FileCheck{
		RootPath:    releaseverify.RootPath,
		SeenPath:    releaseverify.SeenPath,
		MetadataDir: metadataDir,
		Roles:       rolesOf(target),
		Target:      target,
		File:        archive,
		Now:         time.Now(),
	})
	if err != nil {
		return 0, err
	}
	return verified.SnapshotVersion, nil
}

// rolesOf is the delegated role a target is in: the first path segment of a
// target with a directory, none for a top-level one.
func rolesOf(target string) []string {
	if role, _, ok := strings.Cut(target, "/"); ok {
		return []string{role}
	}
	return nil
}

// endorseStaged records that the archive whose manifest is manifestSHA256 was
// staged because it verified against the adopted release root.
func endorseStaged(e releaseverify.Endorsement) error {
	root, err := releaseverify.ReadRoot(releaseverify.RootPath)
	if err != nil {
		return err
	}
	e.RootSHA256 = releaseverify.RootDigest(root)
	e.StagedAt = time.Now().UTC()
	return releaseverify.RecordStaged(releaseverify.StagedPath, e)
}
