package gateway

import (
	"net"
	"strings"
)

// Where the anonymous relay may go (bugboard #266).
//
// A destination is a host the relay dials by name through Tor. The check and the
// dial must see the same string, and that string must mean the same thing to the
// check as to the resolver at the exit, so only plain ASCII letters, digits and
// hyphens are accepted: no case folding that can turn a non-ASCII letter into an
// ASCII one (U+212A KELVIN SIGN lowercases to "k"), no NUL, no port or userinfo
// separators, no IP literal.

const (
	// maxRelayHostLength and maxRelayLabelLength are the DNS limits.
	maxRelayHostLength  = 253
	maxRelayLabelLength = 63
)

// normalizeRelayHost returns host lowercased with at most one trailing dot
// removed, and whether it is an LDH hostname that is not an IP literal.
func normalizeRelayHost(host string) (string, bool) {
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > maxRelayHostLength {
		return "", false
	}
	b := []byte(host)
	for i, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
			b[i] = c + ('a' - 'A')
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '.':
		default:
			return "", false
		}
	}
	host = string(b)
	if net.ParseIP(host) != nil {
		return "", false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > maxRelayLabelLength || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
	}
	return host, true
}

// hostAllowed reports whether host, already normalized, is one of the suffixes
// or a name under one. The comparison is on whole labels: "evil-base.example" is
// not under "base.example", and neither is "ns-x.base.example.attacker.tld".
func (s *relayService) hostAllowed(host string) bool {
	for _, suffix := range s.suffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

// target validates the destination a client asked for and returns it in the form
// that is dialled: the normalized host, exactly as it was checked.
func (s *relayService) target(rawHost, rawPort string) (tunnelTarget, bool) {
	host, ok := normalizeRelayHost(rawHost)
	if !ok || !s.hostAllowed(host) {
		return tunnelTarget{}, false
	}
	if rawPort != "" && rawPort != "443" {
		return tunnelTarget{}, false
	}
	return tunnelTarget{host: host, port: relayPort}, true
}

// normalizeRelaySuffixes is the allowlist in the form hostAllowed compares. A
// suffix that is not a hostname never matches, and config validation has
// already refused it at start.
func normalizeRelaySuffixes(suffixes []string) []string {
	out := make([]string, 0, len(suffixes))
	for _, suffix := range suffixes {
		if s, ok := normalizeRelayHost(suffix); ok {
			out = append(out, s)
		}
	}
	return out
}
