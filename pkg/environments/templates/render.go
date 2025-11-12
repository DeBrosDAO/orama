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

// BootstrapConfigData holds parameters for bootstrap.yaml rendering
type BootstrapConfigData struct {
	NodeID            string
	P2PPort           int
	DataDir           string
	RQLiteHTTPPort    int
	RQLiteRaftPort    int
	ClusterAPIPort    int
	IPFSAPIPort       int      // Default: 4501
	BootstrapPeers    []string // List of bootstrap peer multiaddrs
	RQLiteJoinAddress string   // Optional: join address for secondary bootstraps
	HTTPAdvAddress    string   // Advertised HTTP address (IP:port)
	RaftAdvAddress    string   // Advertised Raft address (IP:port)
}

// NodeConfigData holds parameters for node.yaml rendering
type NodeConfigData struct {
	NodeID            string
	P2PPort           int
	DataDir           string
	RQLiteHTTPPort    int
	RQLiteRaftPort    int
	RQLiteJoinAddress string
	BootstrapPeers    []string
	ClusterAPIPort    int
	IPFSAPIPort       int    // Default: 4501+
	HTTPAdvAddress    string // Advertised HTTP address (IP:port)
	RaftAdvAddress    string // Advertised Raft address (IP:port)
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
	BindAddr       string
	HTTPPort       int
	MemberlistPort int
}

// SystemdIPFSData holds parameters for systemd IPFS service rendering
type SystemdIPFSData struct {
	NodeType     string
	HomeDir      string
	IPFSRepoPath string
	SecretsDir   string
	DebrosDir    string
}

// SystemdIPFSClusterData holds parameters for systemd IPFS Cluster service rendering
type SystemdIPFSClusterData struct {
	NodeType    string
	HomeDir     string
	ClusterPath string
	DebrosDir   string
}

// SystemdRQLiteData holds parameters for systemd RQLite service rendering
type SystemdRQLiteData struct {
	NodeType  string
	HomeDir   string
	HTTPPort  int
	RaftPort  int
	DataDir   string
	JoinAddr  string
	DebrosDir string
}

// SystemdOlricData holds parameters for systemd Olric service rendering
type SystemdOlricData struct {
	HomeDir    string
	ConfigPath string
	DebrosDir  string
}

// SystemdNodeData holds parameters for systemd Node service rendering
type SystemdNodeData struct {
	NodeType   string
	HomeDir    string
	ConfigFile string
	DebrosDir  string
}

// SystemdGatewayData holds parameters for systemd Gateway service rendering
type SystemdGatewayData struct {
	HomeDir   string
	DebrosDir string
}

// RenderBootstrapConfig renders the bootstrap config template with the given data
func RenderBootstrapConfig(data BootstrapConfigData) (string, error) {
	return renderTemplate("bootstrap.yaml", data)
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

// RenderRQLiteService renders the RQLite systemd service template
func RenderRQLiteService(data SystemdRQLiteData) (string, error) {
	return renderTemplate("systemd_rqlite.service", data)
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
