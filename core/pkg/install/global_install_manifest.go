package install

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// DefaultStagedManifest is the manifest of the release extracted at
// /opt/orama: `orama global install --staged-dir /opt/orama/bin` reads its
// files against it.
const DefaultStagedManifest = "/opt/orama/" + archivetrust.ManifestName

// stagedManifestLimit bounds the manifest read; a real one is a few kilobytes.
const stagedManifestLimit = 1 << 20

// stagedManifest is the checksums the release's manifest lists for its bin/
// files, by file name. The archive that manifest came from was verified when it
// was extracted (its signature, or the release root that staged it), and every
// file in it matched; this installer checks again, against the bytes it is
// about to install, that the staged directory still holds exactly those files.
type stagedManifest map[string]string

// readStagedManifest reads the manifest at path. Its directory is the anchor:
// as root, one that is not root's or that others may write is refused.
func readStagedManifest(path string) (stagedManifest, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("manifest path %q must be absolute", path)
	}
	data, err := rootfs.At(filepath.Dir(path)).ReadFile(path, stagedManifestLimit)
	if err != nil {
		return nil, fmt.Errorf("read the release manifest %s (it comes with the release's archive; pass --manifest if it is elsewhere): %w", path, err)
	}
	m, err := archivetrust.ParseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := stagedManifest{}
	for key, sum := range m.Checksums {
		if !strings.Contains(key, "/") {
			out[key] = strings.ToLower(sum)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no bin/ files", path)
	}
	return out, nil
}

// verify refuses data unless the manifest lists name with the SHA-256 data has.
func (m stagedManifest) verify(name string, data []byte) error {
	want, ok := m[name]
	if !ok {
		return fmt.Errorf("the release manifest does not list %s, so the staged one is not the release's; build the release with the global layer (orama maint build, without %s)", name, "--skip-global-layer")
	}
	sum := sha256.Sum256(data)
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(want)) != 1 {
		return fmt.Errorf("the staged %s has sha256 %x but the release manifest lists %s: it is not the file the release shipped", name, sum, want)
	}
	return nil
}

// verifierFiles are the names of the verifier and its digest file.
const (
	globalVerifierBinary = constants.ChainVerifierBinary
	globalVerifierDigest = constants.ChainVerifierSHA256File
	// verifierDigestLimit bounds the digest file: one hex line.
	verifierDigestLimit = 1 << 10
)
