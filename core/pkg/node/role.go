package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
	"github.com/DeBrosOfficial/network/pkg/node/boot"
	"gopkg.in/yaml.v3"
)

// systemdUnitDir is where the global installer writes the namespace unit.
const systemdUnitDir = "/etc/systemd/system"

// verifyNetnsLayout checks the co-located layout recorded in preferences
// (global_netns) against the files on this machine. A test replaces it.
var verifyNetnsLayout = func(recorded string) error {
	return globalnetns.Verify(recorded, globalnetns.DefaultPaths(systemdUnitDir), func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	})
}

// role is the graph this process converges. node.role and preferences.yaml
// role name the same fact; when both are set they must agree. A missing file
// or an empty value is cluster. Both (a co-located cluster and global node) is
// accepted only when preferences record the network namespace layout and its
// files are installed; the cluster graph is what this process runs then. A file that cannot be read or parsed is an
// error: guessing cluster would start WireGuard, RQLite, Olric and the gateway
// on a machine whose preferences were supposed to say global.
func (n *Node) role() (boot.Role, error) {
	cfgRole := ""
	if n != nil && n.config != nil {
		cfgRole = strings.TrimSpace(n.config.Node.Role)
	}
	prefs, err := n.preferences()
	if err != nil {
		return "", err
	}
	prefRole := strings.TrimSpace(prefs.Role)
	if cfgRole != "" && prefRole != "" && !strings.EqualFold(cfgRole, prefRole) {
		return "", fmt.Errorf("node role %q in config disagrees with preferences role %q", cfgRole, prefRole)
	}
	raw := cfgRole
	if raw == "" {
		raw = prefRole
	}
	return boot.ParseRole(raw, func() error { return verifyNetnsLayout(prefs.GlobalNetns) })
}

// nodePreferences is the part of preferences.yaml the role reads.
type nodePreferences struct {
	Role        string `yaml:"role"`
	GlobalNetns string `yaml:"global_netns"`
}

func (n *Node) preferences() (nodePreferences, error) {
	if n == nil || n.config == nil || strings.TrimSpace(n.config.Node.DataDir) == "" {
		return nodePreferences{}, nil
	}
	dataDir, err := config.ExpandPath(n.config.Node.DataDir)
	if err != nil {
		return nodePreferences{}, err
	}
	if dataDir == "" {
		return nodePreferences{}, nil
	}
	path := filepath.Join(filepath.Dir(dataDir), "preferences.yaml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nodePreferences{}, nil
	}
	if err != nil {
		return nodePreferences{}, fmt.Errorf("read %s: %w", path, err)
	}
	var prefs nodePreferences
	if err := yaml.Unmarshal(data, &prefs); err != nil {
		return nodePreferences{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return prefs, nil
}
