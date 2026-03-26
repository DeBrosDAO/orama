package sfu

import "fmt"

// Config holds configuration for the SFU server
type Config struct {
	// ListenAddr is the address to bind the signaling WebSocket server.
	// Must be a WireGuard IP (10.0.0.x) — never 0.0.0.0.
	ListenAddr string `yaml:"listen_addr"`

	// Namespace this SFU instance belongs to
	Namespace string `yaml:"namespace"`

	// MediaPortRange defines the UDP port range for RTP media
	MediaPortStart int `yaml:"media_port_start"`
	MediaPortEnd   int `yaml:"media_port_end"`

	// TURN servers this SFU should advertise to peers
	TURNServers []TURNServerConfig `yaml:"turn_servers"`

	// TURNSecret is the shared HMAC-SHA1 secret for generating TURN credentials
	TURNSecret string `yaml:"turn_secret"`

	// TURNCredentialTTL is the lifetime of TURN credentials in seconds
	TURNCredentialTTL int `yaml:"turn_credential_ttl"`

	// RQLiteDSN is the namespace-local RQLite DSN for room state
	RQLiteDSN string `yaml:"rqlite_dsn"`
}

// TURNServerConfig represents a single TURN server endpoint
type TURNServerConfig struct {
	Host   string `yaml:"host"`   // IP or hostname
	Port   int    `yaml:"port"`   // Port number (3478 for TURN, 5349 for TURNS)
	Secure bool   `yaml:"secure"` // true = TURNS (TLS over TCP), false = TURN (UDP)
}

// Validate checks the SFU configuration for errors
func (c *Config) Validate() []error {
	var errs []error

	if c.ListenAddr == "" {
		errs = append(errs, fmt.Errorf("sfu.listen_addr: must not be empty"))
	}

	if c.Namespace == "" {
		errs = append(errs, fmt.Errorf("sfu.namespace: must not be empty"))
	}

	if c.MediaPortStart <= 0 || c.MediaPortEnd <= 0 {
		errs = append(errs, fmt.Errorf("sfu.media_port_range: start and end must be positive"))
	} else if c.MediaPortEnd <= c.MediaPortStart {
		errs = append(errs, fmt.Errorf("sfu.media_port_range: end (%d) must be greater than start (%d)", c.MediaPortEnd, c.MediaPortStart))
	}

	if len(c.TURNServers) == 0 {
		errs = append(errs, fmt.Errorf("sfu.turn_servers: at least one TURN server must be configured"))
	}
	for i, ts := range c.TURNServers {
		if ts.Host == "" {
			errs = append(errs, fmt.Errorf("sfu.turn_servers[%d].host: must not be empty", i))
		}
		if ts.Port <= 0 || ts.Port > 65535 {
			errs = append(errs, fmt.Errorf("sfu.turn_servers[%d].port: must be between 1 and 65535", i))
		}
	}

	if c.TURNSecret == "" {
		errs = append(errs, fmt.Errorf("sfu.turn_secret: must not be empty"))
	}

	if c.TURNCredentialTTL <= 0 {
		errs = append(errs, fmt.Errorf("sfu.turn_credential_ttl: must be positive"))
	}

	if c.RQLiteDSN == "" {
		errs = append(errs, fmt.Errorf("sfu.rqlite_dsn: must not be empty"))
	}

	return errs
}
