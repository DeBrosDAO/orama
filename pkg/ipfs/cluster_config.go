package ipfs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ClusterServiceConfig represents the service.json configuration
type ClusterServiceConfig struct {
	Cluster struct {
		Peername           string   `json:"peername"`
		Secret             string   `json:"secret"`
		ListenMultiaddress []string `json:"listen_multiaddress"`
		PeerAddresses      []string `json:"peer_addresses"`
		LeaveOnShutdown    bool     `json:"leave_on_shutdown"`
	} `json:"cluster"`

	Consensus struct {
		CRDT struct {
			ClusterName  string   `json:"cluster_name"`
			TrustedPeers []string `json:"trusted_peers"`
			Batching     struct {
				MaxBatchSize int    `json:"max_batch_size"`
				MaxBatchAge  string `json:"max_batch_age"`
			} `json:"batching"`
			RepairInterval string `json:"repair_interval"`
		} `json:"crdt"`
	} `json:"consensus"`

	API struct {
		RestAPI struct {
			HTTPListenMultiaddress string `json:"http_listen_multiaddress"`
		} `json:"restapi"`
		IPFSProxy struct {
			ListenMultiaddress string `json:"listen_multiaddress"`
			NodeMultiaddress   string `json:"node_multiaddress"`
		} `json:"ipfsproxy"`
		PinSvcAPI struct {
			HTTPListenMultiaddress string `json:"http_listen_multiaddress"`
		} `json:"pinsvcapi"`
	} `json:"api"`

	IPFSConnector struct {
		IPFSHTTP struct {
			NodeMultiaddress string `json:"node_multiaddress"`
		} `json:"ipfshttp"`
	} `json:"ipfs_connector"`

	Raw map[string]interface{} `json:"-"`
}

func (cm *ClusterConfigManager) loadOrCreateConfig(path string) (*ClusterServiceConfig, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cm.createTemplateConfig(), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read service.json: %w", err)
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

func (cm *ClusterConfigManager) saveConfig(path string, cfg *ClusterServiceConfig) error {
	cm.updateNestedMap(cfg.Raw, "cluster", "peername", cfg.Cluster.Peername)
	cm.updateNestedMap(cfg.Raw, "cluster", "secret", cfg.Cluster.Secret)
	cm.updateNestedMap(cfg.Raw, "cluster", "listen_multiaddress", cfg.Cluster.ListenMultiaddress)
	cm.updateNestedMap(cfg.Raw, "cluster", "peer_addresses", cfg.Cluster.PeerAddresses)
	cm.updateNestedMap(cfg.Raw, "cluster", "leave_on_shutdown", cfg.Cluster.LeaveOnShutdown)

	consensus := cm.ensureRequiredSection(cfg.Raw, "consensus")
	crdt := cm.ensureRequiredSection(consensus, "crdt")
	crdt["cluster_name"] = cfg.Consensus.CRDT.ClusterName
	crdt["trusted_peers"] = cfg.Consensus.CRDT.TrustedPeers
	crdt["repair_interval"] = cfg.Consensus.CRDT.RepairInterval

	batching := cm.ensureRequiredSection(crdt, "batching")
	batching["max_batch_size"] = cfg.Consensus.CRDT.Batching.MaxBatchSize
	batching["max_batch_age"] = cfg.Consensus.CRDT.Batching.MaxBatchAge

	api := cm.ensureRequiredSection(cfg.Raw, "api")
	restapi := cm.ensureRequiredSection(api, "restapi")
	restapi["http_listen_multiaddress"] = cfg.API.RestAPI.HTTPListenMultiaddress

	ipfsproxy := cm.ensureRequiredSection(api, "ipfsproxy")
	ipfsproxy["listen_multiaddress"] = cfg.API.IPFSProxy.ListenMultiaddress
	ipfsproxy["node_multiaddress"] = cfg.API.IPFSProxy.NodeMultiaddress

	pinsvcapi := cm.ensureRequiredSection(api, "pinsvcapi")
	pinsvcapi["http_listen_multiaddress"] = cfg.API.PinSvcAPI.HTTPListenMultiaddress

	ipfsConn := cm.ensureRequiredSection(cfg.Raw, "ipfs_connector")
	ipfsHttp := cm.ensureRequiredSection(ipfsConn, "ipfshttp")
	ipfsHttp["node_multiaddress"] = cfg.IPFSConnector.IPFSHTTP.NodeMultiaddress

	data, err := json.MarshalIndent(cfg.Raw, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal service.json: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	return os.WriteFile(path, data, 0644)
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

