package turn

import (
	"fmt"
	"net"
)

// Config holds configuration for the TURN server
type Config struct {
	// ListenAddr is the address to bind the TURN listener (e.g., "0.0.0.0:3478")
	ListenAddr string `yaml:"listen_addr"`

	// TURNSListenAddr is the address for TURNS (TURN over TLS on TCP, e.g., "0.0.0.0:5349")
	TURNSListenAddr string `yaml:"turns_listen_addr"`

	// TLSCertPath is the path to the TLS certificate PEM file (for TURNS)
	TLSCertPath string `yaml:"tls_cert_path"`

	// TLSKeyPath is the path to the TLS private key PEM file (for TURNS)
	TLSKeyPath string `yaml:"tls_key_path"`

	// PublicIP is the public IP address of this node, advertised in TURN allocations
	PublicIP string `yaml:"public_ip"`

	// Realm is the TURN realm (typically the base domain)
	Realm string `yaml:"realm"`

	// AuthSecret is the HMAC-SHA1 shared secret for credential validation
	AuthSecret string `yaml:"auth_secret"`

	// RelayPortStart is the beginning of the UDP relay port range
	RelayPortStart int `yaml:"relay_port_start"`

	// RelayPortEnd is the end of the UDP relay port range
	RelayPortEnd int `yaml:"relay_port_end"`

	// Namespace this TURN instance belongs to
	Namespace string `yaml:"namespace"`

	// StealthDomain is the neutral, CDN-bland SNI hostname this server also
	// answers TURNS for (e.g. "cdn-a1b2c3d4e5f6.orama-devnet.network").
	//
	// The stealth endpoint is an SNI-router passthrough, NOT a separate TURN
	// server: a router on :443 reads only the TLS ClientHello SNI and forwards
	// the raw bytes for this hostname to this same TURNS listener. TLS is still
	// terminated here, by this TURN server, which therefore presents two certs
	// (the primary TURN domain and StealthDomain) selected by ClientHello SNI.
	// When empty, the stealth endpoint is disabled and behavior is unchanged.
	StealthDomain string `yaml:"stealth_domain,omitempty"`

	// TLSStealthCertPath is the path to the TLS certificate PEM file presented
	// for StealthDomain. The SNI router only forwards bytes; this TURN server
	// terminates the TLS handshake, so it needs the stealth domain's cert here.
	TLSStealthCertPath string `yaml:"tls_stealth_cert_path,omitempty"`

	// TLSStealthKeyPath is the path to the TLS private key PEM file for the
	// StealthDomain certificate (TURN terminates TLS for the router-forwarded
	// stealth connections).
	TLSStealthKeyPath string `yaml:"tls_stealth_key_path,omitempty"`
}

// Validate checks the TURN configuration for errors
func (c *Config) Validate() []error {
	var errs []error

	if c.ListenAddr == "" {
		errs = append(errs, fmt.Errorf("turn.listen_addr: must not be empty"))
	}

	if c.PublicIP == "" {
		errs = append(errs, fmt.Errorf("turn.public_ip: must not be empty"))
	} else if ip := net.ParseIP(c.PublicIP); ip == nil {
		errs = append(errs, fmt.Errorf("turn.public_ip: %q is not a valid IP address", c.PublicIP))
	}

	if c.Realm == "" {
		errs = append(errs, fmt.Errorf("turn.realm: must not be empty"))
	}

	if c.AuthSecret == "" {
		errs = append(errs, fmt.Errorf("turn.auth_secret: must not be empty"))
	}

	if c.RelayPortStart <= 0 || c.RelayPortEnd <= 0 {
		errs = append(errs, fmt.Errorf("turn.relay_port_range: start and end must be positive"))
	} else if c.RelayPortEnd <= c.RelayPortStart {
		errs = append(errs, fmt.Errorf("turn.relay_port_range: end (%d) must be greater than start (%d)", c.RelayPortEnd, c.RelayPortStart))
	} else if c.RelayPortEnd-c.RelayPortStart < 100 {
		errs = append(errs, fmt.Errorf("turn.relay_port_range: range must be at least 100 ports (got %d)", c.RelayPortEnd-c.RelayPortStart))
	}

	if c.Namespace == "" {
		errs = append(errs, fmt.Errorf("turn.namespace: must not be empty"))
	}

	return errs
}
