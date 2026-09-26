package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

// PreBuiltManifest describes the contents of a pre-built binary archive. It is
// the manifest `orama build` signs and archivetrust verifies.
type PreBuiltManifest = archivetrust.Manifest

// HasPreBuiltArchive checks if a pre-built binary archive has been extracted
// at /opt/orama/ by looking for the manifest.json file.
func HasPreBuiltArchive() bool {
	_, err := os.Stat(OramaManifest)
	return err == nil
}

// LoadPreBuiltManifest loads and parses the pre-built manifest.
func LoadPreBuiltManifest() (*PreBuiltManifest, error) {
	data, err := os.ReadFile(OramaManifest)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	var manifest PreBuiltManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	return &manifest, nil
}

// installFromPreBuilt installs all binaries from a pre-built archive.
// The archive must already be extracted at /opt/orama/ with:
//   - /opt/orama/bin/ — all pre-compiled binaries
//   - /opt/orama/systemd/ — namespace service templates
//   - /opt/orama/packages/ — optional .deb packages
//   - /opt/orama/manifest.json — archive metadata
//
// Nothing is installed from an archive that does not verify: manifest.sig must
// recover to a signer in the trust anchor (archivetrust.AnchorPath) and every file
// must match the signed manifest. A missing signature, an untrusted signer or
// a missing anchor is a hard failure; there is no unsigned mode.
//
// It holds the archive lock throughout, so `orama node stage-archive` cannot
// replace the archive between its verification and the copies made from it.
func (ps *ProductionSetup) installFromPreBuilt(detected *PreBuiltManifest) (err error) {
	unlock, err := lockArchive(OramaBase)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()

	manifest, err := ps.verifyPreBuiltArchive(detected)
	if err != nil {
		return err
	}
	ps.logf("  Using pre-built binary archive v%s (%s) linux/%s", manifest.Version, manifest.Commit, manifest.Arch)

	// Install minimal system dependencies (no build tools needed)
	if err := ps.installMinimalSystemDeps(); err != nil {
		return fmt.Errorf("install system dependencies: %w", err)
	}

	// Copy binaries to runtime locations
	if err := ps.deployPreBuiltBinaries(manifest); err != nil {
		return fmt.Errorf("failed to deploy pre-built binaries: %w", err)
	}

	// Set capabilities on binaries that need to bind privileged ports
	if err := ps.setCapabilities(); err != nil {
		return fmt.Errorf("failed to set capabilities: %w", err)
	}

	// The privileged helper is copied from the same verified bin/, so it is
	// installed under the same lock.
	if err := ps.ensurePrivHelper(); err != nil {
		return err
	}

	// Install ntfy on every node (feature #72). ntfy is not bundled in
	// the pre-built archive — its installer downloads from upstream and
	// verifies the SHA-256 checksum. ntfy listens on
	// 127.0.0.1:NtfyListenPort only (no public exposure), so it's safe
	// to run cluster-wide; nodes that don't serve a public push.* DNS
	// entry just have an idle ntfy with no inbound traffic. Uniform
	// install means no per-node toggling and no surprises when DNS
	// topology changes.
	//
	// Note: this must run BEFORE Phase 4's ConfigureNtfy, otherwise the
	// chown of /etc/ntfy/server.yml fails because the `ntfy` user
	// doesn't exist yet.
	if err := ps.binaryInstaller.InstallNtfy(); err != nil {
		return fmt.Errorf("install ntfy: %w", err)
	}

	if err := freeResolverPort(ps.isNameserver, ps.disableResolvedStub); err != nil {
		return err
	}

	ps.logf("  ✓ All pre-built binaries installed")
	return nil
}

// installMinimalSystemDeps installs only runtime dependencies (no build tools).
func (ps *ProductionSetup) installMinimalSystemDeps() error {
	ps.logf("  Installing minimal system dependencies...")

	cmd := exec.Command("apt-get", "update")
	if err := cmd.Run(); err != nil {
		ps.logf("    Warning: apt update failed")
	}

	// Only install runtime deps — no build-essential, make, nodejs, npm needed
	// sudo: the orama user's root actions go through `sudo orama-privhelper`,
	// and minimal Debian images ship without it.
	cmd = exec.Command("apt-get", "install", "-y", "curl", "wget", "unzip", "sudo")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install minimal dependencies: %w", err)
	}

	ps.logf("  ✓ Minimal system dependencies installed (no build tools needed)")
	return nil
}

// deployPreBuiltBinaries copies pre-built binaries to their runtime locations.
func (ps *ProductionSetup) deployPreBuiltBinaries(manifest *PreBuiltManifest) error {
	ps.logf("  Deploying pre-built binaries...")

	// Binary → destination mapping
	// Most go to /usr/local/bin/, caddy goes to /usr/bin/
	type binaryDest struct {
		name string
		dest string
	}

	binaries := []binaryDest{
		{name: "orama", dest: "/usr/local/bin/orama"},
		{name: "orama-node", dest: "/usr/local/bin/orama-node"},
		{name: "gateway", dest: "/usr/local/bin/gateway"},
		{name: "identity", dest: "/usr/local/bin/identity"},
		{name: "sfu", dest: "/usr/local/bin/sfu"},
		{name: "turn", dest: "/usr/local/bin/turn"},
		{name: "olric-server", dest: "/usr/local/bin/olric-server"},
		{name: "ipfs", dest: "/usr/local/bin/ipfs"},
		{name: "ipfs-cluster-service", dest: "/usr/local/bin/ipfs-cluster-service"},
		{name: "rqlited", dest: "/usr/local/bin/rqlited"},
		{name: "coredns", dest: "/usr/local/bin/coredns"},
		{name: "caddy", dest: "/usr/bin/caddy"},
	}
	// Note: vault-guardian stays at /opt/orama/bin/ (from archive extraction)
	// and is referenced by absolute path in the systemd service — no copy needed.

	for _, bin := range binaries {
		srcPath := filepath.Join(OramaArchiveBin, bin.name)

		// Skip optional binaries (e.g., coredns on non-nameserver nodes)
		if _, ok := manifest.Checksums[bin.name]; !ok {
			continue
		}

		if err := copyBinary(srcPath, bin.dest); err != nil {
			return fmt.Errorf("failed to copy %s: %w", bin.name, err)
		}
		ps.logf("    ✓ %s → %s", bin.name, bin.dest)
	}

	return nil
}

// setCapabilities sets cap_net_bind_service on binaries that need to bind privileged ports.
// Both the /opt/orama/bin/ originals (used by systemd) and /usr/local/bin/ copies need caps.
func (ps *ProductionSetup) setCapabilities() error {
	caps := []string{
		filepath.Join(OramaArchiveBin, "orama-node"), // systemd uses this path
		"/usr/local/bin/orama-node",                  // PATH copy
		"/usr/bin/caddy",                             // caddy's standard location
	}
	for _, binary := range caps {
		if _, err := os.Stat(binary); os.IsNotExist(err) {
			continue
		}
		cmd := exec.Command("setcap", "cap_net_bind_service=+ep", binary)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("setcap failed on %s: %w (node won't be able to bind port 443)", binary, err)
		}
		ps.logf("    ✓ setcap on %s", binary)
	}
	return nil
}

// freeResolverPort disables the systemd-resolved stub listener on a nameserver
// (needed even in pre-built mode so CoreDNS can bind port 53). Fatal: a
// nameserver whose :53 stays with the stub installs "successfully" and then
// cannot answer for its zone.
func freeResolverPort(isNameserver bool, disable func() error) error {
	if !isNameserver {
		return nil
	}
	if err := disable(); err != nil {
		return fmt.Errorf("disable the systemd-resolved stub listener so CoreDNS can bind :53: %w", err)
	}
	return nil
}

// disableResolvedStub disables systemd-resolved's stub listener so CoreDNS can bind port 53.
func (ps *ProductionSetup) disableResolvedStub() error {
	// Delegate to the coredns installer's method
	return ps.binaryInstaller.coredns.DisableResolvedStubListener()
}

// copyBinary copies a file from src to dest, preserving executable permissions.
// It removes the destination first to avoid ETXTBSY ("text file busy") errors
// when overwriting a binary that is currently running.
func copyBinary(src, dest string) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	// Remove the old binary first. On Linux, if the binary is running,
	// rm unlinks the filename while the kernel keeps the inode alive for
	// the running process. Writing a new file at the same path creates a
	// fresh inode — no ETXTBSY conflict.
	_ = os.Remove(dest)

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	destFile, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(destFile, srcFile); err != nil {
		destFile.Close()
		return err
	}
	// Close reports a failed write (a full disk surfaces here, not in Copy).
	return destFile.Close()
}
