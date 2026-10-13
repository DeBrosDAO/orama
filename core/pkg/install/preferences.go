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
	Role       string `yaml:"role,omitempty"` // cluster (empty), global, or both (co-located; needs GlobalNetns)
	// GlobalNetns names the network namespace the global services run in on a
	// co-located machine. `orama global install --colocated` writes it with
	// role both; the node refuses role both without it.
	GlobalNetns string `yaml:"global_netns,omitempty"`
}

const preferencesFile = "preferences.yaml"

// PreferencesForInstall is the preferences `orama node install` writes: the
// install's own choices (branch, nameserver) laid over what the machine already
// records. Role and GlobalNetns belong to the global layer, which may have been
// installed first (a global-only machine becoming a cluster node) or since the
// last install (a co-located machine re-installed); writing a fresh struct made
// the node forget role both, and a node that has lost it refuses to start the
// global graph or runs it outside its namespace.
func PreferencesForInstall(oramaDir string, nameserver bool) *NodePreferences {
	prefs := LoadPreferences(oramaDir)
	prefs.Branch = "main"
	prefs.Nameserver = nameserver
	return prefs
}

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
