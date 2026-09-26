package install

import (
	"fmt"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"gopkg.in/yaml.v3"
)

// NodePreferences contains persistent node configuration that survives upgrades
type NodePreferences struct {
	Branch     string `yaml:"branch"`
	Nameserver bool   `yaml:"nameserver"`
}

const preferencesFile = "preferences.yaml"

// SavePreferences saves node preferences to disk
func SavePreferences(oramaDir string, prefs *NodePreferences) error {
	root := OramaRoot(oramaDir)
	if err := root.MkdirAll(oramaDir, 0755); err != nil {
		return fmt.Errorf("create %s for the node preferences: %w", oramaDir, err)
	}

	// Save to YAML file
	path := filepath.Join(oramaDir, preferencesFile)
	data, err := yaml.Marshal(prefs)
	if err != nil {
		return err
	}

	if err := root.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("save the node preferences: %w", err)
	}

	return nil
}

// LoadPreferences loads node preferences from disk
// Falls back to reading legacy .branch file if preferences.yaml doesn't exist
func LoadPreferences(oramaDir string) *NodePreferences {
	prefs := &NodePreferences{
		Branch:     "main",
		Nameserver: false,
	}

	// Try to load from preferences.yaml
	path := filepath.Join(oramaDir, preferencesFile)
	if data, err := OramaRoot(oramaDir).ReadFile(path, rootfs.SmallFileLimit); err == nil {
		if err := yaml.Unmarshal(data, prefs); err == nil {
			return prefs
		}
	}

	return prefs
}
