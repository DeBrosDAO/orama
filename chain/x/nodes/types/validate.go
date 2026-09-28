package types

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	// MaxIDLen is the longest operator-chosen node or cluster id.
	MaxIDLen = 64
	// MaxEndpointLen is the longest single endpoint string.
	MaxEndpointLen = 256
	// MaxRegionHintLen is the longest region hint.
	MaxRegionHintLen = 64
	// MaxDomainLen is the longest cluster base domain.
	MaxDomainLen = 253
	// MaxMetadataURILen is the longest cluster metadata URI.
	MaxMetadataURILen = 256
	// Secp256k1PubKeyLen is a compressed secp256k1 public key.
	Secp256k1PubKeyLen = 33
	// Ed25519PubKeyLen is an ed25519 public key.
	Ed25519PubKeyLen = 32
	// SignatureLen is the length of both secp256k1 (R||S) and ed25519 signatures.
	SignatureLen = 64
)

var (
	idPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
	servicePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	regionPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
)

// CanonicalAddress parses a bech32 account and returns its canonical string.
func CanonicalAddress(bech32 string) (string, error) {
	addr, err := sdk.AccAddressFromBech32(bech32)
	if err != nil {
		return "", fmt.Errorf("address %q: %w", bech32, err)
	}
	return addr.String(), nil
}

// ValidateID checks an operator-chosen node or cluster id.
func ValidateID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("id %q must match %s", id, idPattern.String())
	}
	return nil
}

// ValidateRoles checks a node role set: non-empty, known, no duplicates.
func ValidateRoles(roles []Role) error {
	if len(roles) == 0 {
		return fmt.Errorf("at least one role is required")
	}
	seen := make(map[Role]struct{}, len(roles))
	for _, role := range roles {
		if !knownRole(role) {
			return fmt.Errorf("unknown role %s", role)
		}
		if _, ok := seen[role]; ok {
			return fmt.Errorf("duplicate role %s", role)
		}
		seen[role] = struct{}{}
	}
	return nil
}

// HasRole reports whether roles contains role.
func HasRole(roles []Role, role Role) bool {
	for _, got := range roles {
		if got == role {
			return true
		}
	}
	return false
}

// ValidateRegion checks a region hint. Empty is allowed.
func ValidateRegion(region string) error {
	if region == "" {
		return nil
	}
	if len(region) > MaxRegionHintLen || !regionPattern.MatchString(region) {
		return fmt.Errorf("region hint %q must match %s", region, regionPattern.String())
	}
	return nil
}

// ValidateBindingShape checks a binding's fields without verifying the signature.
func ValidateBindingShape(binding Binding) error {
	if !servicePattern.MatchString(binding.Service) {
		return fmt.Errorf("service %q must match %s", binding.Service, servicePattern.String())
	}
	switch binding.KeyType {
	case KeyTypeSecp256k1:
		if len(binding.Pubkey) != Secp256k1PubKeyLen {
			return fmt.Errorf("service %q secp256k1 pubkey must be %d bytes, got %d", binding.Service, Secp256k1PubKeyLen, len(binding.Pubkey))
		}
	case KeyTypeEd25519:
		if len(binding.Pubkey) != Ed25519PubKeyLen {
			return fmt.Errorf("service %q ed25519 pubkey must be %d bytes, got %d", binding.Service, Ed25519PubKeyLen, len(binding.Pubkey))
		}
	default:
		return fmt.Errorf("service %q has unknown key type %s", binding.Service, binding.KeyType)
	}
	if len(binding.Signature) != SignatureLen {
		return fmt.Errorf("service %q signature must be %d bytes, got %d", binding.Service, SignatureLen, len(binding.Signature))
	}
	return nil
}

// ValidateEndpoints checks endpoint strings. minCount is 0 for a node and 1
// for a cluster. maxCount comes from params. Private addresses and userinfo
// are rejected: the registry holds public endpoints only (track A8, D1).
func ValidateEndpoints(endpoints []string, minCount int, maxCount uint32) error {
	if len(endpoints) < minCount {
		return fmt.Errorf("got %d endpoints, need at least %d", len(endpoints), minCount)
	}
	if uint32(len(endpoints)) > maxCount {
		return fmt.Errorf("got %d endpoints, max is %d", len(endpoints), maxCount)
	}
	seen := make(map[string]struct{}, len(endpoints))
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
		proto := parts[i]
		val := parts[i+1]
		switch proto {
		case "ip4", "ip6", "dns", "dns4", "dns6", "dnsaddr":
			hosts = append(hosts, val)
		case "tcp", "udp", "p2p", "sni":
		case "onion", "onion3":
			host := val
			if cut, _, ok := strings.Cut(val, ":"); ok {
				host = cut
			}
			hosts = append(hosts, host)
		default:
			return nil, fmt.Errorf("unsupported multiaddr protocol %q", proto)
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

// ValidateBaseDomain checks a cluster's public base domain.
func ValidateBaseDomain(domain string) error {
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
		if err := validateDomainLabel(label); err != nil {
			return err
		}
	}
	return nil
}

func validateDomainLabel(label string) error {
	if label == "" || len(label) > 63 {
		return fmt.Errorf("domain label %q has an invalid length", label)
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return fmt.Errorf("domain label %q must not start or end with a hyphen", label)
	}
	for _, c := range label {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf("domain label %q has a character outside [a-z0-9-]", label)
		}
	}
	return nil
}

// ValidateMetadataURI checks a cluster metadata URI. Empty is allowed.
func ValidateMetadataURI(uri string) error {
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

// PositiveAmount requires a positive norama integer.
func PositiveAmount(amount math.Int) error {
	if amount.IsNil() || !amount.IsPositive() {
		return fmt.Errorf("amount must be a positive integer, got %s", amount)
	}
	return nil
}
