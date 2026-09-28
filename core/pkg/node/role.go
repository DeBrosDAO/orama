package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/node/boot"
	"gopkg.in/yaml.v3"
)

// role is the graph this process converges. node.role and preferences.yaml
// role name the same fact; when both are set they must agree. A missing file
// or an empty value is cluster. A file that cannot be read or parsed is an
// error: guessing cluster would start WireGuard, RQLite, Olric and the gateway
// on a machine whose preferences were supposed to say global.
func (n *Node) role() (boot.Role, error) {
	cfgRole := ""
	if n != nil && n.config != nil {
		cfgRole = strings.TrimSpace(n.config.Node.Role)
	}
	prefRole, err := n.preferencesRole()
	if err != nil {
		return "", err
	}
	prefRole = strings.TrimSpace(prefRole)
	if cfgRole != "" && prefRole != "" && !strings.EqualFold(cfgRole, prefRole) {
		return "", fmt.Errorf("node role %q in config disagrees with preferences role %q", cfgRole, prefRole)
	}
	raw := cfgRole
	if raw == "" {
		raw = prefRole
	}
	return boot.ParseRole(raw)
}

func (n *Node) preferencesRole() (string, error) {
	if n == nil || n.config == nil || strings.TrimSpace(n.config.Node.DataDir) == "" {
		return "", nil
	}
	dataDir, err := config.ExpandPath(n.config.Node.DataDir)
	if err != nil {
		return "", err
	}
	if dataDir == "" {
		return "", nil
	}
	path := filepath.Join(filepath.Dir(dataDir), "preferences.yaml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var prefs struct {
		Role string `yaml:"role"`
	}
	if err := yaml.Unmarshal(data, &prefs); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	return prefs.Role, nil
}
