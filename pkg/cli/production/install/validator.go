package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
	"github.com/DeBrosOfficial/network/pkg/config/validate"
	"github.com/DeBrosOfficial/network/pkg/environments/production/installers"
)

// Validator validates install command inputs
type Validator struct {
	flags       *Flags
	oramaDir    string
	isFirstNode bool
}

// NewValidator creates a new validator
func NewValidator(flags *Flags, oramaDir string) *Validator {
	return &Validator{
		flags:       flags,
		oramaDir:    oramaDir,
		isFirstNode: flags.JoinAddress == "",
	}
}

// ValidateFlags validates required flags
func (v *Validator) ValidateFlags() error {
	if v.flags.VpsIP == "" && !v.flags.DryRun {
		return fmt.Errorf("--vps-ip is required for installation\nExample: orama prod install --vps-ip 1.2.3.4")
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
	ports := utils.DefaultPorts()

	// Add ORPort check for relay mode (skip if migrating existing installation)
	if v.flags.AnyoneRelay && !v.flags.AnyoneMigrate {
		ports = append(ports, utils.PortSpec{
			Name: "Anyone ORPort",
			Port: v.flags.AnyoneORPort,
		})
	}

	if err := utils.EnsurePortsAvailable("install", ports); err != nil {
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
		// Extract hex only (strips headers if user passed full file content)
		hexKey := strings.ToUpper(validate.ExtractSwarmKeyHex(v.flags.SwarmKey))
		swarmKeyContent := fmt.Sprintf("/key/swarm/psk/1.0.0/\n/base16/\n%s\n", hexKey)
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

// ValidateAnyoneRelayFlags validates Anyone relay configuration and displays warnings
func (v *Validator) ValidateAnyoneRelayFlags() error {
	// Skip validation if not running as relay
	if !v.flags.AnyoneRelay {
		return nil
	}

	fmt.Printf("\n🔗 Anyone Relay Configuration\n")

	// Check for existing Anyone installation
	existing, err := installers.DetectExistingAnyoneInstallation()
	if err != nil {
		fmt.Printf("  ⚠️  Warning: failed to detect existing installation: %v\n", err)
	}

	if existing != nil {
		fmt.Printf("  ⚠️  Existing Anyone relay detected:\n")
		if existing.Fingerprint != "" {
			fmt.Printf("     Fingerprint: %s\n", existing.Fingerprint)
		}
		if existing.Nickname != "" {
			fmt.Printf("     Nickname: %s\n", existing.Nickname)
		}
		if existing.Wallet != "" {
			fmt.Printf("     Wallet: %s\n", existing.Wallet)
		}
		if existing.MyFamily != "" {
			familyCount := len(strings.Split(existing.MyFamily, ","))
			fmt.Printf("     MyFamily: %d relays\n", familyCount)
		}
		fmt.Printf("     Keys: %s\n", existing.KeysPath)
		fmt.Printf("     Config: %s\n", existing.ConfigPath)
		if existing.IsRunning {
			fmt.Printf("     Status: Running\n")
		}
		if !v.flags.AnyoneMigrate {
			fmt.Printf("\n  💡 Use --anyone-migrate to preserve existing keys and fingerprint\n")
		} else {
			fmt.Printf("\n  ✓ Will migrate existing installation (keys preserved)\n")
			// Auto-populate missing values from existing installation
			if v.flags.AnyoneNickname == "" && existing.Nickname != "" {
				v.flags.AnyoneNickname = existing.Nickname
				fmt.Printf("  ✓ Using existing nickname: %s\n", existing.Nickname)
			}
			if v.flags.AnyoneWallet == "" && existing.Wallet != "" {
				v.flags.AnyoneWallet = existing.Wallet
				fmt.Printf("  ✓ Using existing wallet: %s\n", existing.Wallet)
			}
		}
		fmt.Println()
	}

	// Validate required fields for relay mode
	if v.flags.AnyoneNickname == "" {
		return fmt.Errorf("--anyone-nickname is required for relay mode")
	}
	if err := installers.ValidateNickname(v.flags.AnyoneNickname); err != nil {
		return fmt.Errorf("invalid --anyone-nickname: %w", err)
	}

	if v.flags.AnyoneWallet == "" {
		return fmt.Errorf("--anyone-wallet is required for relay mode (for rewards)")
	}
	if err := installers.ValidateWallet(v.flags.AnyoneWallet); err != nil {
		return fmt.Errorf("invalid --anyone-wallet: %w", err)
	}

	if v.flags.AnyoneContact == "" {
		return fmt.Errorf("--anyone-contact is required for relay mode")
	}

	// Validate ORPort
	if v.flags.AnyoneORPort < 1 || v.flags.AnyoneORPort > 65535 {
		return fmt.Errorf("--anyone-orport must be between 1 and 65535")
	}

	// Display configuration summary
	fmt.Printf("  Nickname: %s\n", v.flags.AnyoneNickname)
	fmt.Printf("  Contact:  %s\n", v.flags.AnyoneContact)
	fmt.Printf("  Wallet:   %s\n", v.flags.AnyoneWallet)
	fmt.Printf("  ORPort:   %d\n", v.flags.AnyoneORPort)
	if v.flags.AnyoneExit {
		fmt.Printf("  Mode:     Exit Relay\n")
	} else {
		fmt.Printf("  Mode:     Non-exit Relay\n")
	}

	// Warning about token requirement
	fmt.Printf("\n  ⚠️  IMPORTANT: Relay operators must hold 100 $ANYONE tokens\n")
	fmt.Printf("     in wallet %s to receive rewards.\n", v.flags.AnyoneWallet)
	fmt.Printf("     Register at: https://dashboard.anyone.io\n")

	// Exit relay warning
	if v.flags.AnyoneExit {
		fmt.Printf("\n  ⚠️  EXIT RELAY WARNING:\n")
		fmt.Printf("     Running an exit relay may expose you to legal liability\n")
		fmt.Printf("     for traffic that exits through your node.\n")
		fmt.Printf("     Ensure you understand the implications before proceeding.\n")
	}

	fmt.Println()
	return nil
}
