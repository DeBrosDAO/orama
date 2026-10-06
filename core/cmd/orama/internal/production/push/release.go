package push

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
func releaseArchive(t stageTarget, opts StageOptions, staging string) (string, error) {
	if opts.ReleaseMetadata == "" && opts.ReleaseTarget == "" {
		return opts.Archive, nil
	}
	if opts.ReleaseMetadata == "" || opts.ReleaseTarget == "" {
		return "", clierr.Usage("--release-metadata and --release-target go together")
	}
	path := filepath.Join(staging, stagedArchive)
	f, err := copyArchive(opts.Archive, path)
	if err != nil {
		return "", err
	}
	checkErr := t.checkRelease(f, opts.ReleaseMetadata, opts.ReleaseTarget)
	if err := errors.Join(checkErr, f.Close()); err != nil {
		return "", fmt.Errorf("release root: %w", err)
	}
	return path, nil
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

// checkReleaseFile is the node's TUF check of an archive file.
func checkReleaseFile(archive *os.File, metadataDir, target string) error {
	_, err := releaseverify.CheckFile(releaseverify.FileCheck{
		RootPath:    releaseverify.RootPath,
		SeenPath:    releaseverify.SeenPath,
		MetadataDir: metadataDir,
		Target:      target,
		File:        archive,
		Now:         time.Now(),
	})
	return err
}
