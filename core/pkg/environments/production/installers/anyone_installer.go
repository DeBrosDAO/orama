package installers

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// AnyoneInstaller installs the anon binary and writes a client-only anonrc
// (SOCKS5 on 127.0.0.1:9050) for /v1/proxy/anon.
type AnyoneInstaller struct {
	*BaseInstaller
}

// NewAnyoneInstaller creates an Anyone client installer.
func NewAnyoneInstaller(arch string, logWriter io.Writer) *AnyoneInstaller {
	return &AnyoneInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
	}
}

// IsInstalled checks if the anon binary is installed
func (ai *AnyoneInstaller) IsInstalled() bool {
	// Check if anon binary exists
	if _, err := exec.LookPath("anon"); err == nil {
		return true
	}
	// Check common installation path
	if _, err := os.Stat("/usr/bin/anon"); err == nil {
		return true
	}
	return false
}

// Install downloads and installs the anon package used by the Anyone client.
func (ai *AnyoneInstaller) Install() error {
	fmt.Fprintf(ai.logWriter, "  Installing Anyone client...\n")

	// Create required directories
	dirs := []string{
		"/etc/anon",
		"/var/lib/anon",
		"/var/log/anon",
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	fmt.Fprintf(ai.logWriter, "    Installing anon package...\n")

	// Add the Anyone repository and install the package directly
	// This is more reliable than running the interactive script
	if err := ai.addAnyoneRepository(); err != nil {
		return fmt.Errorf("failed to add Anyone repository: %w", err)
	}

	// Pre-accept terms via debconf to avoid interactive prompt during apt install.
	// The anon package preinst script checks "anon/terms" via debconf.
	preseed := exec.Command("bash", "-c", `echo "anon anon/terms boolean true" | debconf-set-selections`)
	if output, err := preseed.CombinedOutput(); err != nil {
		fmt.Fprintf(ai.logWriter, "    ⚠️  debconf preseed warning: %v (%s)\n", err, string(output))
	}

	// Install the anon package non-interactively.
	// --force-confold keeps existing config files if present (e.g. during migration).
	cmd := exec.Command("apt-get", "install", "-y", "-o", "Dpkg::Options::=--force-confold", "anon")
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install anon package: %w\n%s", err, string(output))
	}

	// Stop and disable the default 'anon' systemd service that the apt package
	// auto-enables. Orama runs orama-anyone-client instead.
	exec.Command("systemctl", "stop", "anon").Run()
	exec.Command("systemctl", "disable", "anon").Run()

	// Fix logrotate: the apt package installs /etc/logrotate.d/anon with
	// "invoke-rc.d anon reload" in postrotate, but we disabled the anon service.
	ai.fixLogrotate()

	fmt.Fprintf(ai.logWriter, "  ✓ Anyone client binary installed\n")

	// nyx talks to ControlPort 9051 (optional operator tool)
	if err := ai.installNyx(); err != nil {
		fmt.Fprintf(ai.logWriter, "  ⚠️  nyx install warning: %v\n", err)
	}

	return nil
}

// fixLogrotate replaces the apt-provided logrotate config which uses
// "invoke-rc.d anon reload" (broken because we disable the anon service).
func (ai *AnyoneInstaller) fixLogrotate() {
	config := `/var/log/anon/*log {
	daily
	rotate 5
	compress
	delaycompress
	missingok
	notifempty
	create 0640 debian-anon adm
	sharedscripts
	postrotate
		/usr/bin/killall -HUP anon 2>/dev/null || true
	endscript
}
`
	if err := os.WriteFile("/etc/logrotate.d/anon", []byte(config), 0644); err != nil {
		fmt.Fprintf(ai.logWriter, "  ⚠️  logrotate fix warning: %v\n", err)
	}
}

// installNyx installs nyx for the Anyone control port.
func (ai *AnyoneInstaller) installNyx() error {
	if _, err := exec.LookPath("nyx"); err == nil {
		fmt.Fprintf(ai.logWriter, "  ✓ nyx already installed\n")
		return nil
	}

	fmt.Fprintf(ai.logWriter, "  Installing nyx (control-port monitor)...\n")
	cmd := exec.Command("apt-get", "install", "-y", "nyx")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install nyx: %w\n%s", err, string(output))
	}

	fmt.Fprintf(ai.logWriter, "  ✓ nyx installed (use 'nyx' to monitor ControlPort 9051)\n")
	return nil
}

// addAnyoneRepository adds the Anyone apt repository
func (ai *AnyoneInstaller) addAnyoneRepository() error {
	// Add GPG key using wget (as per official install script)
	fmt.Fprintf(ai.logWriter, "    Adding Anyone repository key...\n")

	// Download and add the GPG key using the official method
	keyPath := "/etc/apt/trusted.gpg.d/anon.asc"
	cmd := exec.Command("bash", "-c", "wget -qO- https://deb.en.anyone.tech/anon.asc | tee "+keyPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to download GPG key: %w\n%s", err, string(output))
	}

	// Add repository
	fmt.Fprintf(ai.logWriter, "    Adding Anyone repository...\n")

	// Determine distribution codename
	codename := "stable"
	if data, err := exec.Command("lsb_release", "-cs").Output(); err == nil {
		codename = strings.TrimSpace(string(data))
	}

	// Create sources.list entry using the official format: anon-live-$VERSION_CODENAME
	repoLine := fmt.Sprintf("deb [signed-by=%s] https://deb.en.anyone.tech anon-live-%s main\n", keyPath, codename)
	if err := os.WriteFile("/etc/apt/sources.list.d/anon.list", []byte(repoLine), 0644); err != nil {
		return fmt.Errorf("failed to write repository file: %w", err)
	}

	// Update apt
	cmd = exec.Command("apt-get", "update", "--yes")
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(ai.logWriter, "    ⚠️  Warning: apt update failed: %s\n", string(output))
	}

	return nil
}

// ConfigureClient generates a client-only anonrc (SocksPort 9050, no relay)
func (ai *AnyoneInstaller) ConfigureClient() error {
	fmt.Fprintf(ai.logWriter, "  Configuring Anyone client-only mode...\n")

	configPath := "/etc/anon/anonrc"

	// Backup existing config if it exists
	if _, err := os.Stat(configPath); err == nil {
		backupPath := configPath + ".bak"
		if err := exec.Command("cp", configPath, backupPath).Run(); err != nil {
			fmt.Fprintf(ai.logWriter, "    ⚠️  Warning: failed to backup existing config: %v\n", err)
		}
	}

	config := `# Anyone Client Configuration (Managed by Orama Network)
# Client-only mode — no relay traffic, no ORPort

AgreeToTerms 1
# Bind the SOCKS proxy to loopback only — it is consumed by the local gateway
# (/v1/proxy/anon → localhost:9050) and must never be reachable off-host.
SocksPort 127.0.0.1:9050

Log notice file /var/log/anon/notices.log
DataDirectory /var/lib/anon
ControlPort 9051
`

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write client anonrc: %w", err)
	}

	fmt.Fprintf(ai.logWriter, "  ✓ Anyone client configured (SocksPort 9050)\n")
	return nil
}
