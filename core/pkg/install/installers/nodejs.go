package installers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// nodejs.go — the Node.js runtime the deployment units run.
//
// orama-deploy-node@ executes /usr/bin/node, orama-deploy-npm@ and
// orama-deploy-build@ execute /usr/bin/npm. The installer never put either
// there, so every Node.js, Next.js SSR and npm deployment failed to start on a
// node installed from scratch (stagenet e2e, 2026-09-30).
//
// Layout:
//   - Release:  /usr/local/lib/nodejs/node-v<ver>-linux-<arch>/  (root-owned, read-only to units)
//   - Commands: /usr/bin/node, /usr/bin/npm, /usr/bin/npx → symlinks into the release
//
// The release is the official nodejs.org tarball, verified against a digest
// pinned here before anything is unpacked.

const (
	// nodeVersion is the Node.js LTS release installed. Update it with
	// nodeTarballSHA256: hash the downloaded tarballs and compare them with
	// https://nodejs.org/dist/v<ver>/SHASUMS256.txt.
	nodeVersion = "24.21.0"

	nodeInstallRoot = "/usr/local/lib/nodejs"
	nodeBinDir      = "/usr/bin"
	// nodeTarballMaxBytes bounds the download (the release is ~58 MB).
	nodeTarballMaxBytes = 200 * 1024 * 1024
	nodeDownloadTimeout = 10 * time.Minute
)

// nodeTarballSHA256 pins node-v<nodeVersion>-linux-<arch>.tar.gz, keyed by Go arch.
var nodeTarballSHA256 = map[string]string{
	"amd64": "6e1db87ef58b8819e5d5402eff1536491b18edd8eb7bee5ef7897876e88dc5ff",
	"arm64": "724282c3b43aec998aa9527380465b45d229e021b58035f5f4f63095eabfe5d5",
}

// nodeDistArch is nodejs.org's name for a Go arch.
var nodeDistArch = map[string]string{"amd64": "x64", "arm64": "arm64"}

// nodeCommands are linked into nodeBinDir, each to bin/<name> of the release.
var nodeCommands = []string{"node", "npm", "npx"}

// NodeJSInstaller installs the pinned Node.js release.
type NodeJSInstaller struct {
	*BaseInstaller
	// root and binDir are nodeInstallRoot and nodeBinDir; tests point them elsewhere.
	root, binDir string
}

// NewNodeJSInstaller returns an installer for arch (a Go arch, amd64 or arm64).
func NewNodeJSInstaller(arch string, logWriter io.Writer) *NodeJSInstaller {
	return &NodeJSInstaller{BaseInstaller: NewBaseInstaller(arch, logWriter), root: nodeInstallRoot, binDir: nodeBinDir}
}

func (ni *NodeJSInstaller) goArch() string {
	if ni.arch == "" {
		return "amd64"
	}
	return ni.arch
}

// releaseDir is where the pinned release is unpacked.
func (ni *NodeJSInstaller) releaseDir() (string, error) {
	dist, ok := nodeDistArch[ni.goArch()]
	if !ok {
		return "", fmt.Errorf("node.js: unsupported arch %q (want amd64 or arm64)", ni.goArch())
	}
	return filepath.Join(ni.root, fmt.Sprintf("node-v%s-linux-%s", nodeVersion, dist)), nil
}

// IsInstalled reports whether every command links into the pinned release.
func (ni *NodeJSInstaller) IsInstalled() bool {
	dir, err := ni.releaseDir()
	if err != nil {
		return false
	}
	for _, name := range nodeCommands {
		target, err := os.Readlink(filepath.Join(ni.binDir, name))
		if err != nil || target != filepath.Join(dir, "bin", name) {
			return false
		}
		if _, err := os.Stat(target); err != nil {
			return false
		}
	}
	return true
}

// Install downloads, verifies and unpacks the release, then links the
// commands. Idempotent: a node already on the pinned release is left alone,
// and an upgrade replaces the links only after the new release is in place.
func (ni *NodeJSInstaller) Install() error {
	if ni.IsInstalled() {
		fmt.Fprintf(ni.logWriter, "  ✓ Node.js %s already installed\n", nodeVersion)
		return nil
	}
	fmt.Fprintf(ni.logWriter, "  Installing Node.js %s...\n", nodeVersion)
	dir, err := ni.releaseDir()
	if err != nil {
		return err
	}
	tarball, err := ni.download()
	if err != nil {
		return err
	}
	defer os.Remove(tarball)
	if err := ni.unpack(tarball, dir); err != nil {
		return err
	}
	if err := ni.link(dir); err != nil {
		return err
	}
	fmt.Fprintf(ni.logWriter, "  ✓ Node.js %s installed (/usr/bin/node, npm, npx)\n", nodeVersion)
	return nil
}

// download fetches the release tarball into a temp file and verifies it.
func (ni *NodeJSInstaller) download() (string, error) {
	name := fmt.Sprintf("node-v%s-linux-%s.tar.gz", nodeVersion, nodeDistArch[ni.goArch()])
	url := fmt.Sprintf("https://nodejs.org/dist/v%s/%s", nodeVersion, name)
	fmt.Fprintf(ni.logWriter, "    Downloading %s...\n", url)
	data, err := httpGetLimited(&http.Client{Timeout: nodeDownloadTimeout}, url, nodeTarballMaxBytes)
	if err != nil {
		return "", fmt.Errorf("node.js: download %s: %w", url, err)
	}
	if err := verifyNodeTarball(ni.goArch(), data); err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "orama-node-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("node.js: stage the tarball: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("node.js: stage the tarball: %w", err)
	}
	return f.Name(), nil
}

// verifyNodeTarball refuses a tarball whose SHA-256 is not the pinned digest.
func verifyNodeTarball(arch string, data []byte) error {
	want, ok := nodeTarballSHA256[arch]
	if !ok {
		return fmt.Errorf("node.js: no pinned %s digest for %s", nodeVersion, arch)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("node.js tarball SHA-256 mismatch: got %s, want %s — refusing to install (possible supply-chain tampering)", got, want)
	}
	return nil
}

// unpack extracts the verified tarball into a staging directory beside dir
// and renames it into place, so a failed extract never leaves a partial
// release where the links would point.
func (ni *NodeJSInstaller) unpack(tarball, dir string) error {
	if err := os.MkdirAll(ni.root, 0o755); err != nil {
		return fmt.Errorf("node.js: create %s: %w", ni.root, err)
	}
	staging, err := os.MkdirTemp(ni.root, ".staging-")
	if err != nil {
		return fmt.Errorf("node.js: create a staging directory in %s: %w", ni.root, err)
	}
	defer os.RemoveAll(staging)
	if out, err := exec.Command("tar", "-xzf", tarball, "-C", staging, "--no-same-owner").CombinedOutput(); err != nil {
		return fmt.Errorf("node.js: extract the release: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	unpacked := filepath.Join(staging, filepath.Base(dir))
	if _, err := os.Stat(filepath.Join(unpacked, "bin", "node")); err != nil {
		return fmt.Errorf("node.js: the release has no bin/node: %w", err)
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		return fmt.Errorf("node.js: %w", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("node.js: remove the incomplete %s: %w", dir, err)
	}
	if err := os.Rename(unpacked, dir); err != nil {
		return fmt.Errorf("node.js: move the release into %s: %w", dir, err)
	}
	return nil
}

// link points each command at the release, replacing an existing link
// atomically (a new link renamed over the old one).
func (ni *NodeJSInstaller) link(dir string) error {
	for _, name := range nodeCommands {
		dst := filepath.Join(ni.binDir, name)
		tmp := dst + ".orama-new"
		_ = os.Remove(tmp)
		if err := os.Symlink(filepath.Join(dir, "bin", name), tmp); err != nil {
			return fmt.Errorf("node.js: link %s: %w", dst, err)
		}
		if err := os.Rename(tmp, dst); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("node.js: link %s: %w", dst, err)
		}
	}
	return nil
}
