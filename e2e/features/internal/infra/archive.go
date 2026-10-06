//go:build e2e_fleet

package infra

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

// Names inside a build archive (core/pkg/archivetrust/verify.go).
const (
	ManifestName  = archivetrust.ManifestName
	SignatureName = archivetrust.SignatureName
	// maxEditBytes bounds a file an edit reads into memory: only the manifest,
	// the signature and systemd templates are ever edited.
	maxEditBytes = 4 << 20
)

// Edit says how RewriteArchive changes a build archive. Paths are relative
// to the archive root ("manifest.json", "systemd/x.service").
type Edit struct {
	// Drop removes these entries.
	Drop map[string]bool
	// Replace gives these entries new content (they must exist).
	Replace map[string]func(old []byte) ([]byte, error)
	// Add appends regular files that are not in the archive.
	Add map[string][]byte
}

// RewriteArchive copies the gzip tarball src to a new file in dir, applying
// e, and returns its path. The edits model what an attacker between the
// operator's build and the node can do: drop the signature, change a file,
// add one, or re-sign with a key the node does not trust.
func RewriteArchive(t testing.TB, src, dir, name string, e Edit) string {
	t.Helper()
	harness.RequireArchive(t, src)
	dst := filepath.Join(dir, name)
	if err := rewrite(src, dst, e); err != nil {
		t.Fatalf("failed to build the test archive %s: %v", name, err)
	}
	return dst
}

func rewrite(src, dst string, e Edit) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("%s is not gzip: %w", src, err)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	zw := gzip.NewWriter(out)
	tw := tar.NewWriter(zw)
	if err := copyEntries(tar.NewReader(gz), tw, e); err != nil {
		return err
	}
	for name, data := range e.Add {
		if err := writeEntry(tw, &tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg}, data); err != nil {
			return err
		}
	}
	return errors.Join(tw.Close(), zw.Close())
}

func copyEntries(tr *tar.Reader, tw *tar.Writer, e Edit) error {
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read the archive: %w", err)
		}
		rel := strings.TrimPrefix(hdr.Name, "./")
		if e.Drop[rel] {
			continue
		}
		fn, edit := e.Replace[rel]
		if !edit {
			if err := tw.WriteHeader(hdr); err != nil {
				return fmt.Errorf("write %s: %w", rel, err)
			}
			if _, err := io.Copy(tw, tr); err != nil {
				return fmt.Errorf("copy %s: %w", rel, err)
			}
			continue
		}
		old, err := io.ReadAll(io.LimitReader(tr, maxEditBytes))
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		data, err := fn(old)
		if err != nil {
			return fmt.Errorf("edit %s: %w", rel, err)
		}
		if err := writeEntry(tw, hdr, data); err != nil {
			return err
		}
	}
}

func writeEntry(tw *tar.Writer, hdr *tar.Header, data []byte) error {
	h := *hdr
	h.Size = int64(len(data))
	if err := tw.WriteHeader(&h); err != nil {
		return fmt.Errorf("write %s: %w", h.Name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", h.Name, err)
	}
	return nil
}

// ReadArchiveFile returns one small file of a build archive.
func ReadArchiveFile(t testing.TB, src, rel string) []byte {
	t.Helper()
	harness.RequireArchive(t, src)
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			t.Fatalf("%s has no %s: %v", src, rel, err)
		}
		if strings.TrimPrefix(hdr.Name, "./") == rel {
			data, err := io.ReadAll(io.LimitReader(tr, maxEditBytes))
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
}

// ArchiveManifest is the signed manifest of a build archive.
func ArchiveManifest(t testing.TB, src string) archivetrust.Manifest {
	t.Helper()
	var m archivetrust.Manifest
	if err := json.Unmarshal(ReadArchiveFile(t, src, ManifestName), &m); err != nil {
		t.Fatalf("the manifest of %s is not JSON: %v", src, err)
	}
	return m
}

// FirstTemplate is a systemd template the archive carries, for edits that
// must not touch a binary.
func FirstTemplate(t testing.TB, m archivetrust.Manifest) string {
	t.Helper()
	for key := range m.Checksums {
		if strings.HasPrefix(key, "systemd/") {
			return key
		}
	}
	t.Fatal("the archive's manifest lists no systemd template")
	return ""
}

// RunningArchive is the run's archive (HEAD, or the previous release on a
// fleet installed with E2E_INSTALL_PREVIOUS) that the core nodes run now: a
// node joined mid-run installs the build its cluster runs.
func RunningArchive(t testing.TB, f *fleet.Fleet) string {
	t.Helper()
	harness.RequireArchive(t, f.State.ArchivePath)
	staged := string(f.ReadFile(t, f.State.Nodes[0], StagedManifest))
	for _, a := range []string{f.State.ArchivePath, f.State.PreviousArchivePath} {
		if a != "" && string(ReadArchiveFile(t, a, ManifestName)) == staged {
			return a
		}
	}
	t.Fatalf("%s runs a build that is neither of the run's archives", f.State.Nodes[0].Name)
	return ""
}

// SignManifest signs manifestJSON as `orama build` does, with w: the
// signature is valid, the signer is whoever w is.
func SignManifest(manifestJSON []byte, w *wallet.EVM) ([]byte, error) {
	msg, err := archivetrust.SigningMessage(manifestJSON)
	if err != nil {
		return nil, fmt.Errorf("build the signing message: %w", err)
	}
	sig, err := w.Sign(msg)
	if err != nil {
		return nil, fmt.Errorf("sign the manifest: %w", err)
	}
	return []byte(sig), nil
}

// SetManifestArch rewrites the manifest's architecture.
func SetManifestArch(manifestJSON []byte, arch string) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return nil, fmt.Errorf("parse the manifest: %w", err)
	}
	m["arch"] = arch
	return json.Marshal(m)
}

// StagedState is what a refused push or stage must leave exactly as it was:
// the digests of the staged manifest, its signature and the trust anchor.
type StagedState struct{ Manifest, Signature, Anchor string }

// ReadStaged digests the staged build and the trust anchor on n.
func ReadStaged(t testing.TB, f *fleet.Fleet, n fleet.Node) StagedState {
	t.Helper()
	sum := func(p string) string {
		return strings.TrimSpace(f.MustExec(t, n, "sha256sum "+p).Stdout)
	}
	return StagedState{sum(StagedManifest), sum(StagedSignature), sum(ArchiveSigners)}
}
