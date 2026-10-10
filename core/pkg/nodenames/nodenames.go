// Package nodenames serves node identification names in DNS.
//
// An operator claims a name for one of its nodes on the chain (x/nodes MsgClaimNodeName). The name
// is a DNS label, and <name>.<zone> is one A or AAAA record per literal IP among the node's
// endpoints: identification only, with no NS and no glue. The cluster whose nameservers answer
// <zone> pages the chain's NodeNames query and keeps its dns_records in step (Syncer), so what the
// zone answers is always what the chain holds and can never drift from it.
package nodenames

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/netguard"
)

const (
	// RecordNamespace tags the dns_records rows this package owns. It deletes only rows it tagged,
	// so it can never remove a system, namespace or deployment record.
	RecordNamespace = "node-names"

	// RecordTTL is the TTL of a name's records, the same as the cluster's other records: a name
	// that moves is followed within five minutes.
	RecordTTL = 300

	// MinNameLen and MaxNameLen are the chain's bounds on a name (x/nodes types.MinNameLen/MaxNameLen).
	MinNameLen = 3
	MaxNameLen = 32

	punycode = "xn--"
)

var (
	namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	// zoneInfra matches the labels the zone publishes itself: nameservers and seeds. The chain
	// reserves them; the sync refuses them again so a bad chain state can never add an address to a
	// nameserver's glue.
	zoneInfra = regexp.MustCompile(`^(seed|ns)[0-9]*$`)
)

// Named is one claimed name, as the chain reports it: the node it identifies and the literal IPs
// among that node's endpoints.
type Named struct {
	Name   string
	NodeID string
	IPs    []string
}

// Record is one DNS record the zone answers for a name.
type Record struct {
	// FQDN is lower case with a trailing dot, the form dns_records stores.
	FQDN string
	// Type is "A" or "AAAA".
	Type  string
	Value string
}

// Refusal is a name or address the sync did not publish, and why.
type Refusal struct {
	Name   string
	IP     string
	Reason string
}

func (r Refusal) String() string {
	if r.IP == "" {
		return fmt.Sprintf("name %q: %s", r.Name, r.Reason)
	}
	return fmt.Sprintf("name %q address %q: %s", r.Name, r.IP, r.Reason)
}

// ValidateName checks the label of a name the way the chain does, minus its reserved list, and
// refuses the labels the zone publishes itself.
func ValidateName(name string) error {
	switch {
	case len(name) < MinNameLen || len(name) > MaxNameLen:
		return fmt.Errorf("must be %d to %d characters", MinNameLen, MaxNameLen)
	case !namePattern.MatchString(name):
		return fmt.Errorf("must use only a-z, 0-9 and '-', without a leading or trailing '-'")
	case strings.HasPrefix(name, punycode):
		return fmt.Errorf("must not start with %q", punycode)
	case zoneInfra.MatchString(name):
		return fmt.Errorf("is a nameserver or seed label the zone publishes itself")
	}
	return nil
}

// Desired is the set of records the zone must answer for entries, sorted by name and address, and
// the names and addresses it refused. A name with no publishable address has no record. Only a
// literal public IP is published: an address in a private, loopback, link-local or otherwise
// reserved range (netguard.Reserved) is refused, since a name that resolves there would aim every
// client of the zone at the client's own network.
func Desired(entries []Named, zone string) ([]Record, []Refusal) {
	var records []Record
	var refused []Refusal
	seen := map[Record]struct{}{}
	for _, e := range entries {
		if err := ValidateName(e.Name); err != nil {
			refused = append(refused, Refusal{Name: e.Name, Reason: err.Error()})
			continue
		}
		fqdn := e.Name + "." + zone + "."
		for _, raw := range e.IPs {
			rec, reason := recordFor(fqdn, raw)
			if reason != "" {
				refused = append(refused, Refusal{Name: e.Name, IP: raw, Reason: reason})
				continue
			}
			if _, dup := seen[rec]; dup {
				continue
			}
			seen[rec] = struct{}{}
			records = append(records, rec)
		}
	}
	return records, refused
}

func recordFor(fqdn, raw string) (Record, string) {
	ip := net.ParseIP(raw)
	if ip == nil {
		return Record{}, "is not a literal IP address"
	}
	if netguard.Reserved(ip) {
		return Record{}, "is in a private or reserved range"
	}
	if v4 := ip.To4(); v4 != nil {
		return Record{FQDN: fqdn, Type: "A", Value: v4.String()}, ""
	}
	return Record{FQDN: fqdn, Type: "AAAA", Value: ip.String()}, ""
}
