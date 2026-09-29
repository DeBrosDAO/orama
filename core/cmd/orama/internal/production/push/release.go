package push

import (
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

// checkRelease runs the TUF check when the release root was asked for.
// Asking for it with half the flags is an error, never the wallet path.
func checkRelease(t stageTarget, opts StageOptions) error {
	if opts.ReleaseMetadata == "" && opts.ReleaseTarget == "" {
		return nil
	}
	if opts.ReleaseMetadata == "" || opts.ReleaseTarget == "" {
		return clierr.Usage("--release-metadata and --release-target go together")
	}
	if err := t.checkRelease(opts.Archive, opts.ReleaseMetadata, opts.ReleaseTarget); err != nil {
		return fmt.Errorf("release root: %w", err)
	}
	return nil
}

// checkReleaseFile is the node's TUF check of an archive file.
func checkReleaseFile(archive, metadataDir, target string) error {
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
