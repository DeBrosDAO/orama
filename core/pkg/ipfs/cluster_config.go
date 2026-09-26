package ipfs

import (
	"encoding/json"
	"fmt"
	"os"
)

// ClusterServiceConfig is the part of service.json the node writes. Every
// other field — the listeners, the IPFS connector, the CRDT tuning — is
// install's, and survives a node write untouched in Raw.
type ClusterServiceConfig struct {
	Cluster struct {
		Peername      string   `json:"peername"`
		Secret        string   `json:"secret"`
		PeerAddresses []string `json:"peer_addresses"`
	} `json:"cluster"`

	Consensus struct {
		CRDT struct {
			ClusterName  string   `json:"cluster_name"`
			TrustedPeers []string `json:"trusted_peers"`
		} `json:"crdt"`
	} `json:"consensus"`

	Raw map[string]interface{} `json:"-"`
}

// loadConfig reads service.json. Install creates it; a node without one has
// not been installed or upgraded with this release, and inventing one here
// would be a second writer of the whole file.
func (cm *ClusterConfigManager) loadConfig(path string) (*ClusterServiceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the IPFS Cluster config %s (install and upgrade write it; "+
			"run `orama node upgrade` on this node): %w", path, err)
	}

	var cfg ClusterServiceConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse service.json: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse raw service.json: %w", err)
	}
	cfg.Raw = raw

	return &cfg, nil
}

// saveConfig writes the node's fields into service.json, leaving the rest as
// install wrote it.
func (cm *ClusterConfigManager) saveConfig(path string, cfg *ClusterServiceConfig) error {
	cm.updateNestedMap(cfg.Raw, "cluster", "peername", cfg.Cluster.Peername)
	cm.updateNestedMap(cfg.Raw, "cluster", "secret", cfg.Cluster.Secret)
	cm.updateNestedMap(cfg.Raw, "cluster", "peer_addresses", cfg.Cluster.PeerAddresses)

	consensus := cm.ensureRequiredSection(cfg.Raw, "consensus")
	crdt := cm.ensureRequiredSection(consensus, "crdt")
	crdt["cluster_name"] = cfg.Consensus.CRDT.ClusterName
	crdt["trusted_peers"] = cfg.Consensus.CRDT.TrustedPeers

	data, err := json.MarshalIndent(cfg.Raw, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal service.json: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write the IPFS Cluster config %s: %w", path, err)
	}
	return nil
}

func (cm *ClusterConfigManager) updateNestedMap(m map[string]interface{}, section, key string, val interface{}) {
	if _, ok := m[section]; !ok {
		m[section] = make(map[string]interface{})
	}
	s := m[section].(map[string]interface{})
	s[key] = val
}

func (cm *ClusterConfigManager) ensureRequiredSection(m map[string]interface{}, key string) map[string]interface{} {
	if _, ok := m[key]; !ok {
		m[key] = make(map[string]interface{})
	}
	return m[key].(map[string]interface{})
}
