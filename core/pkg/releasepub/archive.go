package releasepub

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

// globalLayerFiles are the files an amd64 release carries for `orama global
// install` (orama maint build, without --skip-global-layer).
func globalLayerFiles(arch string) []string {
	return []string{
		constants.ChainDaemonName, constants.ChainVerifierBinary, constants.ChainVerifierSHA256File,
		"orama-global", constants.CosmovisorTarball(arch),
	}
}

// checkArchive refuses an archive a node could not install from a release: one
// signed by a wallet or carrying a signer rotation or a release root (a node
// installs a release through the release root alone, and only an unsigned
// archive with none of those), one whose manifest is for another version or
// architecture than its name says, and, for amd64 and unless clusterOnly,
// one without the global layer. It reads the manifest; the build that made the
// archive hashed every file.
func checkArchive(path string, ref releaseverify.ArchiveRef, clusterOnly bool) error {
	manifestJSON, signed, err := archivetrust.ReadArchiveManifest(path)
	if err != nil {
		return err
	}
	m, err := archivetrust.ParseManifest(manifestJSON)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	switch {
	case signed:
		return fmt.Errorf("%s carries a %s: a release is built with `orama maint build --unsigned` and trusted through the release root, and a node refuses a signed archive staged that way", path, archivetrust.SignatureName)
	case m.Signers != nil || m.ReleaseRoot != "":
		return fmt.Errorf("%s carries a signer list or a release root; a release decides what code runs, not who is trusted", path)
	case m.Version != ref.Version || m.Arch != ref.Arch:
		return fmt.Errorf("%s is named for version %s linux/%s but its manifest says %s linux/%s", path, ref.Version, ref.Arch, m.Version, m.Arch)
	}
	if ref.Arch != "amd64" || clusterOnly {
		return nil
	}
	var missing []string
	for _, name := range globalLayerFiles(ref.Arch) {
		if _, ok := m.Checksums[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s lacks the global layer (%s): build it without --skip-global-layer, or pass --allow-cluster-only to release a cluster-only archive", path, strings.Join(missing, ", "))
	}
	return nil
}
