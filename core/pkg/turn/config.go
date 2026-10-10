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

	// Namespace this TURN instance belongs to.
	//
	// Bugboard #283: retained as the SINGLE-TENANT form. TURN binds the
	// well-known ports 3478/5349, which are exclusive per host, so one TURN
	// process per namespace meant only one namespace could have TURN on a given
	// node — the second crash-looped on bind, and on a small fleet later
	// namespaces got no relay at all. Moving each namespace to its own ports
	// would have worked but put TURN on arbitrary high ports, which restrictive
	// networks routinely block — the worst outcome for the users who most need a
	// relay. Instead one server serves many namespaces: see Tenants. Namespace +
	// AuthSecret remain accepted and are normalized into a single tenant, so an
	// existing config keeps working across the rollout.
	Namespace string `yaml:"namespace"`

	// Tenants is the set of namespaces this server accepts credentials for.
	//
	// TURN credentials already carry the namespace ("{expiry}:{namespace}"), so
	// a shared server can authenticate each tenant against its OWN secret with
	// no protocol change and no change to what clients dial. A namespace absent
	// from this list is rejected exactly as a namespace mismatch was before —
	// the isolation boundary is the per-tenant secret lookup, so it must never
	// fall back to a default secret.
	Tenants []TenantConfig `yaml:"tenants,omitempty"`

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

// TenantConfig is one namespace served by a shared TURN server (bugboard #283).
type TenantConfig struct {
	// Namespace this tenant's credentials are issued for.
	Namespace string `yaml:"namespace"`

	// AuthSecret is the HMAC-SHA1 shared secret for THIS namespace. Never shared
	// between tenants: it is what keeps one tenant from minting another's
	// credentials on the shared server.
	AuthSecret string `yaml:"auth_secret"`

	// StealthDomain is this tenant's censorship-resistant TURNS hostname, if
	// enabled. Empty disables stealth for the tenant.
	StealthDomain string `yaml:"stealth_domain,omitempty"`

	// TLSStealthCertPath / TLSStealthKeyPath are the cert and key presented for
	// StealthDomain. The shared server selects them by ClientHello SNI.
	TLSStealthCertPath string `yaml:"tls_stealth_cert_path,omitempty"`
	TLSStealthKeyPath  string `yaml:"tls_stealth_key_path,omitempty"`
}

// ResolvedTenants returns the tenant set this config serves, normalizing the
// legacy single-tenant form (Namespace + AuthSecret) into a one-element list.
//
// Callers must use this rather than reading Namespace/Tenants directly, so the
// two forms can never disagree about who is authorized.
func (c *Config) ResolvedTenants() []TenantConfig {
	if len(c.Tenants) > 0 {
		return c.Tenants
	}
	if c.Namespace == "" || c.AuthSecret == "" {
		return nil
	}
	return []TenantConfig{{
		Namespace:          c.Namespace,
		AuthSecret:         c.AuthSecret,
		StealthDomain:      c.StealthDomain,
		TLSStealthCertPath: c.TLSStealthCertPath,
		TLSStealthKeyPath:  c.TLSStealthKeyPath,
	}}
}

// TenantSecret returns the auth secret for a namespace, and whether it is served
// here at all. A miss must be treated as "not authorized" — never as a reason to
// fall back to some other secret.
func (c *Config) TenantSecret(namespace string) (string, bool) {
	for _, t := range c.ResolvedTenants() {
		if t.Namespace == namespace {
			if t.AuthSecret == "" {
				return "", false
			}
			return t.AuthSecret, true
		}
	}
	return "", false
}

// Validate checks the TURN configuration for errors
func (c *Config) Validate() []error {
	var errs []error

	// NOTE (bugboard #161): a zero port ("0.0.0.0:0") is deliberately NOT rejected
	// here. It binds an OS-assigned ephemeral port, which is a legitimate and
	// widely-used idiom for tests that need a free port. The danger is a
	// PRODUCTION spawn built from an incomplete port allocation, where a zero
	// port yields a server that looks healthy and relays nothing — that is
	// guarded at the spawn site by turnPortBlockSpawnable (pkg/namespace), which
	// refuses to write such a config in the first place. Validating it here would
	// break the test idiom without protecting anything the spawn guard misses.
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

	tenants := c.ResolvedTenants()
	if len(tenants) == 0 {
		errs = append(errs, fmt.Errorf("turn: no tenants configured (set tenants, or namespace + auth_secret)"))
	}
	seen := make(map[string]bool, len(tenants))
	for i, t := range tenants {
		if t.Namespace == "" {
			errs = append(errs, fmt.Errorf("turn.tenants[%d].namespace: must not be empty", i))
			continue
		}
		if t.AuthSecret == "" {
			errs = append(errs, fmt.Errorf("turn.tenants[%d] (%s): auth_secret must not be empty", i, t.Namespace))
		}
		if seen[t.Namespace] {
			// Two entries for one namespace makes which secret authorizes it
			// order-dependent — refuse rather than pick.
			errs = append(errs, fmt.Errorf("turn.tenants: namespace %q listed more than once", t.Namespace))
		}
		seen[t.Namespace] = true
	}

	if c.RelayPortStart <= 0 || c.RelayPortEnd <= 0 {
		errs = append(errs, fmt.Errorf("turn.relay_port_range: start and end must be positive"))
	} else if c.RelayPortEnd <= c.RelayPortStart {
		errs = append(errs, fmt.Errorf("turn.relay_port_range: end (%d) must be greater than start (%d)", c.RelayPortEnd, c.RelayPortStart))
	} else if c.RelayPortEnd-c.RelayPortStart < 100 {
		errs = append(errs, fmt.Errorf("turn.relay_port_range: range must be at least 100 ports (got %d)", c.RelayPortEnd-c.RelayPortStart))
	}

	return errs
}
