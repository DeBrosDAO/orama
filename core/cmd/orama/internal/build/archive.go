package build

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

// Manifest describes the contents of a binary archive. It is the type nodes
// verify, so what the build signs and what a node checks cannot drift apart.
type Manifest = archivetrust.Manifest

// generateManifest creates the manifest with SHA256 checksums of every file
// the archive installs: binaries keyed by name, systemd templates by
// "systemd/<name>". Nodes refuse a file the manifest does not list, so nothing
// in the archive escapes the signature.
func (b *Builder) generateManifest() (*Manifest, error) {
	m := &Manifest{
		Version:   b.version,
		Commit:    b.commit,
		Date:      b.date,
		Arch:      b.flags.Arch,
		Checksums: make(map[string]string),
		Signers:   b.flags.Signers,
	}
	if err := addChecksums(m.Checksums, b.binDir, ""); err != nil {
		return nil, err
	}
	for _, sub := range []string{systemdArchiveDir, packagesArchiveDir} {
		dir := filepath.Join(b.tmpDir, sub)
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		if err := addChecksums(m.Checksums, dir, sub+"/"); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// systemdArchiveDir and packagesArchiveDir hold the namespace templates and
// optional packages inside an archive.
const (
	systemdArchiveDir  = "systemd"
	packagesArchiveDir = "packages"
)

// addChecksums records the SHA256 of each file in dir under keyPrefix+name.
func addChecksums(checksums map[string]string, dir, keyPrefix string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return fmt.Errorf("%s holds a directory, %s; an archive's content directories are flat", dir, entry.Name())
		}
		hash, err := sha256File(filepath.Join(dir, entry.Name()))
		if err != nil {
			return fmt.Errorf("failed to hash %s: %w", entry.Name(), err)
		}
		checksums[keyPrefix+entry.Name()] = hash
	}
	return nil
}

// createArchive creates the tar.gz archive from the build directory, with
// manifestJSON as manifest.json and, when set, signature as manifest.sig.
//
// The archive is a function of the files and the build date alone: entries are
// in a fixed order with the build date as their time, root as owner and one of
// two modes, and the gzip header names nothing and carries no time. Two builds
// of one release therefore produce the same bytes.
func (b *Builder) createArchive(outputPath string, manifest *Manifest, manifestJSON []byte, signature string) error {
	fmt.Printf("\nCreating archive: %s\n", outputPath)

	built, err := time.Parse(dateLayout, b.date)
	if err != nil {
		return fmt.Errorf("the build date %q: %w", b.date, err)
	}
	if err := os.WriteFile(filepath.Join(b.tmpDir, archivetrust.ManifestName), manifestJSON, 0644); err != nil {
		return err
	}
	if signature != "" {
		if err := os.WriteFile(filepath.Join(b.tmpDir, archivetrust.SignatureName), []byte(signature), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", archivetrust.SignatureName, err)
		}
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	w := &archiveWriter{tw: tar.NewWriter(gw), modTime: built}
	defer w.tw.Close()

	if err := w.addBuildTree(b.tmpDir, signature != ""); err != nil {
		return err
	}

	fmt.Printf("  files:     %d, all in the manifest\n", len(manifest.Checksums))
	fmt.Printf("  systemd/:  namespace templates\n")
	fmt.Printf("  manifest:  v%s (%s) linux/%s\n", manifest.Version, manifest.Commit, manifest.Arch)

	if info, err := f.Stat(); err == nil {
		fmt.Printf("  size:      %s\n", printer.FormatBytes(info.Size()))
	}
	return nil
}

// archiveWriter writes tar entries whose headers hold nothing about the
// machine that built them.
type archiveWriter struct {
	tw      *tar.Writer
	modTime time.Time
}

// Fixed permissions: an executable file, any other file, and a directory.
const (
	archiveExecMode = 0o755
	archiveFileMode = 0o644
	archiveDirMode  = 0o755
)

// addBuildTree writes the content directories that exist under root, then the
// manifest and, when signed, its signature.
func (w *archiveWriter) addBuildTree(root string, signed bool) error {
	for _, sub := range archivetrust.ContentDirs {
		dir := filepath.Join(root, sub)
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := w.addDir(dir, sub); err != nil {
			return err
		}
	}
	files := []string{archivetrust.ManifestName}
	if signed {
		files = append(files, archivetrust.SignatureName)
	}
	for _, name := range files {
		if err := w.addFile(filepath.Join(root, name), name); err != nil {
			return err
		}
	}
	return nil
}

// addDir adds srcDir and everything under it, in lexical order, under prefix.
func (w *archiveWriter) addDir(srcDir, prefix string) error {
	return filepath.WalkDir(srcDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(filepath.Join(prefix, rel))
		if entry.IsDir() {
			return w.tw.WriteHeader(&tar.Header{
				Name: name + "/", Typeflag: tar.TypeDir, Mode: archiveDirMode, ModTime: w.modTime,
			})
		}
		return w.addFile(path, name)
	})
}

// addFile adds the regular file at srcPath as name.
func (w *archiveWriter) addFile(srcPath, name string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	mode := int64(archiveFileMode)
	if info.Mode()&0o111 != 0 {
		mode = archiveExecMode
	}
	if err := w.tw.WriteHeader(&tar.Header{
		Name: name, Typeflag: tar.TypeReg, Size: info.Size(), Mode: mode, ModTime: w.modTime,
	}); err != nil {
		return err
	}
	_, err = io.Copy(w.tw, f)
	return err
}

// sha256File computes the SHA256 hash of a file.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verifyPinnedSHA256 refuses the file at path unless its SHA-256 equals the
// digest pinned for arch in pins.
func verifyPinnedSHA256(path, name, arch string, pins map[string]string) error {
	want, ok := pins[arch]
	if !ok {
		return fmt.Errorf("no SHA-256 is pinned for %s on %s; pin the release digest in pkg/constants/release_digests.go", name, arch)
	}
	got, err := sha256File(path)
	if err != nil {
		return fmt.Errorf("hash %s: %w", name, err)
	}
	if got != want {
		return fmt.Errorf("%s has sha256 %s, not the pinned %s; it is not the release the version pin names", name, got, want)
	}
	return nil
}

// downloadFile downloads a URL to a local file path.
func downloadFile(url, destPath string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s returned status %d", url, resp.StatusCode)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

// extractFileFromTarball extracts a single file from a tar.gz archive.
func extractFileFromTarball(tarPath, targetFile, destPath string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Match the target file (strip leading ./ if present)
		name := strings.TrimPrefix(header.Name, "./")
		if name == targetFile {
			out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			defer out.Close()

			if _, err := io.Copy(out, tr); err != nil {
				return err
			}
			return nil
		}
	}

	return fmt.Errorf("file %s not found in archive %s", targetFile, tarPath)
}
