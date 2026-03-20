package production

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// PreBuiltManifest describes the contents of a pre-built binary archive.
type PreBuiltManifest struct {
	Version   string            `json:"version"`
	Commit    string            `json:"commit"`
	Date      string            `json:"date"`
	Arch      string            `json:"arch"`
	Checksums map[string]string `json:"checksums"` // filename -> sha256
}

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

// OramaSignerAddress is the Ethereum address authorized to sign build archives.
// Archives signed by any other address are rejected during install.
// This is the DeBros deploy wallet — update if the signing key rotates.
const OramaSignerAddress = "0xb5d8a496c8b2412990d7D467E17727fdF5954afC"

// VerifyArchiveSignature verifies that the pre-built archive was signed by the
// authorized Orama signer. Returns nil if the signature is valid, or if no
// signature file exists (unsigned archives are allowed but logged as a warning).
func VerifyArchiveSignature(manifest *PreBuiltManifest) error {
	sigData, err := os.ReadFile(OramaManifestSig)
	if os.IsNotExist(err) {
		return nil // unsigned archive — caller decides whether to proceed
	}
	if err != nil {
		return fmt.Errorf("failed to read manifest.sig: %w", err)
	}

	// Reproduce the same hash used during signing: SHA256 of compact JSON
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}
	manifestHash := sha256.Sum256(manifestJSON)
	hashHex := hex.EncodeToString(manifestHash[:])

	// EVM personal_sign: keccak256("\x19Ethereum Signed Message:\n" + len + message)
	msg := []byte(hashHex)
	prefix := []byte("\x19Ethereum Signed Message:\n" + fmt.Sprintf("%d", len(msg)))
	ethHash := ethcrypto.Keccak256(prefix, msg)

	// Decode signature
	sigHex := strings.TrimSpace(string(sigData))
	if strings.HasPrefix(sigHex, "0x") || strings.HasPrefix(sigHex, "0X") {
		sigHex = sigHex[2:]
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != 65 {
		return fmt.Errorf("invalid signature format in manifest.sig")
	}

	// Normalize recovery ID
	if sig[64] >= 27 {
		sig[64] -= 27
	}

	// Recover public key from signature
	pub, err := ethcrypto.SigToPub(ethHash, sig)
	if err != nil {
		return fmt.Errorf("signature recovery failed: %w", err)
	}

	recovered := ethcrypto.PubkeyToAddress(*pub).Hex()
	expected := strings.ToLower(OramaSignerAddress)
	got := strings.ToLower(recovered)

	if got != expected {
		return fmt.Errorf("archive signed by %s, expected %s — refusing to install", recovered, OramaSignerAddress)
	}

	return nil
}

// IsArchiveSigned returns true if a manifest.sig file exists alongside the manifest.
func IsArchiveSigned() bool {
	_, err := os.Stat(OramaManifestSig)
	return err == nil
}

// installFromPreBuilt installs all binaries from a pre-built archive.
// The archive must already be extracted at /opt/orama/ with:
//   - /opt/orama/bin/ — all pre-compiled binaries
//   - /opt/orama/systemd/ — namespace service templates
//   - /opt/orama/packages/ — optional .deb packages
//   - /opt/orama/manifest.json — archive metadata
func (ps *ProductionSetup) installFromPreBuilt(manifest *PreBuiltManifest) error {
	ps.logf("  Using pre-built binary archive v%s (%s) linux/%s", manifest.Version, manifest.Commit, manifest.Arch)

	// Verify archive signature if present
	if IsArchiveSigned() {
		if err := VerifyArchiveSignature(manifest); err != nil {
			return fmt.Errorf("archive signature verification failed: %w", err)
		}
		ps.logf("  ✓ Archive signature verified")
	} else {
		ps.logf("  ⚠️  Archive is unsigned — consider using 'orama build --sign'")
	}

	// Install minimal system dependencies (no build tools needed)
	if err := ps.installMinimalSystemDeps(); err != nil {
		ps.logf("  ⚠️  System dependencies warning: %v", err)
	}

	// Copy binaries to runtime locations
	if err := ps.deployPreBuiltBinaries(manifest); err != nil {
		return fmt.Errorf("failed to deploy pre-built binaries: %w", err)
	}

	// Set capabilities on binaries that need to bind privileged ports
	if err := ps.setCapabilities(); err != nil {
		return fmt.Errorf("failed to set capabilities: %w", err)
	}

	// Disable systemd-resolved stub listener for nameserver nodes
	// (needed even in pre-built mode so CoreDNS can bind port 53)
	if ps.isNameserver {
		if err := ps.disableResolvedStub(); err != nil {
			ps.logf("  ⚠️  Failed to disable systemd-resolved stub: %v", err)
		}
	}

	// Install Anyone relay from .deb package if available
	if ps.IsAnyoneRelay() || ps.IsAnyoneClient() {
		if err := ps.installAnyonFromPreBuilt(); err != nil {
			ps.logf("  ⚠️  Anyone install warning: %v", err)
		}
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
	cmd = exec.Command("apt-get", "install", "-y", "curl", "wget", "unzip")
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

		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			ps.logf("    ⚠️  Binary %s not found in archive, skipping", bin.name)
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

// disableResolvedStub disables systemd-resolved's stub listener so CoreDNS can bind port 53.
func (ps *ProductionSetup) disableResolvedStub() error {
	// Delegate to the coredns installer's method
	return ps.binaryInstaller.coredns.DisableResolvedStubListener()
}

// installAnyonFromPreBuilt installs the Anyone relay .deb from the packages dir,
// falling back to apt install if the .deb is not bundled.
func (ps *ProductionSetup) installAnyonFromPreBuilt() error {
	debPath := filepath.Join(OramaPackagesDir, "anon.deb")
	if _, err := os.Stat(debPath); err == nil {
		ps.logf("  Installing Anyone from bundled .deb...")
		cmd := exec.Command("dpkg", "-i", debPath)
		if err := cmd.Run(); err != nil {
			ps.logf("  ⚠️  dpkg -i failed, falling back to apt...")
			cmd = exec.Command("apt-get", "install", "-y", "anon")
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to install anon: %w", err)
			}
		}
		ps.logf("  ✓ Anyone installed from .deb")
		return nil
	}

	// No .deb bundled — fall back to apt (the existing path in source mode)
	ps.logf("  Installing Anyone via apt (not bundled in archive)...")
	cmd := exec.Command("apt-get", "install", "-y", "anon")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install anon via apt: %w", err)
	}
	ps.logf("  ✓ Anyone installed via apt")
	return nil
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
	defer destFile.Close()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		return err
	}

	return nil
}
