//go:build e2e_fleet

package tuf

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

// Channel is the release channel the tests publish to.
const Channel = "stable"

// ReleaseArchive turns the run's signed archive into the unsigned release CI
// publishes: the same files with the manifest at version, no signature. The
// manifest's checksums do not cover the manifest, so the files still match it.
func ReleaseArchive(t testing.TB, signed []byte, version string) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(signed))
	if err != nil {
		t.Fatalf("the run's archive is not gzip: %v", err)
	}
	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	tw := tar.NewWriter(gw)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read the run's archive: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", hdr.Name, err)
		}
		switch hdr.Name {
		case archivetrust.SignatureName:
			continue
		case archivetrust.ManifestName:
			body = releaseManifest(t, body, version)
			hdr.Size = int64(len(body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), gw.Close()); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func releaseManifest(t testing.TB, data []byte, version string) []byte {
	t.Helper()
	m, err := archivetrust.ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	m.Version, m.Signers, m.ReleaseRoot = version, nil, ""
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// manifestOf is the manifest of an archive.
func manifestOf(t testing.TB, archive []byte) *archivetrust.Manifest {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			t.Fatalf("the archive has no %s: %v", archivetrust.ManifestName, err)
		}
		if hdr.Name != archivetrust.ManifestName {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		m, err := archivetrust.ParseManifest(body)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
}

// ManifestArch is the architecture the archive is built for.
func ManifestArch(t testing.TB, archive []byte) string { return manifestOf(t, archive).Arch }

// ManifestVersion is the version the archive was built as.
func ManifestVersion(t testing.TB, archive []byte) string { return manifestOf(t, archive).Version }

// ChannelRepo is a generated release root whose targets are archives under
// channel prefixes. Every key is made for the test; none is a production key.
type ChannelRepo struct {
	keys releaserepo.Keys
	root []byte
	// until is when the root and the roles under it expire.
	until time.Time
}

// NewChannelRepo generates a root valid for a day.
func NewChannelRepo(t testing.TB) *ChannelRepo {
	t.Helper()
	keys, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(24 * time.Hour)
	root, err := releaserepo.NewRoot(keys, until)
	if err != nil {
		t.Fatal(err)
	}
	return &ChannelRepo{keys: keys, root: root, until: until}
}

// Root is the signed root.json.
func (r *ChannelRepo) Root() []byte { return append([]byte(nil), r.root...) }

// Digest is the root's SHA-256, how a node and the CLI name it.
func (r *ChannelRepo) Digest() string { return releaseverify.RootDigest(r.root) }

// Files is the metadata at snapshot version naming archives (version ->
// bytes) on the stable channel, by file name, and the archives by the path a
// repository serves them at (targets/stable/orama-<version>-linux-<arch>.tar.gz).
// A zero timestampExpires is the root's own expiry; one in the past is a
// frozen repository.
func (r *ChannelRepo) Files(t testing.TB, snapshot int64, timestampExpires time.Time, arch string, archives map[string][]byte) map[string][]byte {
	t.Helper()
	targets := map[string][]byte{}
	for version, data := range archives {
		targets[releaseverify.ArchiveTarget(Channel, version, arch)] = data
	}
	files, err := releaserepo.Build(r.keys, releaserepo.Spec{
		Version: snapshot, RootValidUntil: r.until, TimestampExpires: timestampExpires,
		Targets: targets,
	})
	if err != nil {
		t.Fatal(err)
	}
	for version, data := range archives {
		files["targets/"+releaseverify.ArchiveTarget(Channel, version, arch)] = data
	}
	return files
}
