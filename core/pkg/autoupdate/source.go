package autoupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

const (
	// workDirPerm: only root reads what a fetch leaves.
	workDirPerm = 0o700
	// archiveFilePerm: the downloaded archive is root's alone.
	archiveFilePerm = 0o600
	// downloadName is the archive a fetch leaves in its directory.
	downloadName = "release.tar.gz"
	// fetchPrefix names the directory a fetch is made in, inside WorkDir.
	fetchPrefix = "fetch-"
)

// Source is a release repository as one node reads it: through the root this
// node adopted and the rollback record it keeps.
type Source struct {
	RootPath string
	SeenPath string
	// WorkDir is where metadata and archives are fetched to, below which a
	// fresh directory is made for each fetch. Only root may read it.
	WorkDir string
	Arch    string
	Now     func() time.Time
}

// Release is the newest release of a channel, verified.
type Release struct {
	Version string
	Target  releaseverify.Target
	// Dir holds the metadata and, after Download, the archive.
	Dir string
}

// MetadataDir is where the verified metadata is.
func (r Release) MetadataDir() string { return filepath.Join(r.Dir, "metadata") }

// ArchivePath is where Download leaves the archive.
func (r Release) ArchivePath() string { return filepath.Join(r.Dir, downloadName) }

// SweepStale removes the fetch directories a run that was killed left in
// WorkDir (the archive in one is hundreds of megabytes). The caller holds the
// run lock, so none is in use.
func (s Source) SweepStale() error {
	stale, err := filepath.Glob(filepath.Join(s.WorkDir, fetchPrefix+"*"))
	if err != nil {
		return fmt.Errorf("list the fetch directories in %s: %w", s.WorkDir, err)
	}
	for _, dir := range stale {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove the stale fetch directory %s: %w", dir, err)
		}
	}
	return nil
}

// Newest brings the adopted root up to the newest version repoURL publishes
// (a rotation, releaseverify.Repository.UpdateRoot), fetches the channel's
// metadata from repoURL, verifies it against that root and the rollback record, and returns the newest release for
// this machine's architecture. ok is false when the channel lists none. The
// directory it makes holds the metadata; Remove deletes it.
func (s Source) Newest(ctx context.Context, repoURL, channel string) (rel Release, ok bool, err error) {
	if err := os.MkdirAll(s.WorkDir, workDirPerm); err != nil {
		return Release{}, false, fmt.Errorf("create %s: %w", s.WorkDir, err)
	}
	dir, err := os.MkdirTemp(s.WorkDir, fetchPrefix+"*")
	if err != nil {
		return Release{}, false, fmt.Errorf("create a fetch directory: %w", err)
	}
	defer func() {
		if err != nil || !ok {
			err = errors.Join(err, os.RemoveAll(dir))
		}
	}()
	rel = Release{Dir: dir}
	if err := os.Mkdir(rel.MetadataDir(), workDirPerm); err != nil {
		return Release{}, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, fetchBudget)
	defer cancel()
	update := releaseverify.RootUpdate{RootPath: s.RootPath, SeenPath: s.SeenPath, Now: s.Now()}
	if err := (releaseverify.Repository{BaseURL: repoURL}).Sync(ctx, rel.MetadataDir(), update); err != nil {
		return Release{}, false, fmt.Errorf("fetch the %s channel: %w", channel, err)
	}
	verified, err := releaseverify.Load(s.check(rel, ""))
	if err != nil {
		return Release{}, false, err
	}
	target, ref, found, err := verified.Newest(channel, s.Arch, Compare)
	if err != nil || !found {
		return Release{}, false, err
	}
	rel.Version, rel.Target = ref.Version, target
	return rel, true, nil
}

// Download fetches the release's archive and checks its length and hashes
// against the verified metadata. Only a release that passes has raised the
// rollback record.
func (s Source) Download(ctx context.Context, repoURL string, rel Release) (err error) {
	f, err := os.OpenFile(rel.ArchivePath(), os.O_RDWR|os.O_CREATE|os.O_EXCL, archiveFilePerm)
	if err != nil {
		return fmt.Errorf("create the archive file: %w", err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	ctx, cancel := context.WithTimeout(ctx, fetchBudget)
	defer cancel()
	if err := (releaseverify.Repository{BaseURL: repoURL}).FetchTarget(ctx, rel.Target, f); err != nil {
		return err
	}
	check := s.check(rel, rel.Target.Path)
	check.File = f
	if _, err := releaseverify.CheckFile(check); err != nil {
		return fmt.Errorf("refusing release %s: %w", rel.Version, err)
	}
	return nil
}

// Remove deletes a fetch directory.
func (r Release) Remove() error { return os.RemoveAll(r.Dir) }

func (s Source) check(rel Release, target string) releaseverify.FileCheck {
	return releaseverify.FileCheck{
		RootPath: s.RootPath, SeenPath: s.SeenPath, MetadataDir: rel.MetadataDir(),
		Target: target, Now: s.Now(),
	}
}
