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
func (b *Builder) createArchive(outputPath string, manifest *Manifest, manifestJSON []byte, signature string) error {
	fmt.Printf("\nCreating archive: %s\n", outputPath)

	if err := os.WriteFile(filepath.Join(b.tmpDir, archivetrust.ManifestName), manifestJSON, 0644); err != nil {
		return err
	}
	if signature != "" {
		if err := os.WriteFile(filepath.Join(b.tmpDir, archivetrust.SignatureName), []byte(signature), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", archivetrust.SignatureName, err)
		}
	}

	// Create output file
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Add bin/ directory
	if err := addDirToTar(tw, b.binDir, "bin"); err != nil {
		return err
	}

	// Add systemd/ directory
	systemdDir := filepath.Join(b.tmpDir, "systemd")
	if _, err := os.Stat(systemdDir); err == nil {
		if err := addDirToTar(tw, systemdDir, "systemd"); err != nil {
			return err
		}
	}

	// Add packages/ directory if it exists (checksummed like the rest)
	packagesDir := filepath.Join(b.tmpDir, packagesArchiveDir)
	if _, err := os.Stat(packagesDir); err == nil {
		if err := addDirToTar(tw, packagesDir, "packages"); err != nil {
			return err
		}
	}

	// Add manifest.json
	if err := addFileToTar(tw, filepath.Join(b.tmpDir, archivetrust.ManifestName), archivetrust.ManifestName); err != nil {
		return err
	}

	if signature != "" {
		if err := addFileToTar(tw, filepath.Join(b.tmpDir, archivetrust.SignatureName), archivetrust.SignatureName); err != nil {
			return err
		}
	}

	// Print summary
	fmt.Printf("  files:     %d, all in the manifest\n", len(manifest.Checksums))
	fmt.Printf("  systemd/:  namespace templates\n")
	fmt.Printf("  manifest:  v%s (%s) linux/%s\n", manifest.Version, manifest.Commit, manifest.Arch)

	info, err := f.Stat()
	if err == nil {
		fmt.Printf("  size:      %s\n", printer.FormatBytes(info.Size()))
	}

	return nil
}

// addDirToTar adds all files in a directory to the tar archive under the given prefix.
func addDirToTar(tw *tar.Writer, srcDir, prefix string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate relative path
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		tarPath := filepath.Join(prefix, relPath)

		if info.IsDir() {
			header := &tar.Header{
				Name:     tarPath + "/",
				Mode:     0755,
				Typeflag: tar.TypeDir,
			}
			return tw.WriteHeader(header)
		}

		return addFileToTar(tw, path, tarPath)
	})
}

// addFileToTar adds a single file to the tar archive.
func addFileToTar(tw *tar.Writer, srcPath, tarPath string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	header := &tar.Header{
		Name: tarPath,
		Size: info.Size(),
		Mode: int64(info.Mode()),
	}

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	_, err = io.Copy(tw, f)
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
