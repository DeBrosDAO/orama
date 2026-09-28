package clusterreg

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// Limits copied from chain/x/nodes/types. A test reads that file so a change
// there fails here instead of accepting a registration the chain will reject.
const (
	MaxIDLen          = 64
	MaxEndpointLen    = 256
	MaxDomainLen      = 253
	MaxMetadataURILen = 256
	MaxEndpoints      = 64
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// Registration is MsgRegisterCluster's stateless body. It carries no node
// address, tenant name, or cluster secret.
type Registration struct {
	Operator    string
	ClusterID   string
	BaseDomain  string
	Endpoints   []string
	MetadataURI string
}

// Validate checks the fields x/nodes ValidateBasic checks, including that
// every endpoint is a public address.
func Validate(r Registration) error {
	if _, err := CanonicalAccount(r.Operator); err != nil {
		return fmt.Errorf("operator: %w", err)
	}
	if !idPattern.MatchString(r.ClusterID) || len(r.ClusterID) > MaxIDLen {
		return fmt.Errorf("cluster id %q must match %s", r.ClusterID, idPattern.String())
	}
	if err := validateBaseDomain(r.BaseDomain); err != nil {
		return err
	}
	if err := validateEndpoints(r.Endpoints); err != nil {
		return err
	}
	return validateMetadata(r.MetadataURI)
}

func validateBaseDomain(domain string) error {
	if domain == "" || len(domain) > MaxDomainLen {
		return fmt.Errorf("base domain length must be 1..%d", MaxDomainLen)
	}
	if domain != strings.ToLower(domain) {
		return fmt.Errorf("base domain must be lowercase")
	}
	if net.ParseIP(domain) != nil {
		return fmt.Errorf("base domain must be a name, not an ip")
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return fmt.Errorf("base domain must have at least two labels")
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("domain label %q has an invalid length or hyphen", label)
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return fmt.Errorf("domain label %q has a character outside [a-z0-9-]", label)
			}
		}
	}
	return nil
}

func validateEndpoints(endpoints []string) error {
	if len(endpoints) < 1 {
		return fmt.Errorf("got 0 endpoints, need at least 1")
	}
	if len(endpoints) > MaxEndpoints {
		return fmt.Errorf("got %d endpoints, max is %d", len(endpoints), MaxEndpoints)
	}
	seen := map[string]struct{}{}
	for _, ep := range endpoints {
		if err := validateEndpoint(ep); err != nil {
			return err
		}
		if _, ok := seen[ep]; ok {
			return fmt.Errorf("duplicate endpoint %q", ep)
		}
		seen[ep] = struct{}{}
	}
	return nil
}

func validateEndpoint(ep string) error {
	if ep == "" {
		return fmt.Errorf("endpoint is empty")
	}
	if len(ep) > MaxEndpointLen {
		return fmt.Errorf("endpoint exceeds %d bytes", MaxEndpointLen)
	}
	for i := 0; i < len(ep); i++ {
		if ep[i] < 0x21 || ep[i] > 0x7e {
			return fmt.Errorf("endpoint %q contains a non-printable or non-ascii character", ep)
		}
	}
	if strings.Contains(ep, "@") {
		return fmt.Errorf("endpoint %q must not include userinfo", ep)
	}
	hosts, err := endpointHosts(ep)
	if err != nil {
		return fmt.Errorf("endpoint %q: %w", ep, err)
	}
	for _, host := range hosts {
		if err := rejectNonPublicHost(host); err != nil {
			return fmt.Errorf("endpoint %q: %w", ep, err)
		}
	}
	return nil
}

func endpointHosts(ep string) ([]string, error) {
	if strings.HasPrefix(ep, "/") {
		return multiaddrHosts(ep)
	}
	if strings.Contains(ep, "://") {
		u, err := url.Parse(ep)
		if err != nil {
			return nil, fmt.Errorf("parse endpoint: %w", err)
		}
		if u.Scheme != "https" && u.Scheme != "http" {
			return nil, fmt.Errorf("scheme %q is not http or https", u.Scheme)
		}
		if u.Host == "" {
			return nil, fmt.Errorf("missing host")
		}
		if u.User != nil {
			return nil, fmt.Errorf("must not include userinfo")
		}
		return []string{u.Hostname()}, nil
	}
	host := ep
	if h, _, err := net.SplitHostPort(ep); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return nil, fmt.Errorf("missing host")
	}
	return []string{host}, nil
}

func multiaddrHosts(ep string) ([]string, error) {
	parts := strings.Split(ep, "/")
	var hosts []string
	for i := 1; i+1 < len(parts); i += 2 {
		switch parts[i] {
		case "ip4", "ip6", "dns", "dns4", "dns6", "dnsaddr":
			hosts = append(hosts, parts[i+1])
		case "tcp", "udp", "p2p", "sni":
		case "onion", "onion3":
			host, _, _ := strings.Cut(parts[i+1], ":")
			hosts = append(hosts, host)
		default:
			return nil, fmt.Errorf("unsupported multiaddr protocol %q", parts[i])
		}
	}
	if len(parts)%2 == 0 {
		return nil, fmt.Errorf("multiaddr %q has a trailing protocol", ep)
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("multiaddr has no host")
	}
	return hosts, nil
}

func rejectNonPublicHost(host string) error {
	host = strings.TrimSuffix(host, ".")
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") || strings.HasSuffix(lower, ".local") {
		return fmt.Errorf("host %q is not public", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsGlobalUnicast() || ip.IsPrivate() {
			return fmt.Errorf("ip %s is not a public address", ip)
		}
	}
	return nil
}

func validateMetadata(uri string) error {
	if uri == "" {
		return nil
	}
	if len(uri) > MaxMetadataURILen {
		return fmt.Errorf("metadata uri exceeds %d bytes", MaxMetadataURILen)
	}
	u, err := url.ParseRequestURI(uri)
	if err != nil {
		return fmt.Errorf("metadata uri: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fmt.Errorf("metadata uri must be https without userinfo")
	}
	if err := rejectNonPublicHost(u.Hostname()); err != nil {
		return fmt.Errorf("metadata uri: %w", err)
	}
	return nil
}
