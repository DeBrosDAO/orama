package installers

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// AnyoneRelayConfig holds configuration for the Anyone relay
type AnyoneRelayConfig struct {
	Nickname  string // Relay nickname (1-19 alphanumeric)
	Contact   string // Contact info (email or @telegram)
	Wallet    string // Ethereum wallet for rewards
	ORPort    int    // ORPort for relay (default 9001)
	ExitRelay bool   // Whether to run as exit relay
	Migrate   bool   // Whether to migrate existing installation
	MyFamily  string // Comma-separated list of family fingerprints (for multi-relay operators)
}

// ExistingAnyoneInfo contains information about an existing Anyone installation
type ExistingAnyoneInfo struct {
	HasKeys     bool
	HasConfig   bool
	IsRunning   bool
	Fingerprint string
	Wallet      string
	Nickname    string
	MyFamily    string // Existing MyFamily setting (important to preserve!)
	ConfigPath  string
	KeysPath    string
}

// AnyoneRelayInstaller handles Anyone relay installation
type AnyoneRelayInstaller struct {
	*BaseInstaller
	config AnyoneRelayConfig
}

// NewAnyoneRelayInstaller creates a new Anyone relay installer
func NewAnyoneRelayInstaller(arch string, logWriter io.Writer, config AnyoneRelayConfig) *AnyoneRelayInstaller {
	return &AnyoneRelayInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
		config:        config,
	}
}

// DetectExistingAnyoneInstallation checks for an existing Anyone relay installation
func DetectExistingAnyoneInstallation() (*ExistingAnyoneInfo, error) {
	info := &ExistingAnyoneInfo{
		ConfigPath: "/etc/anon/anonrc",
		KeysPath:   "/var/lib/anon/keys",
	}

	// Check for existing keys
	if _, err := os.Stat(info.KeysPath); err == nil {
		info.HasKeys = true
	}

	// Check for existing config
	if _, err := os.Stat(info.ConfigPath); err == nil {
		info.HasConfig = true

		// Parse existing config for fingerprint/wallet/nickname
		if file, err := os.Open(info.ConfigPath); err == nil {
			defer file.Close()
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "#") {
					continue
				}

				// Parse Nickname
				if strings.HasPrefix(line, "Nickname ") {
					info.Nickname = strings.TrimPrefix(line, "Nickname ")
				}

				// Parse ContactInfo for wallet (format: ... @anon:0x... or @anon: 0x...)
				if strings.HasPrefix(line, "ContactInfo ") {
					contact := strings.TrimPrefix(line, "ContactInfo ")
					// Extract wallet address from @anon: prefix (handle space after colon)
					if idx := strings.Index(contact, "@anon:"); idx != -1 {
						wallet := strings.TrimSpace(contact[idx+6:])
						info.Wallet = wallet
					}
				}

				// Parse MyFamily (critical to preserve for multi-relay operators)
				if strings.HasPrefix(line, "MyFamily ") {
					info.MyFamily = strings.TrimPrefix(line, "MyFamily ")
				}
			}
		}
	}

	// Check if anon service is running
	cmd := exec.Command("systemctl", "is-active", "--quiet", "anon")
	if cmd.Run() == nil {
		info.IsRunning = true
	}

	// Try to get fingerprint from data directory (it's in /var/lib/anon/, not keys/)
	fingerprintFile := "/var/lib/anon/fingerprint"
	if data, err := os.ReadFile(fingerprintFile); err == nil {
		info.Fingerprint = strings.TrimSpace(string(data))
	}

	// Return nil if no installation detected
	if !info.HasKeys && !info.HasConfig && !info.IsRunning {
		return nil, nil
	}

	return info, nil
}

// IsInstalled checks if the anon relay binary is installed
func (ari *AnyoneRelayInstaller) IsInstalled() bool {
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

// Install downloads and installs the Anyone relay using the official install script
func (ari *AnyoneRelayInstaller) Install() error {
	fmt.Fprintf(ari.logWriter, "  Installing Anyone relay...\n")

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

	// Download the official install script
	installScript := "/tmp/anon-install.sh"
	scriptURL := "https://raw.githubusercontent.com/anyone-protocol/anon-install/refs/heads/main/install.sh"

	fmt.Fprintf(ari.logWriter, "    Downloading install script...\n")
	if err := DownloadFile(scriptURL, installScript); err != nil {
		return fmt.Errorf("failed to download install script: %w", err)
	}

	// Make script executable
	if err := os.Chmod(installScript, 0755); err != nil {
		return fmt.Errorf("failed to chmod install script: %w", err)
	}

	// The official script is interactive, so we need to provide answers via stdin
	// or install the package directly
	fmt.Fprintf(ari.logWriter, "    Installing anon package...\n")

	// Add the Anyone repository and install the package directly
	// This is more reliable than running the interactive script
	if err := ari.addAnyoneRepository(); err != nil {
		return fmt.Errorf("failed to add Anyone repository: %w", err)
	}

	// Install the anon package
	cmd := exec.Command("apt-get", "install", "-y", "anon")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install anon package: %w\n%s", err, string(output))
	}

	// Clean up
	os.Remove(installScript)

	fmt.Fprintf(ari.logWriter, "  ✓ Anyone relay binary installed\n")

	// Install nyx for relay monitoring (connects to ControlPort 9051)
	if err := ari.installNyx(); err != nil {
		fmt.Fprintf(ari.logWriter, "  ⚠️  nyx install warning: %v\n", err)
	}

	return nil
}

// installNyx installs the nyx relay monitor tool
func (ari *AnyoneRelayInstaller) installNyx() error {
	// Check if already installed
	if _, err := exec.LookPath("nyx"); err == nil {
		fmt.Fprintf(ari.logWriter, "  ✓ nyx already installed\n")
		return nil
	}

	fmt.Fprintf(ari.logWriter, "  Installing nyx (relay monitor)...\n")
	cmd := exec.Command("apt-get", "install", "-y", "nyx")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install nyx: %w\n%s", err, string(output))
	}

	fmt.Fprintf(ari.logWriter, "  ✓ nyx installed (use 'nyx' to monitor relay on ControlPort 9051)\n")
	return nil
}

// addAnyoneRepository adds the Anyone apt repository
func (ari *AnyoneRelayInstaller) addAnyoneRepository() error {
	// Add GPG key using wget (as per official install script)
	fmt.Fprintf(ari.logWriter, "    Adding Anyone repository key...\n")

	// Download and add the GPG key using the official method
	keyPath := "/etc/apt/trusted.gpg.d/anon.asc"
	cmd := exec.Command("bash", "-c", "wget -qO- https://deb.en.anyone.tech/anon.asc | tee "+keyPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to download GPG key: %w\n%s", err, string(output))
	}

	// Add repository
	fmt.Fprintf(ari.logWriter, "    Adding Anyone repository...\n")

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
		fmt.Fprintf(ari.logWriter, "    ⚠️  Warning: apt update failed: %s\n", string(output))
	}

	return nil
}

// Configure generates the anonrc configuration file
func (ari *AnyoneRelayInstaller) Configure() error {
	fmt.Fprintf(ari.logWriter, "  Configuring Anyone relay...\n")

	configPath := "/etc/anon/anonrc"

	// Backup existing config if it exists
	if _, err := os.Stat(configPath); err == nil {
		backupPath := configPath + ".bak"
		if err := exec.Command("cp", configPath, backupPath).Run(); err != nil {
			fmt.Fprintf(ari.logWriter, "    ⚠️  Warning: failed to backup existing config: %v\n", err)
		} else {
			fmt.Fprintf(ari.logWriter, "    Backed up existing config to %s\n", backupPath)
		}
	}

	// Generate configuration
	config := ari.generateAnonrc()

	// Write configuration
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write anonrc: %w", err)
	}

	fmt.Fprintf(ari.logWriter, "  ✓ Anyone relay configured\n")
	return nil
}

// generateAnonrc creates the anonrc configuration content
func (ari *AnyoneRelayInstaller) generateAnonrc() string {
	var sb strings.Builder

	sb.WriteString("# Anyone Relay Configuration (Managed by Orama Network)\n")
	sb.WriteString("# Generated automatically - manual edits may be overwritten\n\n")

	// Nickname
	sb.WriteString(fmt.Sprintf("Nickname %s\n", ari.config.Nickname))

	// Contact info with wallet
	if ari.config.Wallet != "" {
		sb.WriteString(fmt.Sprintf("ContactInfo %s @anon:%s\n", ari.config.Contact, ari.config.Wallet))
	} else {
		sb.WriteString(fmt.Sprintf("ContactInfo %s\n", ari.config.Contact))
	}

	sb.WriteString("\n")

	// ORPort
	sb.WriteString(fmt.Sprintf("ORPort %d\n", ari.config.ORPort))

	// SOCKS port for local use
	sb.WriteString("SocksPort 9050\n")

	sb.WriteString("\n")

	// Exit relay configuration
	if ari.config.ExitRelay {
		sb.WriteString("ExitRelay 1\n")
		sb.WriteString("# Exit policy - allow common ports\n")
		sb.WriteString("ExitPolicy accept *:80\n")
		sb.WriteString("ExitPolicy accept *:443\n")
		sb.WriteString("ExitPolicy reject *:*\n")
	} else {
		sb.WriteString("ExitRelay 0\n")
		sb.WriteString("ExitPolicy reject *:*\n")
	}

	sb.WriteString("\n")

	// Logging
	sb.WriteString("Log notice file /var/log/anon/notices.log\n")

	// Data directory
	sb.WriteString("DataDirectory /var/lib/anon\n")

	// Control port for monitoring
	sb.WriteString("ControlPort 9051\n")

	// MyFamily for multi-relay operators (preserve from existing config)
	if ari.config.MyFamily != "" {
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("MyFamily %s\n", ari.config.MyFamily))
	}

	return sb.String()
}

// MigrateExistingInstallation migrates an existing Anyone installation into Orama Network
func (ari *AnyoneRelayInstaller) MigrateExistingInstallation(existing *ExistingAnyoneInfo, backupDir string) error {
	fmt.Fprintf(ari.logWriter, "  Migrating existing Anyone installation...\n")

	// Create backup directory
	backupAnonDir := filepath.Join(backupDir, "anon-backup")
	if err := os.MkdirAll(backupAnonDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Stop existing anon service if running
	if existing.IsRunning {
		fmt.Fprintf(ari.logWriter, "    Stopping existing anon service...\n")
		exec.Command("systemctl", "stop", "anon").Run()
	}

	// Backup keys
	if existing.HasKeys {
		fmt.Fprintf(ari.logWriter, "    Backing up keys...\n")
		keysBackup := filepath.Join(backupAnonDir, "keys")
		if err := exec.Command("cp", "-r", existing.KeysPath, keysBackup).Run(); err != nil {
			return fmt.Errorf("failed to backup keys: %w", err)
		}
	}

	// Backup config
	if existing.HasConfig {
		fmt.Fprintf(ari.logWriter, "    Backing up config...\n")
		configBackup := filepath.Join(backupAnonDir, "anonrc")
		if err := exec.Command("cp", existing.ConfigPath, configBackup).Run(); err != nil {
			return fmt.Errorf("failed to backup config: %w", err)
		}
	}

	// Preserve nickname from existing installation if not provided
	if ari.config.Nickname == "" && existing.Nickname != "" {
		fmt.Fprintf(ari.logWriter, "    Using existing nickname: %s\n", existing.Nickname)
		ari.config.Nickname = existing.Nickname
	}

	// Preserve wallet from existing installation if not provided
	if ari.config.Wallet == "" && existing.Wallet != "" {
		fmt.Fprintf(ari.logWriter, "    Using existing wallet: %s\n", existing.Wallet)
		ari.config.Wallet = existing.Wallet
	}

	// Preserve MyFamily from existing installation (critical for multi-relay operators)
	if existing.MyFamily != "" {
		fmt.Fprintf(ari.logWriter, "    Preserving MyFamily configuration (%d relays)\n", len(strings.Split(existing.MyFamily, ",")))
		ari.config.MyFamily = existing.MyFamily
	}

	fmt.Fprintf(ari.logWriter, "  ✓ Backup created at %s\n", backupAnonDir)
	fmt.Fprintf(ari.logWriter, "  ✓ Migration complete - keys and fingerprint preserved\n")

	return nil
}

// ValidateNickname validates the relay nickname (1-19 alphanumeric chars)
func ValidateNickname(nickname string) error {
	if len(nickname) < 1 || len(nickname) > 19 {
		return fmt.Errorf("nickname must be 1-19 characters")
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9]+$`).MatchString(nickname) {
		return fmt.Errorf("nickname must be alphanumeric only")
	}
	return nil
}

// ValidateWallet validates an Ethereum wallet address
func ValidateWallet(wallet string) error {
	if !regexp.MustCompile(`^0x[a-fA-F0-9]{40}$`).MatchString(wallet) {
		return fmt.Errorf("invalid Ethereum wallet address (must be 0x followed by 40 hex characters)")
	}
	return nil
}
