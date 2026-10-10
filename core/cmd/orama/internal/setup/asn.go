package setup

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

// cymruZone answers "which autonomous system announces this address" over DNS:
// a TXT query for <reversed octets>.origin.asn.cymru.com.
const cymruZone = "origin.asn.cymru.com"

// lookupTXT is net.DefaultResolver.LookupTXT; a test replaces it.
var lookupTXT = net.DefaultResolver.LookupTXT

// OriginASN is the autonomous system number that announces ip, from Team Cymru's
// IP-to-ASN DNS service. The chain cannot verify the number a node declares; this
// is the number the operator's provider announces the address under. An address
// announced by several systems gets the first the service lists; --asn overrides.
func OriginASN(ctx context.Context, ip string) (uint32, error) {
	parsed := net.ParseIP(ip).To4()
	if parsed == nil {
		return 0, fmt.Errorf("%q is not an IPv4 address", ip)
	}
	name := fmt.Sprintf("%d.%d.%d.%d.%s", parsed[3], parsed[2], parsed[1], parsed[0], cymruZone)
	records, err := lookupTXT(ctx, name)
	if err != nil {
		return 0, fmt.Errorf("ask %s: %w", cymruZone, err)
	}
	if len(records) == 0 {
		return 0, fmt.Errorf("%s has no origin AS record", ip)
	}
	return parseCymru(records[0])
}

// parseCymru reads "15169 | 8.8.8.0/24 | US | arin | 1992-12-01": the first
// field, which may list several numbers, and checks the chain would accept it.
func parseCymru(record string) (uint32, error) {
	field, _, _ := strings.Cut(record, "|")
	numbers := strings.Fields(field)
	if len(numbers) == 0 {
		return 0, fmt.Errorf("the origin record %q names no AS number", record)
	}
	n, err := strconv.ParseUint(numbers[0], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("the origin record %q: AS number %q: %w", record, numbers[0], err)
	}
	if err := clusterreg.ValidateASN(uint32(n)); err != nil {
		return 0, fmt.Errorf("the origin record %q: %w", record, err)
	}
	return uint32(n), nil
}
