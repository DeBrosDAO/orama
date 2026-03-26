// Package update implements OTA updates with A/B partition switching.
//
// Partition layout:
//
//	/dev/sda1 — rootfs-A (current or standby, read-only, dm-verity)
//	/dev/sda2 — rootfs-B (standby or current, read-only, dm-verity)
//	/dev/sda3 — data (LUKS encrypted, persistent)
//
// Uses systemd-boot with Boot Loader Specification (BLS) entries.
// Boot counting: tries_left=3 on new partition, decremented each boot.
// If all tries exhausted, systemd-boot falls back to the other partition.
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	// CheckInterval is how often we check for updates.
	CheckInterval = 1 * time.Hour

	// UpdateURL is the endpoint to check for new versions.
	UpdateURL = "https://updates.orama.network/v1/latest"

	// PartitionA is the rootfs-A device.
	PartitionA = "/dev/sda1"

	// PartitionB is the rootfs-B device.
	PartitionB = "/dev/sda2"
)

// VersionInfo describes an available update.
type VersionInfo struct {
	Version   string `json:"version"`
	Arch      string `json:"arch"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
}

// Manager handles OTA updates.
type Manager struct {
	mu      sync.Mutex
	stopCh  chan struct{}
	stopped bool
}

// NewManager creates a new update manager.
func NewManager() *Manager {
	return &Manager{
		stopCh: make(chan struct{}),
	}
}

// RunLoop periodically checks for updates and applies them.
func (m *Manager) RunLoop() {
	log.Println("update manager started")

	// Initial delay to let the system stabilize after boot
	select {
	case <-time.After(5 * time.Minute):
	case <-m.stopCh:
		return
	}

	ticker := time.NewTicker(CheckInterval)
	defer ticker.Stop()

	for {
		if err := m.checkAndApply(); err != nil {
			log.Printf("update check failed: %v", err)
		}

		select {
		case <-ticker.C:
		case <-m.stopCh:
			return
		}
	}
}

// Stop signals the update loop to exit.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.stopped {
		m.stopped = true
		close(m.stopCh)
	}
}

// checkAndApply checks for a new version and applies it if available.
func (m *Manager) checkAndApply() error {
	arch := detectArch()

	resp, err := http.Get(fmt.Sprintf("%s?arch=%s", UpdateURL, arch))
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil // no update available
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update server returned %d", resp.StatusCode)
	}

	var info VersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return fmt.Errorf("failed to parse update info: %w", err)
	}

	currentVersion := readCurrentVersion()
	if info.Version == currentVersion {
		return nil // already up to date
	}

	log.Printf("update available: %s → %s", currentVersion, info.Version)

	// Download image
	imagePath, err := m.download(info)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(imagePath)

	// Verify checksum
	if err := verifyChecksum(imagePath, info.SHA256); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	// Verify rootwallet signature (EVM personal_sign over the SHA256 hash)
	if info.Signature == "" {
		return fmt.Errorf("update has no signature — refusing to install")
	}
	if err := verifySignature(info.SHA256, info.Signature); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	log.Println("update signature verified")

	// Write to standby partition
	standby := getStandbyPartition()
	if err := writeImage(imagePath, standby); err != nil {
		return fmt.Errorf("failed to write image: %w", err)
	}

	// Update bootloader entry with tries_left=3
	if err := updateBootEntry(standby, info.Version); err != nil {
		return fmt.Errorf("failed to update boot entry: %w", err)
	}

	log.Printf("update %s installed on %s — reboot to activate", info.Version, standby)
	return nil
}

// download fetches the update image to a temporary file.
func (m *Manager) download(info VersionInfo) (string, error) {
	log.Printf("downloading update %s (%d bytes)", info.Version, info.Size)

	resp, err := http.Get(info.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	f, err := os.CreateTemp("/tmp", "orama-update-*.img")
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()

	return f.Name(), nil
}

// verifyChecksum verifies the SHA256 checksum of a file.
func verifyChecksum(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != expected {
		return fmt.Errorf("checksum mismatch: got %s, expected %s", got, expected)
	}

	return nil
}

// writeImage writes a raw image to a partition device.
func writeImage(imagePath, device string) error {
	log.Printf("writing image to %s", device)

	cmd := exec.Command("dd", "if="+imagePath, "of="+device, "bs=4M", "conv=fsync")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("dd failed: %w\n%s", err, string(output))
	}

	return nil
}

// getStandbyPartition returns the partition that is NOT currently booted.
func getStandbyPartition() string {
	// Read current root device from /proc/cmdline
	cmdline, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return PartitionB // fallback
	}

	if strings.Contains(string(cmdline), "root="+PartitionA) {
		return PartitionB
	}
	return PartitionA
}

// getCurrentPartition returns the currently booted partition.
func getCurrentPartition() string {
	cmdline, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return PartitionA
	}
	if strings.Contains(string(cmdline), "root="+PartitionB) {
		return PartitionB
	}
	return PartitionA
}

// updateBootEntry configures systemd-boot to boot from the standby partition
// with tries_left=3 for automatic rollback.
func updateBootEntry(partition, version string) error {
	// Create BLS entry with boot counting
	entryName := "orama-" + version
	entryPath := fmt.Sprintf("/boot/loader/entries/%s+3.conf", entryName)

	content := fmt.Sprintf(`title   OramaOS %s
linux   /vmlinuz
options root=%s ro quiet
`, version, partition)

	if err := os.MkdirAll("/boot/loader/entries", 0755); err != nil {
		return err
	}

	if err := os.WriteFile(entryPath, []byte(content), 0644); err != nil {
		return err
	}

	// Set as one-shot boot target
	cmd := exec.Command("bootctl", "set-oneshot", entryName+"+3")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bootctl set-oneshot failed: %w\n%s", err, string(output))
	}

	return nil
}

// MarkBootSuccessful marks the current boot as successful, removing the
// tries counter so systemd-boot doesn't fall back.
func MarkBootSuccessful() error {
	cmd := exec.Command("bootctl", "set-default", "orama")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bootctl set-default failed: %w\n%s", err, string(output))
	}

	log.Println("boot marked as successful")
	return nil
}

// readCurrentVersion reads the installed version from /etc/orama-version.
func readCurrentVersion() string {
	data, err := os.ReadFile("/etc/orama-version")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}

// detectArch returns the current architecture.
func detectArch() string {
	data, err := os.ReadFile("/proc/sys/kernel/arch")
	if err != nil {
		return "amd64"
	}
	arch := strings.TrimSpace(string(data))
	if arch == "x86_64" {
		return "amd64"
	}
	return arch
}
