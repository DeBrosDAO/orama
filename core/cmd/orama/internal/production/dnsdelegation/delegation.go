// Package dnsdelegation prints the records an operator creates at the parent
// zone so the internet reaches a cluster's nameservers.
//
// Nameserver slots (ns1, ns2, …) are claimed at run time by whichever
// --nameserver node gets there first, so which address holds which name is
// only known to the cluster. The records at the registrar or parent zone have
// to match it exactly; this reads them from the cluster instead of asking the
// operator to guess.
package dnsdelegation

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/noderesolver"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/clusterops"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
)

// Nameserver is one claimed slot: nsN.<domain> served at IP.
type Nameserver struct {
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
}

// Delegation is everything the parent zone needs for one domain.
type Delegation struct {
	Domain      string       `json:"domain"`
	Nameservers []Nameserver `json:"nameservers"`
}

// Query selects every claimed slot whose glue record exists. A slot without
// glue is not published by the cluster's own zone, so it is not delegated to.
const Query = `SELECT ns.domain, ns.hostname, ns.ip_address FROM dns_nameservers ns
 WHERE EXISTS (SELECT 1 FROM dns_records g
                WHERE g.fqdn = ns.hostname||'.'||ns.domain||'.'
                  AND g.record_type = 'A' AND g.value = ns.ip_address AND g.is_active = TRUE)`

// slotHostname is what the cluster writes into dns_nameservers.hostname.
var slotHostname = regexp.MustCompile(`^ns[1-9][0-9]*$`)

// dnsName is a hostname of letter-digit-hyphen labels, at least two of them —
// the shape pkg/invite accepts for a server name.
var dnsName = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// Read returns the environment's delegations, read over SSH from its first
// node (every node holds the same registry).
func Read(env string) ([]Delegation, error) {
	nodes, err := noderesolver.ResolveNodes(env)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes found for environment %q (register them with `orama node setup`)", env)
	}
	node := nodes[0]
	cleanup, err := remotessh.PrepareNodeKeys([]inspector.Node{node})
	if err != nil {
		return nil, err
	}
	defer cleanup()

	body, err := clusterops.QuerySQL(node, Query)
	if err != nil {
		return nil, fmt.Errorf("read the nameserver slots from %s: %w", node.Host, err)
	}
	rows, err := clusterops.Rows(body)
	if err != nil {
		return nil, fmt.Errorf("read the nameserver slots from %s: %w", node.Host, err)
	}
	return FromRows(rows)
}

// FromRows validates the (domain, hostname, ip) rows and groups them by
// domain, slots in numeric order. What it returns is printed as records the
// operator types into a registrar, so a row that is not exactly what the
// cluster writes is refused rather than printed.
func FromRows(rows [][]any) ([]Delegation, error) {
	byDomain := map[string][]Nameserver{}
	for _, row := range rows {
		if len(row) < 3 {
			return nil, fmt.Errorf("unexpected dns_nameservers row shape: %v", row)
		}
		domain := strings.TrimSuffix(clusterops.AsString(row[0]), ".")
		ns := Nameserver{Hostname: clusterops.AsString(row[1]), IP: clusterops.AsString(row[2])}
		if !slotHostname.MatchString(ns.Hostname) {
			return nil, fmt.Errorf("nameserver slot %q for %s is not an nsN name", ns.Hostname, domain)
		}
		if err := install.ValidatePublicIP(ns.IP); err != nil {
			return nil, fmt.Errorf("nameserver %s.%s: %w", ns.Hostname, domain, err)
		}
		ns.IP = net.ParseIP(ns.IP).To4().String()
		if !dnsName.MatchString(domain) || net.ParseIP(domain) != nil {
			return nil, fmt.Errorf("nameserver %s has domain %q, which is not a DNS name", ns.Hostname, domain)
		}
		byDomain[domain] = append(byDomain[domain], ns)
	}

	out := make([]Delegation, 0, len(byDomain))
	for domain, nss := range byDomain {
		sort.Slice(nss, func(i, j int) bool { return slotIndex(nss[i].Hostname) < slotIndex(nss[j].Hostname) })
		out = append(out, Delegation{Domain: domain, Nameservers: nss})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

// slotIndex is N of nsN; the hostname was validated by FromRows.
func slotIndex(hostname string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(hostname, "ns"))
	return n
}

// ParentZone is the zone the delegation records go in: the domain without its
// first label. For a registered domain itself (example.com) that is the TLD,
// run by the registry — the records then go in at the registrar.
func ParentZone(domain string) string {
	_, parent, _ := strings.Cut(domain, ".")
	return parent
}

// Records renders d as zone-file lines: the NS records, then their glue.
func Records(d Delegation) []string {
	apex := d.Domain + "."
	var lines []string
	for _, ns := range d.Nameservers {
		lines = append(lines, fmt.Sprintf("%s\tIN\tNS\t%s.%s", apex, ns.Hostname, apex))
	}
	for _, ns := range d.Nameservers {
		lines = append(lines, fmt.Sprintf("%s.%s\tIN\tA\t%s", ns.Hostname, apex, ns.IP))
	}
	return lines
}
