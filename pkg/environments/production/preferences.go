package production

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// NodePreferences contains persistent node configuration that survives upgrades
type NodePreferences struct {
	Branch     string `yaml:"branch"`
	Nameserver bool   `yaml:"nameserver"`
}

const (
	preferencesFile = "preferences.yaml"
	legacyBranchFile = ".branch"
)

// SavePreferences saves node preferences to disk
func SavePreferences(oramaDir string, prefs *NodePreferences) error {
	// Ensure directory exists
	if err := os.MkdirAll(oramaDir, 0755); err != nil {
		return err
	}

	// Save to YAML file
	path := filepath.Join(oramaDir, preferencesFile)
	data, err := yaml.Marshal(prefs)
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}

	// Also save branch to legacy .branch file for backward compatibility
	legacyPath := filepath.Join(oramaDir, legacyBranchFile)
	os.WriteFile(legacyPath, []byte(prefs.Branch), 0644)

	return nil
}

// LoadPreferences loads node preferences from disk
// Falls back to reading legacy .branch file if preferences.yaml doesn't exist
func LoadPreferences(oramaDir string) *NodePreferences {
	prefs := &NodePreferences{
		Branch:     "main",
		Nameserver: false,
	}

	// Try to load from preferences.yaml first
	path := filepath.Join(oramaDir, preferencesFile)
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, prefs); err == nil {
			return prefs
		}
	}

	// Fall back to legacy .branch file
	legacyPath := filepath.Join(oramaDir, legacyBranchFile)
	if data, err := os.ReadFile(legacyPath); err == nil {
		branch := strings.TrimSpace(string(data))
		if branch != "" {
			prefs.Branch = branch
		}
	}

	return prefs
}

// SaveNameserverPreference updates just the nameserver preference
func SaveNameserverPreference(oramaDir string, isNameserver bool) error {
	prefs := LoadPreferences(oramaDir)
	prefs.Nameserver = isNameserver
	return SavePreferences(oramaDir, prefs)
}

// ReadNameserverPreference reads just the nameserver preference
func ReadNameserverPreference(oramaDir string) bool {
	return LoadPreferences(oramaDir).Nameserver
}
