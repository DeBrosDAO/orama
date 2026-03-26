package sfu

import "testing"

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		wantErrs int
	}{
		{
			name: "valid config",
			config: Config{
				ListenAddr:     "10.0.0.1:8443",
				Namespace:      "test-ns",
				MediaPortStart: 20000,
				MediaPortEnd:   20500,
				TURNServers:    []TURNServerConfig{{Host: "1.2.3.4", Port: 3478}},
				TURNSecret:     "secret-key",
				TURNCredentialTTL: 600,
				RQLiteDSN:      "http://10.0.0.1:4001",
			},
			wantErrs: 0,
		},
		{
			name: "valid config with multiple TURN servers",
			config: Config{
				ListenAddr:     "10.0.0.1:8443",
				Namespace:      "test-ns",
				MediaPortStart: 20000,
				MediaPortEnd:   20500,
				TURNServers: []TURNServerConfig{
					{Host: "1.2.3.4", Port: 3478},
					{Host: "5.6.7.8", Port: 443},
				},
				TURNSecret:        "secret-key",
				TURNCredentialTTL: 600,
				RQLiteDSN:         "http://10.0.0.1:4001",
			},
			wantErrs: 0,
		},
		{
			name:     "missing all fields",
			config:   Config{},
			wantErrs: 7, // listen_addr, namespace, media_port_range, turn_servers, turn_secret, turn_credential_ttl, rqlite_dsn
		},
		{
			name: "missing listen addr",
			config: Config{
				Namespace:         "test-ns",
				MediaPortStart:    20000,
				MediaPortEnd:      20500,
				TURNServers:       []TURNServerConfig{{Host: "1.2.3.4", Port: 3478}},
				TURNSecret:        "secret",
				TURNCredentialTTL: 600,
				RQLiteDSN:         "http://10.0.0.1:4001",
			},
			wantErrs: 1,
		},
		{
			name: "missing namespace",
			config: Config{
				ListenAddr:        "10.0.0.1:8443",
				MediaPortStart:    20000,
				MediaPortEnd:      20500,
				TURNServers:       []TURNServerConfig{{Host: "1.2.3.4", Port: 3478}},
				TURNSecret:        "secret",
				TURNCredentialTTL: 600,
				RQLiteDSN:         "http://10.0.0.1:4001",
			},
			wantErrs: 1,
		},
		{
			name: "invalid media port range - inverted",
			config: Config{
				ListenAddr:        "10.0.0.1:8443",
				Namespace:         "test-ns",
				MediaPortStart:    20500,
				MediaPortEnd:      20000,
				TURNServers:       []TURNServerConfig{{Host: "1.2.3.4", Port: 3478}},
				TURNSecret:        "secret",
				TURNCredentialTTL: 600,
				RQLiteDSN:         "http://10.0.0.1:4001",
			},
			wantErrs: 1,
		},
		{
			name: "invalid media port range - zero",
			config: Config{
				ListenAddr:        "10.0.0.1:8443",
				Namespace:         "test-ns",
				MediaPortStart:    0,
				MediaPortEnd:      0,
				TURNServers:       []TURNServerConfig{{Host: "1.2.3.4", Port: 3478}},
				TURNSecret:        "secret",
				TURNCredentialTTL: 600,
				RQLiteDSN:         "http://10.0.0.1:4001",
			},
			wantErrs: 1,
		},
		{
			name: "no TURN servers",
			config: Config{
				ListenAddr:        "10.0.0.1:8443",
				Namespace:         "test-ns",
				MediaPortStart:    20000,
				MediaPortEnd:      20500,
				TURNServers:       []TURNServerConfig{},
				TURNSecret:        "secret",
				TURNCredentialTTL: 600,
				RQLiteDSN:         "http://10.0.0.1:4001",
			},
			wantErrs: 1,
		},
		{
			name: "TURN server with invalid port",
			config: Config{
				ListenAddr:        "10.0.0.1:8443",
				Namespace:         "test-ns",
				MediaPortStart:    20000,
				MediaPortEnd:      20500,
				TURNServers:       []TURNServerConfig{{Host: "1.2.3.4", Port: 0}},
				TURNSecret:        "secret",
				TURNCredentialTTL: 600,
				RQLiteDSN:         "http://10.0.0.1:4001",
			},
			wantErrs: 1,
		},
		{
			name: "TURN server with empty host",
			config: Config{
				ListenAddr:        "10.0.0.1:8443",
				Namespace:         "test-ns",
				MediaPortStart:    20000,
				MediaPortEnd:      20500,
				TURNServers:       []TURNServerConfig{{Host: "", Port: 3478}},
				TURNSecret:        "secret",
				TURNCredentialTTL: 600,
				RQLiteDSN:         "http://10.0.0.1:4001",
			},
			wantErrs: 1,
		},
		{
			name: "negative credential TTL",
			config: Config{
				ListenAddr:        "10.0.0.1:8443",
				Namespace:         "test-ns",
				MediaPortStart:    20000,
				MediaPortEnd:      20500,
				TURNServers:       []TURNServerConfig{{Host: "1.2.3.4", Port: 3478}},
				TURNSecret:        "secret",
				TURNCredentialTTL: -1,
				RQLiteDSN:         "http://10.0.0.1:4001",
			},
			wantErrs: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.config.Validate()
			if len(errs) != tt.wantErrs {
				t.Errorf("Validate() returned %d errors, want %d: %v", len(errs), tt.wantErrs, errs)
			}
		})
	}
}
