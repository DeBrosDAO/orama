package validate

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

const (
	maxZoneLen  = 253
	maxLabelLen = 63
)

var zoneLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// DNSConfig is the node.yaml dns block for validation purposes.
type DNSConfig struct {
	// NodeNamesZone is dns.node_names_zone.
	NodeNamesZone string
	// BaseDomain is http_gateway.base_domain: the zone this cluster's nameservers answer.
	BaseDomain string
}

// ValidateDNS checks the dns block. An empty node_names_zone is valid: the node does not publish
// node names. A zone must be a dedicated sub-zone strictly below this cluster's base domain: a name
// written under any other domain would sit in a registry whose nameservers are never asked for it,
// and a zone equal to the base domain would let a claimed name shadow the hostnames the cluster
// publishes there (a namespace gateway, push, a node).
func ValidateDNS(c DNSConfig) []error {
	if c.NodeNamesZone == "" {
		return nil
	}
	if err := ValidateZone(c.NodeNamesZone); err != nil {
		return []error{ValidationError{Path: "dns.node_names_zone", Message: err.Error()}}
	}
	if !ZoneServedBy(c.NodeNamesZone, c.BaseDomain) {
		return []error{ValidationError{
			Path:    "dns.node_names_zone",
			Message: fmt.Sprintf("%q is not a sub-zone below this cluster's base domain (http_gateway.base_domain is %q)", c.NodeNamesZone, c.BaseDomain),
			Hint:    "use a dedicated subdomain of the base domain, for example nodes.<base domain>, never the base domain itself; only the cluster whose nameservers answer the zone publishes its node names",
		}}
	}
	return nil
}

// ValidateZone checks a DNS zone: a lower-case domain of at least two labels, without a trailing
// dot, that is not an IP address.
func ValidateZone(zone string) error {
	if zone == "" || len(zone) > maxZoneLen {
		return fmt.Errorf("zone %q must be 1 to %d characters", zone, maxZoneLen)
	}
	if zone != strings.ToLower(zone) || strings.HasSuffix(zone, ".") {
		return fmt.Errorf("zone %q must be lower case, without a trailing dot", zone)
	}
	if net.ParseIP(zone) != nil {
		return fmt.Errorf("zone %q is an IP address, not a domain", zone)
	}
	labels := strings.Split(zone, ".")
	if len(labels) < 2 {
		return fmt.Errorf("zone %q must have at least two labels, for example stagenet.orama.network", zone)
	}
	for _, l := range labels {
		if len(l) > maxLabelLen || !zoneLabel.MatchString(l) {
			return fmt.Errorf("zone %q: label %q must be 1 to %d characters of a-z, 0-9 and '-', without a leading or trailing '-'", zone, l, maxLabelLen)
		}
	}
	return nil
}

// ZoneServedBy reports whether a cluster whose base domain is baseDomain answers zone as a
// dedicated sub-zone: the zone is strictly below the base domain, never equal to it.
func ZoneServedBy(zone, baseDomain string) bool {
	return baseDomain != "" && strings.HasSuffix(zone, "."+baseDomain)
}
