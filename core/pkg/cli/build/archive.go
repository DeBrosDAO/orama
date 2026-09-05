package build

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/printer"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Manifest describes the contents of a binary archive.
type Manifest struct {
	Version   string            `json:"version"`
	Commit    string            `json:"commit"`
	Date      string            `json:"date"`
	Arch      string            `json:"arch"`
	Checksums map[string]string `json:"checksums"` // filename -> sha256
}

// generateManifest creates the manifest with SHA256 checksums of all binaries.
func (b *Builder) generateManifest() (*Manifest, error) {
	m := &Manifest{
		Version:   b.version,
		Commit:    b.commit,
		Date:      b.date,
		Arch:      b.flags.Arch,
		Checksums: make(map[string]string),
	}

	entries, err := os.ReadDir(b.binDir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(b.binDir, entry.Name())
		hash, err := sha256File(path)
		if err != nil {
			return nil, fmt.Errorf("failed to hash %s: %w", entry.Name(), err)
		}
		m.Checksums[entry.Name()] = hash
	}

	return m, nil
}

// createArchive creates the tar.gz archive from the build directory.
func (b *Builder) createArchive(outputPath string, manifest *Manifest) error {
	fmt.Printf("\nCreating archive: %s\n", outputPath)

	// Write manifest.json to tmpDir
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(b.tmpDir, "manifest.json"), manifestData, 0644); err != nil {
		return err
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

	// Add packages/ directory if it exists
	packagesDir := filepath.Join(b.tmpDir, "packages")
	if _, err := os.Stat(packagesDir); err == nil {
		if err := addDirToTar(tw, packagesDir, "packages"); err != nil {
			return err
		}
	}

	// Add manifest.json
	if err := addFileToTar(tw, filepath.Join(b.tmpDir, "manifest.json"), "manifest.json"); err != nil {
		return err
	}

	// Add manifest.sig if it exists (created by --sign)
	sigPath := filepath.Join(b.tmpDir, "manifest.sig")
	if _, err := os.Stat(sigPath); err == nil {
		if err := addFileToTar(tw, sigPath, "manifest.sig"); err != nil {
			return err
		}
	}

	// Print summary
	fmt.Printf("  bin/:      %d binaries\n", len(manifest.Checksums))
	fmt.Printf("  systemd/:  namespace templates\n")
	fmt.Printf("  manifest:  v%s (%s) linux/%s\n", manifest.Version, manifest.Commit, manifest.Arch)

	info, err := f.Stat()
	if err == nil {
		fmt.Printf("  size:      %s\n", printer.FormatBytes(info.Size()))
	}

	return nil
}

// signManifest signs the manifest hash using rootwallet CLI.
// Produces manifest.sig containing the hex-encoded EVM signature.
func (b *Builder) signManifest(manifest *Manifest) error {
	fmt.Printf("\nSigning manifest with rootwallet...\n")

	// Serialize manifest deterministically (compact JSON, sorted keys via json.Marshal)
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	// Hash the manifest JSON
	hash := sha256.Sum256(manifestData)
	hashHex := hex.EncodeToString(hash[:])

	// Call rw sign <hash> --chain evm
	cmd := exec.Command("rw", "sign", hashHex, "--chain", "evm")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rw sign failed: %w\n%s", err, stderr.String())
	}

	signature := strings.TrimSpace(stdout.String())
	if signature == "" {
		return fmt.Errorf("rw sign produced empty signature")
	}

	// Write signature file
	sigPath := filepath.Join(b.tmpDir, "manifest.sig")
	if err := os.WriteFile(sigPath, []byte(signature), 0644); err != nil {
		return fmt.Errorf("failed to write manifest.sig: %w", err)
	}

	fmt.Printf("  Manifest signed (SHA256: %s...)\n", hashHex[:16])
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
