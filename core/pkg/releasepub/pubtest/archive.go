// Package pubtest makes archives for tests of the release tool. They are tar.gz
// files with a manifest, not real builds.
package pubtest

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

// Options say what an archive carries beyond a plain release's.
type Options struct {
	// Signed adds a manifest.sig. Signers and ReleaseRoot put a signer list and
	// a release root in the manifest.
	Signed      bool
	Signers     []string
	ReleaseRoot string
	// ClusterOnly leaves out the global layer (amd64 archives have it
	// otherwise).
	ClusterOnly bool
	// ManifestVersion and ManifestArch, when set, replace what the file name says.
	ManifestVersion, ManifestArch string
}

// Archive writes orama-<version>-linux-<arch>.tar.gz in a new directory, its
// bytes depending on content, and returns the path.
func Archive(t testing.TB, version, arch, content string) string {
	t.Helper()
	return ArchiveWith(t, version, arch, content, Options{})
}

// ArchiveWith is Archive with options.
func ArchiveWith(t testing.TB, version, arch, content string, o Options) string {
	t.Helper()
	manifest := archivetrust.Manifest{Version: version, Commit: "abc1234", Date: "2026-10-10T00:00:00Z", Arch: arch, Checksums: map[string]string{"orama": "00"}, Signers: o.Signers, ReleaseRoot: o.ReleaseRoot}
	if o.ManifestVersion != "" {
		manifest.Version = o.ManifestVersion
	}
	if o.ManifestArch != "" {
		manifest.Arch = o.ManifestArch
	}
	if arch == "amd64" && !o.ClusterOnly {
		for _, name := range []string{constants.ChainDaemonName, constants.ChainVerifierBinary, constants.ChainVerifierSHA256File, "orama-global", constants.CosmovisorTarball(arch)} {
			manifest.Checksums[name] = "00"
		}
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "orama-"+version+"-linux-"+arch+".tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	entries := map[string]string{"bin/orama": content, archivetrust.ManifestName: string(manifestJSON)}
	if o.Signed {
		entries[archivetrust.SignatureName] = "0xsig"
	}
	for _, name := range []string{"bin/orama", archivetrust.ManifestName, archivetrust.SignatureName} {
		body, ok := entries[name]
		if !ok {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
