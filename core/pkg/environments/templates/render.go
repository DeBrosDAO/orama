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
	RQLiteRaftPort         int    // External Raft port for advertisement
	RQLiteRaftInternalPort int    // Internal Raft port for local binding (SNI only)
	RQLiteJoinAddress      string // Optional: join address for joining existing cluster

	// RQLite HTTP basic auth. The username/password go into every client this
	// node opens (SQL DSN and admin API); RQLiteAuthFile is the same
	// credentials in rqlite's own JSON format.
	//
	// Rendered whenever they are known, because sending credentials to an
	// rqlited that does not require them is harmless — and it is the first of
	// the two passes that make enabling enforcement safe. Enforcement itself
	// (rqlite_enforce_auth) is deliberately not rendered: it is switched on by
	// an operator once every node in the fleet is sending credentials.
	RQLiteUsername     string
	RQLitePassword     string
	RQLiteAuthFile     string
	BootstrapPeers     []string // List of peer multiaddrs to connect to
	ClusterAPIPort     int
	IPFSAPIPort        int
	OlricHTTPPort      int
	HTTPAdvAddress     string // Advertised HTTP address (IP:port)
	RaftAdvAddress     string // Advertised Raft address (IP:port or domain:port for SNI)
	UnifiedGatewayPort int    // Unified gateway port for all node services
	Domain             string // Domain for this node (e.g., node-123.orama.network)
	BaseDomain         string // Base domain for deployment routing (e.g., dbrs.space)
	EnableHTTPS        bool   // Enable HTTPS/TLS with ACME
	TLSCacheDir        string // Directory for ACME certificate cache
	HTTPPort           int    // HTTP port for ACME challenges (usually 80)
	HTTPSPort          int    // HTTPS port (usually 443)
	WGIP               string // WireGuard IP address (e.g., 10.0.0.1)
	MinClusterSize     int    // Minimum cluster size for RQLite discovery (1 for genesis, 3 for joining)

	// Node-to-node TLS encryption for RQLite Raft communication
	// Required when using SNI gateway for Raft traffic routing
	NodeCert     string // Path to X.509 certificate for node-to-node communication
	NodeKey      string // Path to X.509 private key for node-to-node communication
	NodeCACert   string // Path to CA certificate (optional)
	NodeNoVerify bool   // Skip certificate verification (for self-signed certs)

	// Operator metadata — written to dns_nodes during registration
	SSHUser        string // SSH user for remote management
	Environment    string // Environment name (devnet, testnet, etc.)
	OperatorWallet string // Operator wallet address

	// SecretsEncryptionKey is the AES-256 key (hex, 64 chars) used to encrypt
	// serverless function secrets at rest. Rendered under http_gateway in
	// node.yaml. Sourced from ~/.orama/secrets/secrets-encryption-key — must
	// be identical across all namespace-gateway nodes in a cluster and stable
	// across restarts (bugboard #837). Empty → key omitted from the rendered
	// config (the gateway then reads the secret file directly / get_secret
	// stays disabled until the key is configured).
	SecretsEncryptionKey string

	// NtfyBaseURL is the shared self-hosted ntfy base URL (e.g.
	// "https://push.<dnsZone>"), rendered under http_gateway as ntfy_base_url.
	// When set, the gateway's push provider fans each ntfy publish out to every
	// active push node (bugboard #858). Empty → omitted (single-host delivery).
	NtfyBaseURL string

	// WebRTC/TURN configuration, rendered under http_gateway.webrtc when
	// WebRTCEnabled is true (feat-124 #913). TURNSecret is sourced from
	// ~/.orama/secrets/turn-secret so it survives Phase4 config regeneration;
	// TURNDomain/SFUPort are operator-set values carried forward from the
	// existing node.yaml. The whole block is conditional on TURNSecret being
	// set — clusters without TURN render nothing.
	WebRTCEnabled bool   // Whether to emit the webrtc block
	SFUPort       int    // Local SFU signaling port the gateway proxies to
	TURNDomain    string // TURN domain (e.g., "turn.ns-myapp.dbrs.space")
	TURNSecret    string // HMAC-SHA1 shared secret for TURN credential generation

	// SNIRouterEnabled gates the stealth TURN-over-443 SNI router (feat-124).
	// Rendered as the top-level sni_router.enabled flag. Default false keeps
	// existing nodes byte-identical (Caddy stays on :443); when true the node
	// runs orama-sni-router on :443 and Caddy moves to :8443. This value is
	// carried forward across config regeneration from the existing node.yaml
	// (see production/config.go populateSNIRouterConfig) so a regen never wipes
	// an operator's opt-in (the same preserve-from-existing discipline as the
	// webrtc block, bugboard #259/#846).
	SNIRouterEnabled bool
}

// GatewayConfigData holds parameters for gateway.yaml rendering
type GatewayConfigData struct {
	ListenPort     int
	BootstrapPeers []string
	OlricServers   []string
	ClusterAPIPort int
	IPFSAPIPort    int // Default: constants.IPFSAPIPort
	EnableHTTPS    bool
	DomainName     string
	TLSCacheDir    string
	RQLiteDSN      string
}

// OlricConfigData holds parameters for olric.yaml rendering
type OlricConfigData struct {
	ServerBindAddr          string // HTTP API bind address (127.0.0.1 for security)
	HTTPPort                int
	MemberlistBindAddr      string // Memberlist bind address (WG IP for clustering)
	MemberlistPort          int
	MemberlistEnvironment   string   // "local", "lan", or "wan"
	MemberlistAdvertiseAddr string   // Advertise address (WG IP) so other nodes can reach us
	Peers                   []string // Seed peers for memberlist (host:port)
}

// SystemdIPFSData holds parameters for systemd IPFS service rendering
type SystemdIPFSData struct {
	HomeDir      string
	IPFSRepoPath string
	SecretsDir   string
	OramaDir     string
}

// SystemdIPFSClusterData holds parameters for systemd IPFS Cluster service rendering
type SystemdIPFSClusterData struct {
	HomeDir     string
	ClusterPath string
	OramaDir    string
}

// SystemdOlricData holds parameters for systemd Olric service rendering
type SystemdOlricData struct {
	HomeDir    string
	ConfigPath string
	OramaDir   string
}

// SystemdNodeData holds parameters for systemd Node service rendering
type SystemdNodeData struct {
	HomeDir    string
	ConfigFile string
	OramaDir   string
}

// SystemdGatewayData holds parameters for systemd Gateway service rendering
type SystemdGatewayData struct {
	HomeDir  string
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

// RenderNodeService renders the Orama Node systemd service template
func RenderNodeService(data SystemdNodeData) (string, error) {
	return renderTemplate("systemd_node.service", data)
}

// RenderGatewayService renders the Orama Gateway systemd service template
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
