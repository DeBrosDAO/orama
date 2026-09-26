package gateway

import (
	"strings"
	"testing"
)

func TestValidateListenAddr(t *testing.T) {
	tests := []struct {
		name      string
		addr      string
		wantErr   bool
		errSubstr string
	}{
		{"valid :8080", ":8080", false, ""},
		{"valid 0.0.0.0:443", "0.0.0.0:443", false, ""},
		{"valid 127.0.0.1:6001", "127.0.0.1:6001", false, ""},
		{"valid :80", ":80", false, ""},
		{"valid high port", ":65535", false, ""},
		{"invalid no colon", "8080", true, "invalid format"},
		{"invalid port zero", ":0", true, "port must be a number"},
		{"invalid port too high", ":99999", true, "port must be a number"},
		{"invalid non-numeric port", ":abc", true, "port must be a number"},
		{"empty string", "", true, "invalid format"},
		{"invalid negative port", ":-1", true, "port must be a number"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateListenAddr(tt.addr)
			if tt.wantErr {
				if err == nil {
					t.Errorf("validateListenAddr(%q) = nil, want error containing %q", tt.addr, tt.errSubstr)
				} else if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("validateListenAddr(%q) error = %q, want error containing %q", tt.addr, err.Error(), tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("validateListenAddr(%q) = %v, want nil", tt.addr, err)
				}
			}
		})
	}
}

func TestValidateRQLiteDSN(t *testing.T) {
	tests := []struct {
		name      string
		dsn       string
		wantErr   bool
		errSubstr string
	}{
		{"valid http localhost", "http://localhost:4001", false, ""},
		{"valid https", "https://db.example.com", false, ""},
		{"valid http with path", "http://192.168.1.1:4001/db", false, ""},
		{"valid https with port", "https://db.example.com:4001", false, ""},
		{"invalid scheme ftp", "ftp://localhost", true, "scheme must be http or https"},
		{"invalid scheme tcp", "tcp://localhost:4001", true, "scheme must be http or https"},
		{"missing host", "http://", true, "host must not be empty"},
		{"no scheme", "localhost:4001", true, "scheme must be http or https"},
		{"empty string", "", true, "scheme must be http or https"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRQLiteDSN(tt.dsn)
			if tt.wantErr {
				if err == nil {
					t.Errorf("validateRQLiteDSN(%q) = nil, want error containing %q", tt.dsn, tt.errSubstr)
				} else if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("validateRQLiteDSN(%q) error = %q, want error containing %q", tt.dsn, err.Error(), tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("validateRQLiteDSN(%q) = %v, want nil", tt.dsn, err)
				}
			}
		})
	}
}

func TestExtractTCPPort_Gateway(t *testing.T) {
	tests := []struct {
		name      string
		multiaddr string
		want      string
	}{
		{
			"standard multiaddr",
			"/ip4/127.0.0.1/tcp/4001/p2p/12D3KooWExample",
			"4001",
		},
		{
			"no tcp component",
			"/ip4/127.0.0.1/udp/4001",
			"",
		},
		{
			"multiple tcp segments uses last",
			"/ip4/127.0.0.1/tcp/4001/tcp/5001/p2p/12D3KooWExample",
			"5001",
		},
		{
			"tcp port at end",
			"/ip4/0.0.0.0/tcp/8080",
			"8080",
		},
		{
			"empty string",
			"",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTCPPort(tt.multiaddr)
			if got != tt.want {
				t.Errorf("extractTCPPort(%q) = %q, want %q", tt.multiaddr, got, tt.want)
			}
		})
	}
}

func TestValidateConfig_Empty(t *testing.T) {
	cfg := &Config{}
	errs := cfg.ValidateConfig()

	if len(errs) == 0 {
		t.Fatal("empty config should produce validation errors")
	}

	// Should have errors for listen_addr and client_namespace at minimum
	var foundListenAddr, foundClientNamespace bool
	for _, err := range errs {
		msg := err.Error()
		if strings.Contains(msg, "listen_addr") {
			foundListenAddr = true
		}
		if strings.Contains(msg, "client_namespace") {
			foundClientNamespace = true
		}
	}

	if !foundListenAddr {
		t.Error("expected validation error for listen_addr, got none")
	}
	if !foundClientNamespace {
		t.Error("expected validation error for client_namespace, got none")
	}
}

func TestValidateConfig_ValidMinimal(t *testing.T) {
	cfg := &Config{
		ListenAddr:      ":8080",
		ClientNamespace: "default",
		RQLiteDSN:       "http://10.0.0.1:10100",
		StateDir:        "/opt/orama/.orama/data/namespaces/default/gateway",
		BaseDomain:      "example.com",
		NodePeerID:      testNodePeerID,
	}
	errs := cfg.ValidateConfig()

	if len(errs) > 0 {
		t.Errorf("valid minimal config should not produce errors, got: %v", errs)
	}
}

func TestValidateConfig_DuplicateBootstrapPeers(t *testing.T) {
	peer := "/ip4/127.0.0.1/tcp/4001/p2p/12D3KooWHbcFcrGPXKUrHcxvd8MXEeUzRYyvY8fQcpEBxncSUwhj"
	cfg := &Config{
		ListenAddr:      ":8080",
		ClientNamespace: "default",
		BootstrapPeers:  []string{peer, peer},
	}
	errs := cfg.ValidateConfig()

	var foundDuplicate bool
	for _, err := range errs {
		if strings.Contains(err.Error(), "duplicate") {
			foundDuplicate = true
			break
		}
	}

	if !foundDuplicate {
		t.Error("expected duplicate bootstrap peer error, got none")
	}
}

func TestValidateConfig_InvalidMultiaddr(t *testing.T) {
	cfg := &Config{
		ListenAddr:      ":8080",
		ClientNamespace: "default",
		BootstrapPeers:  []string{"not-a-multiaddr"},
	}
	errs := cfg.ValidateConfig()

	if len(errs) == 0 {
		t.Fatal("invalid multiaddr should produce validation error")
	}

	var foundInvalid bool
	for _, err := range errs {
		if strings.Contains(err.Error(), "invalid multiaddr") {
			foundInvalid = true
			break
		}
	}

	if !foundInvalid {
		t.Errorf("expected 'invalid multiaddr' error, got: %v", errs)
	}
}

func TestValidateConfig_MissingP2PComponent(t *testing.T) {
	cfg := &Config{
		ListenAddr:      ":8080",
		ClientNamespace: "default",
		BootstrapPeers:  []string{"/ip4/127.0.0.1/tcp/4001"},
	}
	errs := cfg.ValidateConfig()

	var foundMissingP2P bool
	for _, err := range errs {
		if strings.Contains(err.Error(), "missing /p2p/") {
			foundMissingP2P = true
			break
		}
	}

	if !foundMissingP2P {
		t.Errorf("expected 'missing /p2p/' error, got: %v", errs)
	}
}

func TestValidateConfig_InvalidListenAddr(t *testing.T) {
	cfg := &Config{
		ListenAddr:      "not-valid",
		ClientNamespace: "default",
	}
	errs := cfg.ValidateConfig()

	if len(errs) == 0 {
		t.Fatal("invalid listen_addr should produce validation error")
	}

	var foundListenAddr bool
	for _, err := range errs {
		if strings.Contains(err.Error(), "listen_addr") {
			foundListenAddr = true
			break
		}
	}

	if !foundListenAddr {
		t.Errorf("expected listen_addr error, got: %v", errs)
	}
}

func TestValidateConfig_InvalidRQLiteDSN(t *testing.T) {
	cfg := &Config{
		ListenAddr:      ":8080",
		ClientNamespace: "default",
		RQLiteDSN:       "ftp://invalid",
	}
	errs := cfg.ValidateConfig()

	var foundDSN bool
	for _, err := range errs {
		if strings.Contains(err.Error(), "rqlite_dsn") {
			foundDSN = true
			break
		}
	}

	if !foundDSN {
		t.Errorf("expected rqlite_dsn error, got: %v", errs)
	}
}

func TestValidateConfig_DomainNameWithoutHTTPSIsFine(t *testing.T) {
	cfg := &Config{
		ListenAddr:      ":10104",
		ClientNamespace: "default",
		RQLiteDSN:       "http://10.0.0.1:10100",
		DomainName:      "example.com",
		BaseDomain:      "example.com",
		StateDir:        "/opt/orama/.orama/data/namespaces/default/gateway",
		NodePeerID:      testNodePeerID,
	}
	errs := cfg.ValidateConfig()
	if len(errs) > 0 {
		t.Errorf("domain_name without enable_https must be valid (Caddy terminates TLS), got: %v", errs)
	}
}

// rqlited binds only its WireGuard address, so there is no default the gateway
// could fall back to: an empty rqlite_dsn is a configuration error.
func TestValidateConfig_EmptyRQLiteDSNRejected(t *testing.T) {
	cfg := &Config{
		ListenAddr:      ":8080",
		ClientNamespace: "default",
		RQLiteDSN:       "",
	}
	errs := cfg.ValidateConfig()

	for _, err := range errs {
		if strings.Contains(err.Error(), "rqlite_dsn") {
			return
		}
	}
	t.Errorf("empty rqlite_dsn must be rejected, got: %v", errs)
}

// A gateway with no state_dir has nowhere it may write its signing keys, so it
// would start, answer /health, and serve no /v1/auth/* route at all. It is
// refused at config load instead.
func TestValidateConfig_MissingStateDirRejected(t *testing.T) {
	cfg := &Config{
		ListenAddr:      ":10104",
		ClientNamespace: "index",
		RQLiteDSN:       "http://10.0.0.1:10100",
	}
	for _, err := range cfg.ValidateConfig() {
		if strings.Contains(err.Error(), "state_dir") && strings.Contains(err.Error(), "must not be empty") {
			return
		}
	}
	t.Error("a gateway config without state_dir was accepted")
}

// A relative state_dir resolves against the unit's WorkingDirectory, outside
// the paths it may write.
func TestValidateConfig_RelativeStateDirRejected(t *testing.T) {
	cfg := &Config{
		ListenAddr:      ":10104",
		ClientNamespace: "index",
		RQLiteDSN:       "http://10.0.0.1:10100",
		StateDir:        "data/namespaces/index/gateway",
	}
	for _, err := range cfg.ValidateConfig() {
		if strings.Contains(err.Error(), "state_dir") && strings.Contains(err.Error(), "absolute") {
			return
		}
	}
	t.Error("a relative state_dir was accepted")
}

// A missing base domain used to become a cluster that no longer exists: the
// gateway routed no deployment and refused every TLS check for its real domain.
func TestValidateConfig_MissingBaseDomainRejected(t *testing.T) {
	for _, base := range []string{"", "   "} {
		cfg := &Config{
			ListenAddr:      ":10104",
			ClientNamespace: "index",
			RQLiteDSN:       "http://10.0.0.1:10100",
			StateDir:        "/opt/orama/.orama/data/namespaces/index/gateway",
			BaseDomain:      base,
			NodePeerID:      testNodePeerID,
		}
		errs := cfg.ValidateConfig()
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), "domain_name") {
			t.Errorf("base domain %q: got %v, want exactly the domain_name error", base, errs)
		}
	}
}

func TestValidateConfig_BaseDomainMustBeADomain(t *testing.T) {
	cfg := func(base string) *Config {
		return &Config{
			ListenAddr:      ":10104",
			ClientNamespace: "index",
			RQLiteDSN:       "http://10.0.0.1:10100",
			StateDir:        "/opt/orama/.orama/data/namespaces/index/gateway",
			BaseDomain:      base,
			NodePeerID:      testNodePeerID,
		}
	}
	for _, bad := range []string{".", "com", "example..com", "-x.com", "x-.com", "a b.com", "example.com.", "*.example.com", "Example.COM", strings.Repeat("a.", 127) + "com"} {
		if errs := cfg(bad).ValidateConfig(); len(errs) != 1 || !strings.Contains(errs[0].Error(), "domain_name") {
			t.Errorf("base domain %q: got %v, want the domain_name error", bad, errs)
		}
	}
	for _, good := range []string{"example.com", "orama-devnet.network", "a.b.c.example.com"} {
		if errs := cfg(good).ValidateConfig(); len(errs) != 0 {
			t.Errorf("base domain %q refused: %v", good, errs)
		}
	}
}

// testNodePeerID is a well-formed libp2p peer id.
const testNodePeerID = "12D3KooWHbcFcrGPXKUrHcxvd8MXEeUzRYyvY8fQcpEBxncSUwhj"

// validNodeConfig is a config that passes ValidateConfig, for tests that
// change one field.
func validNodeConfig() *Config {
	return &Config{
		ListenAddr:      ":8080",
		ClientNamespace: "default",
		RQLiteDSN:       "http://10.0.0.1:10100",
		StateDir:        "/opt/orama/.orama/data/namespaces/default/gateway",
		BaseDomain:      "example.com",
		NodePeerID:      testNodePeerID,
	}
}

// Without the node's peer id, SQLite home-node assignment, deployment
// placement, host TURN and leader locality all match no node — silently.
func TestValidateConfig_NodePeerIDRequired(t *testing.T) {
	for name, id := range map[string]string{"empty": "", "blank": "  "} {
		t.Run(name, func(t *testing.T) {
			cfg := validNodeConfig()
			cfg.NodePeerID = id
			errs := cfg.ValidateConfig()
			if len(errs) != 1 || !strings.Contains(errs[0].Error(), "gateway.node_peer_id") {
				t.Fatalf("got %v, want one node_peer_id error", errs)
			}
			if !strings.Contains(errs[0].Error(), "cluster_secret_path") {
				t.Errorf("the error does not say where the id comes from: %v", errs[0])
			}
		})
	}
}

func TestValidateConfig_NodePeerIDMustBeAPeerID(t *testing.T) {
	cfg := validNodeConfig()
	cfg.NodePeerID = "node-1"
	errs := cfg.ValidateConfig()
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "not a libp2p peer id") {
		t.Fatalf("got %v, want the id refused as not a peer id", errs)
	}
}

func TestValidateConfig_NodePeerIDAccepted(t *testing.T) {
	if errs := validNodeConfig().ValidateConfig(); len(errs) != 0 {
		t.Fatalf("a config with a valid node peer id was refused: %v", errs)
	}
}
