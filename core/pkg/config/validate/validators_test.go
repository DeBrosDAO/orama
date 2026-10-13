package validate

import (
	"strings"
	"testing"
)

func TestValidateHostPort(t *testing.T) {
	tests := []struct {
		name      string
		hostPort  string
		wantErr   bool
		errSubstr string
	}{
		{"valid localhost:8080", "localhost:8080", false, ""},
		{"valid 0.0.0.0:443", "0.0.0.0:443", false, ""},
		{"valid 192.168.1.1:9090", "192.168.1.1:9090", false, ""},
		{"valid max port", "host:65535", false, ""},
		{"valid port 1", "host:1", false, ""},
		{"missing port", "localhost", true, "expected format host:port"},
		{"missing host", ":8080", true, "host must not be empty"},
		{"non-numeric port", "host:abc", true, "port must be a number"},
		{"port too large", "host:99999", true, "port must be a number"},
		{"port zero", "host:0", true, "port must be a number"},
		{"empty string", "", true, "expected format host:port"},
		{"negative port", "host:-1", true, "port must be a number"},
		{"multiple colons", "host:80:90", true, "expected format host:port"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHostPort(tt.hostPort)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateHostPort(%q) = nil, want error containing %q", tt.hostPort, tt.errSubstr)
				} else if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("ValidateHostPort(%q) error = %q, want error containing %q", tt.hostPort, err.Error(), tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateHostPort(%q) = %v, want nil", tt.hostPort, err)
				}
			}
		})
	}
}

func TestValidatePort(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{"valid port 1", 1, false},
		{"valid port 80", 80, false},
		{"valid port 443", 443, false},
		{"valid port 8080", 8080, false},
		{"valid port 65535", 65535, false},
		{"invalid port 0", 0, true},
		{"invalid port -1", -1, true},
		{"invalid port 65536", 65536, true},
		{"invalid large port", 100000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePort(tt.port)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidatePort(%d) = nil, want error", tt.port)
				}
			} else {
				if err != nil {
					t.Errorf("ValidatePort(%d) = %v, want nil", tt.port, err)
				}
			}
		})
	}
}

func TestValidateHostOrHostPort(t *testing.T) {
	tests := []struct {
		name      string
		addr      string
		wantErr   bool
		errSubstr string
	}{
		{"valid host only", "localhost", false, ""},
		{"valid hostname", "myserver.example.com", false, ""},
		{"valid IP", "192.168.1.1", false, ""},
		{"valid host:port", "localhost:8080", false, ""},
		{"valid IP:port", "0.0.0.0:443", false, ""},
		{"empty string", "", true, "address must not be empty"},
		{"invalid port in host:port", "host:abc", true, "port must be a number"},
		{"missing host in host:port", ":8080", true, "host must not be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHostOrHostPort(tt.addr)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateHostOrHostPort(%q) = nil, want error containing %q", tt.addr, tt.errSubstr)
				} else if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("ValidateHostOrHostPort(%q) error = %q, want error containing %q", tt.addr, err.Error(), tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateHostOrHostPort(%q) = %v, want nil", tt.addr, err)
				}
			}
		})
	}
}

func TestExtractSwarmKeyHex(t *testing.T) {
	validHex := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"full swarm key format",
			"/key/swarm/psk/1.0.0/\n/base16/\n" + validHex + "\n",
			validHex,
		},
		{
			"full swarm key format no trailing newline",
			"/key/swarm/psk/1.0.0/\n/base16/\n" + validHex,
			validHex,
		},
		{
			"raw hex string",
			validHex,
			validHex,
		},
		{
			"with leading and trailing whitespace",
			"  " + validHex + "  ",
			validHex,
		},
		{
			"empty string",
			"",
			"",
		},
		{
			"only header lines no hex",
			"/key/swarm/psk/1.0.0/\n/base16/\n",
			"/key/swarm/psk/1.0.0/\n/base16/",
		},
		{
			"base16 marker only",
			"/base16/\n" + validHex,
			validHex,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractSwarmKeyHex(tt.input)
			if got != tt.want {
				t.Errorf("ExtractSwarmKeyHex(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateSwarmKey(t *testing.T) {
	validHex := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

	tests := []struct {
		name      string
		key       string
		wantErr   bool
		errSubstr string
	}{
		{
			"valid 64-char hex",
			validHex,
			false,
			"",
		},
		{
			"valid full swarm key format",
			"/key/swarm/psk/1.0.0/\n/base16/\n" + validHex,
			false,
			"",
		},
		{
			"too short",
			"a1b2c3d4",
			true,
			"must be 64 hex characters",
		},
		{
			"too long",
			validHex + "ffff",
			true,
			"must be 64 hex characters",
		},
		{
			"non-hex characters",
			"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
			true,
			"must be valid hexadecimal",
		},
		{
			"empty string",
			"",
			true,
			"must be 64 hex characters",
		},
		{
			"63 chars (one short)",
			validHex[:63],
			true,
			"must be 64 hex characters",
		},
		{
			"65 chars (one over)",
			validHex + "a",
			true,
			"must be 64 hex characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSwarmKey(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateSwarmKey(%q) = nil, want error containing %q", tt.key, tt.errSubstr)
				} else if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("ValidateSwarmKey(%q) error = %q, want error containing %q", tt.key, err.Error(), tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateSwarmKey(%q) = %v, want nil", tt.key, err)
				}
			}
		})
	}
}

func TestExtractTCPPort(t *testing.T) {
	tests := []struct {
		name      string
		multiaddr string
		want      string
	}{
		{
			"valid multiaddr with tcp port",
			"/ip4/127.0.0.1/tcp/4001/p2p/12D3KooWExample",
			"4001",
		},
		{
			"valid multiaddr no p2p",
			"/ip4/0.0.0.0/tcp/8080",
			"8080",
		},
		{
			"ipv6 with tcp port",
			"/ip6/::/tcp/9090/p2p/12D3KooWExample",
			"9090",
		},
		{
			"no tcp component",
			"/ip4/127.0.0.1/udp/4001",
			"",
		},
		{
			"empty string",
			"",
			"",
		},
		{
			"tcp at end without port value",
			"/ip4/127.0.0.1/tcp",
			"",
		},
		{
			"only tcp with port",
			"/tcp/443",
			"443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTCPPort(tt.multiaddr)
			if got != tt.want {
				t.Errorf("ExtractTCPPort(%q) = %q, want %q", tt.multiaddr, got, tt.want)
			}
		})
	}
}

func TestValidationError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  ValidationError
		want string
	}{
		{
			"with hint",
			ValidationError{
				Path:    "discovery.bootstrap_peers[0]",
				Message: "invalid multiaddr",
				Hint:    "expected /ip{4,6}/.../tcp/<port>/p2p/<peerID>",
			},
			"discovery.bootstrap_peers[0]: invalid multiaddr; expected /ip{4,6}/.../tcp/<port>/p2p/<peerID>",
		},
		{
			"without hint",
			ValidationError{
				Path:    "node.listen_addr",
				Message: "must not be empty",
			},
			"node.listen_addr: must not be empty",
		},
		{
			"empty hint",
			ValidationError{
				Path:    "config.port",
				Message: "invalid",
				Hint:    "",
			},
			"config.port: invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.want {
				t.Errorf("ValidationError.Error() = %q, want %q", got, tt.want)
			}
		})
	}
}
