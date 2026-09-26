package install

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/utils"
	"github.com/DeBrosOfficial/network/pkg/config/validate"
	oramainstall "github.com/DeBrosOfficial/network/pkg/install"
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
		return fmt.Errorf("--vps-ip is required for installation\nExample: orama node install --vps-ip 1.2.3.4")
	}
	// It becomes node.public_ip, which invites and upgrades require to be a
	// public IPv4 address; recording anything else here only defers the error.
	if v.flags.VpsIP != "" {
		if err := oramainstall.ValidatePublicIP(v.flags.VpsIP); err != nil {
			return fmt.Errorf("--vps-ip: %w", err)
		}
	}
	return nil
}

// ValidateDNS validates DNS record if domain is provided.
//
// A nameserver node gets its certificates over DNS-01 from the cluster's own
// CoreDNS, so where the domain's A record points says nothing about whether
// they will issue: on a join it points at the nodes already serving, and on
// genesis there is no record until this node creates it. What they need is the
// parent zone's delegation.
func (v *Validator) ValidateDNS() {
	if v.flags.Domain == "" {
		return
	}
	fmt.Printf("\n🌐 Pre-flight DNS validation...\n")
	if v.flags.Nameserver {
		fmt.Printf("  ℹ️  Certificates for %s are issued over DNS-01 by this cluster's nameservers;\n", v.flags.Domain)
		fmt.Printf("     the parent zone must delegate %s to them (docs/NAMESERVER_SETUP.md)\n", v.flags.Domain)
		return
	}
	utils.ValidateDNSRecord(v.flags.Domain, v.flags.VpsIP)
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
	// secrets/ may already be the orama user's (a re-install): write it
	// without following symlinks.
	root := oramainstall.OramaRoot(v.oramaDir)
	// If cluster secret was provided, save it to secrets directory before setup
	if v.flags.ClusterSecret != "" {
		secretsDir := filepath.Join(v.oramaDir, "secrets")
		if err := root.MkdirAll(secretsDir, 0700); err != nil {
			return fmt.Errorf("failed to create secrets directory: %w", err)
		}
		secretPath := filepath.Join(secretsDir, "cluster-secret")
		if err := root.WriteFile(secretPath, []byte(v.flags.ClusterSecret), 0600); err != nil {
			return fmt.Errorf("failed to save cluster secret: %w", err)
		}
		fmt.Printf("  ✓ Cluster secret saved\n")
	}

	// If swarm key was provided, save it to secrets directory in full format
	if v.flags.SwarmKey != "" {
		secretsDir := filepath.Join(v.oramaDir, "secrets")
		if err := root.MkdirAll(secretsDir, 0700); err != nil {
			return fmt.Errorf("failed to create secrets directory: %w", err)
		}
		// Extract hex only (strips headers if user passed full file content)
		hexKey := strings.ToUpper(validate.ExtractSwarmKeyHex(v.flags.SwarmKey))
		swarmKeyContent := fmt.Sprintf("/key/swarm/psk/1.0.0/\n/base16/\n%s\n", hexKey)
		swarmKeyPath := filepath.Join(secretsDir, "swarm.key")
		if err := root.WriteFile(swarmKeyPath, []byte(swarmKeyContent), 0600); err != nil {
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
