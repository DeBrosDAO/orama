// Package gatewayspec holds the types the factory and the gateway binary share:
// spawn config, on-disk YAML, and the record of a running instance.
// It imports nothing from pkg/gateway or pkg/namespace.
package gatewayspec

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
	"go.uber.org/zap"
)

// InstanceNodeStatus is the lifecycle state of one spawned gateway process.
type InstanceNodeStatus string

const (
	InstanceStatusPending  InstanceNodeStatus = "pending"
	InstanceStatusStarting InstanceNodeStatus = "starting"
	InstanceStatusRunning  InstanceNodeStatus = "running"
	InstanceStatusStopped  InstanceNodeStatus = "stopped"
	InstanceStatusFailed   InstanceNodeStatus = "failed"
)

// InstanceError is an error during instance operations.
type InstanceError struct {
	Message string
	Cause   error
}

func (e *InstanceError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *InstanceError) Unwrap() error {
	return e.Cause
}

// GatewayInstance is the record of a running gateway for a namespace.
type GatewayInstance struct {
	Namespace    string
	NodeID       string
	HTTPPort     int
	BaseDomain   string
	RQLiteDSN    string
	OlricServers []string
	ConfigPath   string
	PID          int
	StartedAt    time.Time
	// Cmd and Logger are used only by the in-process spawner (tests).
	Cmd             *exec.Cmd
	Logger          *zap.Logger
	Mu              sync.RWMutex
	Status          InstanceNodeStatus
	LastHealthCheck time.Time
}

// InstanceConfig is the input to spawning a gateway instance.
type InstanceConfig struct {
	Namespace       string
	NodeID          string
	HTTPPort        int
	BaseDomain      string
	RQLiteDSN       string
	GlobalRQLiteDSN string
	RQLiteUsername  string
	RQLitePassword  string
	OlricServers    []string
	OlricTimeout    time.Duration
	NodePeerID      string
	// StateDir is the gateway's private state directory
	// (<oramaDir>/data/namespaces/<ns>/gateway). The spawner sets it from its
	// namespace base when it renders the YAML; callers leave it empty.
	StateDir              string
	IPFSClusterAPIURL     string
	IPFSAPIURL            string
	IPFSTimeout           time.Duration
	IPFSReplicationFactor int
	WebRTCEnabled         bool
	SFUPort               int
	TURNDomain            string
	TURNSecret            string
	TURNStealthDomain     string
	SecretsEncryptionKey  string
	NtfyBaseURL           string
}

// GatewayYAMLWebRTC is the webrtc section of the gateway YAML config.
// Must match yamlWebRTCCfg in cmd/gateway/config.go.
type GatewayYAMLWebRTC struct {
	Enabled           bool   `yaml:"enabled"`
	SFUPort           int    `yaml:"sfu_port,omitempty"`
	TURNDomain        string `yaml:"turn_domain,omitempty"`
	TURNSecret        string `yaml:"turn_secret,omitempty"`
	TURNStealthDomain string `yaml:"turn_stealth_domain,omitempty"`
}

// GatewayYAMLConfig is the gateway YAML configuration structure.
// Must match yamlCfg in cmd/gateway/config.go because the gateway uses
// strict YAML decoding that rejects unknown fields.
type GatewayYAMLConfig struct {
	ListenAddr            string            `yaml:"listen_addr"`
	ClientNamespace       string            `yaml:"client_namespace"`
	RQLiteDSN             string            `yaml:"rqlite_dsn"`
	GlobalRQLiteDSN       string            `yaml:"global_rqlite_dsn,omitempty"`
	RQLiteUsername        string            `yaml:"rqlite_username,omitempty"`
	RQLitePassword        string            `yaml:"rqlite_password,omitempty"`
	BootstrapPeers        []string          `yaml:"bootstrap_peers,omitempty"`
	EnableHTTPS           bool              `yaml:"enable_https,omitempty"`
	DomainName            string            `yaml:"domain_name,omitempty"`
	TLSCacheDir           string            `yaml:"tls_cache_dir,omitempty"`
	OlricServers          []string          `yaml:"olric_servers"`
	OlricTimeout          string            `yaml:"olric_timeout,omitempty"`
	IPFSClusterAPIURL     string            `yaml:"ipfs_cluster_api_url,omitempty"`
	IPFSAPIURL            string            `yaml:"ipfs_api_url,omitempty"`
	IPFSTimeout           string            `yaml:"ipfs_timeout,omitempty"`
	IPFSReplicationFactor int               `yaml:"ipfs_replication_factor,omitempty"`
	WebRTC                GatewayYAMLWebRTC `yaml:"webrtc,omitempty"`
	SecretsEncryptionKey  string            `yaml:"secrets_encryption_key,omitempty"`
	NtfyBaseURL           string            `yaml:"ntfy_base_url,omitempty"`
	ClusterSecretPath     string            `yaml:"cluster_secret_path,omitempty"`
	APIKeyHMACSecret      string            `yaml:"api_key_hmac_secret,omitempty"`
	StateDir              string            `yaml:"state_dir"`
}

// IsHealthy checks if the Gateway instance answers /v1/health.
func (gi *GatewayInstance) IsHealthy(ctx context.Context) (bool, error) {
	url := fmt.Sprintf("http://localhost:%d/v1/health", gi.HTTPPort)
	client := tlsutil.NewHTTPClient(5 * time.Second)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}

// DSN returns the local connection address for this Gateway instance.
func (gi *GatewayInstance) DSN() string {
	return fmt.Sprintf("http://localhost:%d", gi.HTTPPort)
}

// ExternalURL returns the external URL for accessing this namespace's gateway.
func (gi *GatewayInstance) ExternalURL() string {
	return fmt.Sprintf("https://ns-%s.%s", gi.Namespace, gi.BaseDomain)
}
