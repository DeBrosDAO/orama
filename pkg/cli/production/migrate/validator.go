package migrate

import (
	"fmt"
	"os"
	"path/filepath"
)

// Validator checks if migration is needed
type Validator struct {
	oramaDir string
}

// NewValidator creates a new Validator
func NewValidator(oramaDir string) *Validator {
	return &Validator{oramaDir: oramaDir}
}

// CheckNeedsMigration checks if migration is needed
func (v *Validator) CheckNeedsMigration() bool {
	oldDataDirs := []string{
		filepath.Join(v.oramaDir, "data", "node-1"),
		filepath.Join(v.oramaDir, "data", "node"),
	}

	oldServices := []string{
		"debros-ipfs",
		"debros-ipfs-cluster",
		"debros-node",
	}

	oldConfigs := []string{
		filepath.Join(v.oramaDir, "configs", "bootstrap.yaml"),
	}

	var needsMigration bool

	fmt.Printf("Checking data directories:\n")
	for _, dir := range oldDataDirs {
		if _, err := os.Stat(dir); err == nil {
			fmt.Printf("  ⚠️  Found old directory: %s\n", dir)
			needsMigration = true
		}
	}

	fmt.Printf("\nChecking services:\n")
	for _, svc := range oldServices {
		unitPath := filepath.Join("/etc/systemd/system", svc+".service")
		if _, err := os.Stat(unitPath); err == nil {
			fmt.Printf("  ⚠️  Found old service: %s\n", svc)
			needsMigration = true
		}
	}

	fmt.Printf("\nChecking configs:\n")
	for _, cfg := range oldConfigs {
		if _, err := os.Stat(cfg); err == nil {
			fmt.Printf("  ⚠️  Found old config: %s\n", cfg)
			needsMigration = true
		}
	}

	return needsMigration
}
