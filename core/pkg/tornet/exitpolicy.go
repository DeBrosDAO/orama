package tornet

import (
	"bufio"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/netguard"
)

// exitRejectPorts are the ports an Orama exit refuses, in Tor's ExitPolicy
// port syntax. They are the abuse-prone services: outbound mail (SMTP 25 and
// the submission ports 465 and 587), and the file-sharing and Windows-sharing
// ports Tor's own default policy refuses.
var exitRejectPorts = []string{
	"25", "465", "587", "119", "135-139", "445", "563", "1214",
	"4661-4666", "6346-6429", "6699", "6881-6999",
}

// exitPolicyUnroutable are the reserved IPv4 ranges no TCP connection can
// reach: multicast, and the reserved class E block with the limited broadcast.
// They are left out of the exit policy on purpose. The authorities summarise
// an exit's policy into the `p` line clients choose exits by, and Tor lists a
// port as refused there when the refusals for it cover more than two /8 blocks
// of addresses (see exitSummaryAccepts). These two are sixteen /8 blocks each:
// listed, they made every port "refused", the authorities published
// "reject 1-65535" for the exit and no client would build a path through it.
// Tor's own default policy does not list them either.
var exitPolicyUnroutable = []string{"224.0.0.0/4", "240.0.0.0/4"}

// ExitPolicyLines is the whole exit policy of an exit relay, in torrc order:
// the operator's own refusals first (Tor takes the first rule that matches),
// then every reserved IPv4 range an exit can be asked to reach, then the
// refused ports, then accept the rest. IPv6 is not exited (IPv6Exit 0 is set
// beside it).
//
// The reserved ranges are the shared netguard list, which holds the ones Tor
// does not reject by default and an exit must: 100.64.0.0/10 and the
// 198.18.0.0/15 range the co-located namespace's host end lives in. The
// unroutable ones (exitPolicyUnroutable) are not in it.
func ExitPolicyLines(operatorReject []string) []string {
	var lines []string
	for _, r := range operatorReject {
		lines = append(lines, "ExitPolicy reject "+r)
	}
	for _, cidr := range netguard.Ranges {
		if p := netip.MustParsePrefix(cidr); p.Addr().Is4() && !slices.Contains(exitPolicyUnroutable, cidr) {
			lines = append(lines, "ExitPolicy reject "+cidr+":*")
		}
	}
	for _, port := range exitRejectPorts {
		lines = append(lines, "ExitPolicy reject *:"+port)
	}
	return append(lines, "ExitPolicy accept *:*")
}

// ParseExitRejectList reads an exit operator's list of refused destinations:
// one IPv4 address or CIDR per line, optionally followed by :port or :lo-hi
// (all ports when absent), with blank lines and # comments ignored. The result
// is in torrc syntax. Every line is checked, because it is written into a torrc:
// a line that is not a destination would be a torrc directive.
func ParseExitRejectList(data string) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "#")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rule, err := exitRejectRule(line)
		if err != nil {
			return nil, fmt.Errorf("exit reject list line %d (%q): %w", n, line, err)
		}
		out = append(out, rule)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read the exit reject list: %w", err)
	}
	return out, nil
}

func exitRejectRule(line string) (string, error) {
	host, ports, hasPort := strings.Cut(line, ":")
	if !hasPort {
		ports = "*"
	}
	if strings.Contains(host, "/") {
		p, err := netip.ParsePrefix(host)
		if err != nil || !p.Addr().Is4() {
			return "", fmt.Errorf("%q is not an IPv4 CIDR", host)
		}
		host = p.Masked().String()
	} else {
		a, err := netip.ParseAddr(host)
		if err != nil || !a.Is4() {
			return "", fmt.Errorf("%q is not an IPv4 address", host)
		}
		host = a.String()
	}
	if err := checkPortSpec(ports); err != nil {
		return "", err
	}
	return host + ":" + ports, nil
}

// checkPortSpec accepts *, a port, or lo-hi.
func checkPortSpec(s string) error {
	if s == "*" {
		return nil
	}
	lo, hi, isRange := strings.Cut(s, "-")
	l, errLo := strconv.Atoi(lo)
	h := l
	var errHi error
	if isRange {
		h, errHi = strconv.Atoi(hi)
	}
	if errLo != nil || errHi != nil || l < 1 || h > maxPort || l > h || strconv.Itoa(l) != lo || (isRange && strconv.Itoa(h) != hi) {
		return fmt.Errorf("%q is not a port, a port range or *", s)
	}
	return nil
}
