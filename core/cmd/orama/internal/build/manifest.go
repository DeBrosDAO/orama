package build

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
)

// ManifestName is the manifest's path inside an archive and under /opt/orama.
const ManifestName = "manifest.json"

// maxManifestBytes bounds how much of a manifest is read; a real one is a few
// kilobytes of checksums.
const maxManifestBytes = 1 << 20

// ReadArchiveManifest returns the manifest.json bytes inside the archive at
// path. The installed copy, /opt/orama/manifest.json, is extracted from the
// same archive, so comparing the two bytes-for-bytes says whether a node runs
// exactly this build.
func ReadArchiveManifest(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s is not a gzip archive: %w", path, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s has no %s — not an orama build archive", path, ManifestName)
		}
		if err != nil {
			return nil, fmt.Errorf("read archive %s: %w", path, err)
		}
		if hdr.Name == ManifestName || hdr.Name == "./"+ManifestName {
			data, err := io.ReadAll(io.LimitReader(tr, maxManifestBytes))
			if err != nil {
				return nil, fmt.Errorf("read %s from %s: %w", ManifestName, path, err)
			}
			return data, nil
		}
	}
}

// ArchiveOwnedPaths are the entries an archive installs under /opt/orama.
// They are removed before a new archive is extracted, so nothing from an older
// build survives next to a newer one — above all not a manifest.sig that no
// longer matches the manifest beside it. /opt/orama also holds the node's
// data (.orama/), which is why the directory itself is never cleared.
var ArchiveOwnedPaths = []string{"bin", "systemd", "packages", "manifest.json", "manifest.sig"}
