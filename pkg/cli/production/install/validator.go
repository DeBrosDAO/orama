package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// Validator validates install command inputs
type Validator struct {
	flags     *Flags
	oramaDir  string
	isFirstNode bool
}

// NewValidator creates a new validator
func NewValidator(flags *Flags, oramaDir string) *Validator {
	return &Validator{
		flags:     flags,
		oramaDir:  oramaDir,
		isFirstNode: flags.JoinAddress == "",
	}
}

// ValidateFlags validates required flags
func (v *Validator) ValidateFlags() error {
	if v.flags.VpsIP == "" && !v.flags.DryRun {
		return fmt.Errorf("--vps-ip is required for installation\nExample: dbn prod install --vps-ip 1.2.3.4")
	}
	return nil
}

// ValidateRootPrivileges checks if running as root
func (v *Validator) ValidateRootPrivileges() error {
	if os.Geteuid() != 0 && !v.flags.DryRun {
		return fmt.Errorf("production installation must be run as root (use sudo)")
	}
	return nil
}

// ValidatePorts validates port availability
func (v *Validator) ValidatePorts() error {
	if err := utils.EnsurePortsAvailable("install", utils.DefaultPorts()); err != nil {
		return err
	}
	return nil
}

// ValidateDNS validates DNS record if domain is provided
func (v *Validator) ValidateDNS() {
	if v.flags.Domain != "" {
		fmt.Printf("\n🌐 Pre-flight DNS validation...\n")
		utils.ValidateDNSRecord(v.flags.Domain, v.flags.VpsIP)
	}
}

// ValidateGeneratedConfig validates generated configuration files
func (v *Validator) ValidateGeneratedConfig() error {
	fmt.Printf("  Validating generated configuration...\n")
	if err := utils.ValidateGeneratedConfig(v.oramaDir); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}
	fmt.Printf("  ✓ Configuration validated\n")
	return nil
}

// SaveSecrets saves cluster secret and swarm key to secrets directory
func (v *Validator) SaveSecrets() error {
	// If cluster secret was provided, save it to secrets directory before setup
	if v.flags.ClusterSecret != "" {
		secretsDir := filepath.Join(v.oramaDir, "secrets")
		if err := os.MkdirAll(secretsDir, 0755); err != nil {
			return fmt.Errorf("failed to create secrets directory: %w", err)
		}
		secretPath := filepath.Join(secretsDir, "cluster-secret")
		if err := os.WriteFile(secretPath, []byte(v.flags.ClusterSecret), 0600); err != nil {
			return fmt.Errorf("failed to save cluster secret: %w", err)
		}
		fmt.Printf("  ✓ Cluster secret saved\n")
	}

	// If swarm key was provided, save it to secrets directory in full format
	if v.flags.SwarmKey != "" {
		secretsDir := filepath.Join(v.oramaDir, "secrets")
		if err := os.MkdirAll(secretsDir, 0755); err != nil {
			return fmt.Errorf("failed to create secrets directory: %w", err)
		}
		// Convert 64-hex key to full swarm.key format
		swarmKeyContent := fmt.Sprintf("/key/swarm/psk/1.0.0/\n/base16/\n%s\n", strings.ToUpper(v.flags.SwarmKey))
		swarmKeyPath := filepath.Join(secretsDir, "swarm.key")
		if err := os.WriteFile(swarmKeyPath, []byte(swarmKeyContent), 0600); err != nil {
			return fmt.Errorf("failed to save swarm key: %w", err)
		}
		fmt.Printf("  ✓ Swarm key saved\n")
	}

	return nil
}

// IsFirstNode returns true if this is the first node in the cluster
func (v *Validator) IsFirstNode() bool {
	return v.isFirstNode
}
