package templates

import (
	"bytes"
	"embed"
	"fmt"
	"regexp"
	"text/template"
)

//go:embed *.yaml *.service
var templatesFS embed.FS

// NodeConfigData holds parameters for node.yaml rendering (unified - no bootstrap/node distinction)
type NodeConfigData struct {
	NodeID                 string
	P2PPort                int
	DataDir                string
	RQLiteHTTPPort         int
	RQLiteRaftPort         int // External Raft port for advertisement (7001 for SNI)
	RQLiteRaftInternalPort int // Internal Raft port for local binding (7002 when SNI enabled)
	RQLiteJoinAddress      string   // Optional: join address for joining existing cluster
	BootstrapPeers         []string // List of peer multiaddrs to connect to
	ClusterAPIPort         int
	IPFSAPIPort            int    // Default: 4501
	HTTPAdvAddress         string // Advertised HTTP address (IP:port)
	RaftAdvAddress         string // Advertised Raft address (IP:port or domain:port for SNI)
	UnifiedGatewayPort     int    // Unified gateway port for all node services
	Domain                 string // Domain for this node (e.g., node-123.orama.network)
	BaseDomain             string // Base domain for deployment routing (e.g., dbrs.space)
	EnableHTTPS            bool   // Enable HTTPS/TLS with ACME
	TLSCacheDir            string // Directory for ACME certificate cache
	HTTPPort               int    // HTTP port for ACME challenges (usually 80)
	HTTPSPort              int    // HTTPS port (usually 443)

	// Node-to-node TLS encryption for RQLite Raft communication
	// Required when using SNI gateway for Raft traffic routing
	NodeCert     string // Path to X.509 certificate for node-to-node communication
	NodeKey      string // Path to X.509 private key for node-to-node communication
	NodeCACert   string // Path to CA certificate (optional)
	NodeNoVerify bool   // Skip certificate verification (for self-signed certs)
}

// GatewayConfigData holds parameters for gateway.yaml rendering
type GatewayConfigData struct {
	ListenPort     int
	BootstrapPeers []string
	OlricServers   []string
	ClusterAPIPort int
	IPFSAPIPort    int // Default: 4501
	EnableHTTPS    bool
	DomainName     string
	TLSCacheDir    string
	RQLiteDSN      string
}

// OlricConfigData holds parameters for olric.yaml rendering
type OlricConfigData struct {
	ServerBindAddr        string // HTTP API bind address (127.0.0.1 for security)
	HTTPPort              int
	MemberlistBindAddr    string // Memberlist bind address (0.0.0.0 for clustering)
	MemberlistPort        int
	MemberlistEnvironment string // "local", "lan", or "wan"
}

// SystemdIPFSData holds parameters for systemd IPFS service rendering
type SystemdIPFSData struct {
	HomeDir      string
	IPFSRepoPath string
	SecretsDir   string
	OramaDir    string
}

// SystemdIPFSClusterData holds parameters for systemd IPFS Cluster service rendering
type SystemdIPFSClusterData struct {
	HomeDir     string
	ClusterPath string
	OramaDir   string
}

// SystemdOlricData holds parameters for systemd Olric service rendering
type SystemdOlricData struct {
	HomeDir    string
	ConfigPath string
	OramaDir  string
}

// SystemdNodeData holds parameters for systemd Node service rendering
type SystemdNodeData struct {
	HomeDir    string
	ConfigFile string
	OramaDir  string
}

// SystemdGatewayData holds parameters for systemd Gateway service rendering
type SystemdGatewayData struct {
	HomeDir   string
	OramaDir string
}

// RenderNodeConfig renders the node config template with the given data
func RenderNodeConfig(data NodeConfigData) (string, error) {
	return renderTemplate("node.yaml", data)
}

// RenderGatewayConfig renders the gateway config template with the given data
func RenderGatewayConfig(data GatewayConfigData) (string, error) {
	return renderTemplate("gateway.yaml", data)
}

// RenderOlricConfig renders the olric config template with the given data
func RenderOlricConfig(data OlricConfigData) (string, error) {
	return renderTemplate("olric.yaml", data)
}

// RenderIPFSService renders the IPFS systemd service template
func RenderIPFSService(data SystemdIPFSData) (string, error) {
	return renderTemplate("systemd_ipfs.service", data)
}

// RenderIPFSClusterService renders the IPFS Cluster systemd service template
func RenderIPFSClusterService(data SystemdIPFSClusterData) (string, error) {
	return renderTemplate("systemd_ipfs_cluster.service", data)
}

// RenderOlricService renders the Olric systemd service template
func RenderOlricService(data SystemdOlricData) (string, error) {
	return renderTemplate("systemd_olric.service", data)
}

// RenderNodeService renders the DeBros Node systemd service template
func RenderNodeService(data SystemdNodeData) (string, error) {
	return renderTemplate("systemd_node.service", data)
}

// RenderGatewayService renders the DeBros Gateway systemd service template
func RenderGatewayService(data SystemdGatewayData) (string, error) {
	return renderTemplate("systemd_gateway.service", data)
}

// normalizeTemplate normalizes template placeholders from spaced format { { .Var } } to {{.Var}}
func normalizeTemplate(content string) string {
	// Match patterns like { { .Variable } } or { {.Variable} } or { { .Variable} } etc.
	// and convert them to {{.Variable}}
	// Pattern matches: { { .Something } } -> {{.Something}}
	// This regex specifically matches Go template variables (starting with .)
	re := regexp.MustCompile(`\{\s*\{\s*(\.\S+)\s*\}\s*\}`)
	normalized := re.ReplaceAllString(content, "{{$1}}")
	return normalized
}

// renderTemplate is a helper that renders any template from the embedded FS
func renderTemplate(name string, data interface{}) (string, error) {
	// Read template content
	tmplBytes, err := templatesFS.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("failed to read template %s: %w", name, err)
	}

	// Normalize template content to handle both { { .Var } } and {{.Var}} formats
	normalizedContent := normalizeTemplate(string(tmplBytes))

	// Parse normalized template
	tmpl, err := template.New(name).Parse(normalizedContent)
	if err != nil {
		return "", fmt.Errorf("failed to parse template %s: %w", name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render template %s: %w", name, err)
	}

	return buf.String(), nil
}
