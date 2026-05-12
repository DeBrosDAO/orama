package gateway

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestGatewayYAMLConfig_clusterSecretPathRoundTrip is the regression test for
// bug #215 reopen. The first fix derived JWT keys from cfg.ClusterSecret, but
// namespace gateways spawned via systemd had cfg.ClusterSecret == "" because
// the YAML schema lacked any field to carry it. This test guards the YAML tag
// so every namespace gateway YAML written by SystemdSpawner.SpawnGateway
// carries the path the standalone binary needs.
func TestGatewayYAMLConfig_clusterSecretPathRoundTrip(t *testing.T) {
	cfg := GatewayYAMLConfig{
		ListenAddr:        ":6001",
		ClientNamespace:   "anchat-test",
		RQLiteDSN:         "http://localhost:10000",
		OlricServers:      []string{"localhost:3320"},
		ClusterSecretPath: "/opt/orama/.orama/secrets/cluster-secret",
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "cluster_secret_path: /opt/orama/.orama/secrets/cluster-secret") {
		t.Fatalf("YAML output missing expected cluster_secret_path line:\n%s", out)
	}

	// Round-trip into a struct shaped like cmd/gateway/config.go's yamlCfg
	// (internal type, duplicated here intentionally so this test catches
	// drift between the two declarations).
	type webrtc struct {
		Enabled    bool   `yaml:"enabled"`
		SFUPort    int    `yaml:"sfu_port"`
		TURNDomain string `yaml:"turn_domain"`
		TURNSecret string `yaml:"turn_secret"`
	}
	type yamlCfgMirror struct {
		ListenAddr            string   `yaml:"listen_addr"`
		ClientNamespace       string   `yaml:"client_namespace"`
		RQLiteDSN             string   `yaml:"rqlite_dsn"`
		GlobalRQLiteDSN       string   `yaml:"global_rqlite_dsn"`
		Peers                 []string `yaml:"bootstrap_peers"`
		EnableHTTPS           bool     `yaml:"enable_https"`
		DomainName            string   `yaml:"domain_name"`
		TLSCacheDir           string   `yaml:"tls_cache_dir"`
		OlricServers          []string `yaml:"olric_servers"`
		OlricTimeout          string   `yaml:"olric_timeout"`
		IPFSClusterAPIURL     string   `yaml:"ipfs_cluster_api_url"`
		IPFSAPIURL            string   `yaml:"ipfs_api_url"`
		IPFSTimeout           string   `yaml:"ipfs_timeout"`
		IPFSReplicationFactor int      `yaml:"ipfs_replication_factor"`
		WebRTC                webrtc   `yaml:"webrtc"`
		ClusterSecretPath     string   `yaml:"cluster_secret_path"`
	}
	var parsed yamlCfgMirror
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.ClusterSecretPath != cfg.ClusterSecretPath {
		t.Errorf("round-trip mismatch: got %q, want %q", parsed.ClusterSecretPath, cfg.ClusterSecretPath)
	}
}

// TestGatewayYAMLConfig_omitWhenEmpty: when the host has no cluster secret,
// the field is omitted from the YAML so legacy single-node test rigs don't
// see a stray "cluster_secret_path: " line that operators might mistake for
// a configuration directive.
func TestGatewayYAMLConfig_omitWhenEmpty(t *testing.T) {
	cfg := GatewayYAMLConfig{
		ListenAddr:      ":6001",
		ClientNamespace: "ns",
		RQLiteDSN:       "http://localhost:10000",
		OlricServers:    []string{"localhost:3320"},
		// ClusterSecretPath intentionally empty.
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "cluster_secret_path") {
		t.Errorf("empty ClusterSecretPath should be omitted from YAML; got:\n%s", out)
	}
}
