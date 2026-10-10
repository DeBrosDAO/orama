package tornet

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

const (
	// summaryRejectCutoff is the number of IPv4 addresses a port may be refused
	// for before Tor's exit policy summary lists the port as refused: two /8
	// blocks (REJECT_CUTOFF_COUNT_IPV4 in Tor's src/core/or/policies.c).
	summaryRejectCutoff = uint64(1) << 25
	// summaryPorts is the size of a per-port table indexed by port number.
	summaryPorts = maxPort + 2
)

// summaryIgnored are the refusals Tor's summary does not count: the ranges
// ExitPolicyRejectPrivate expands to (private_nets in policies.c, IPv4 part). A
// rule counts as one of them only when its address and mask are exactly these.
var summaryIgnored = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"),
}

var errNoSummaryPort = errors.New("the refused destinations cover more than two /8 blocks of addresses on every port, so Tor's exit policy summary lists every port as refused: the authorities would publish `reject 1-65535` for this exit and no client would build a path through it")

// checkExitSummary reports whether the authorities' summary of policy (the
// lines ExitPolicyLines renders) accepts at least one port, by the rule of
// Tor's policy_summarize: a port is accepted when the `accept *:*` rule covers
// it and the rules refusing it, other than the private ranges, name no more
// than summaryRejectCutoff addresses. The summary is what clients choose exits
// by (the `p` line of the consensus); an exit it refuses on every port is never used.
func checkExitSummary(policy []string) error {
	var refused [summaryPorts]uint64
	var accepted [summaryPorts]bool
	for _, line := range policy {
		verb, rule, ok := strings.Cut(strings.TrimPrefix(line, "ExitPolicy "), " ")
		host, ports, hasPorts := strings.Cut(rule, ":")
		if !ok || !hasPorts {
			continue
		}
		lo, hi := 1, maxPort
		if ports != "*" {
			var err error
			if lo, hi, err = portRange(ports); err != nil {
				return fmt.Errorf("exit policy line %q: %w", line, err)
			}
			if lo < 1 || hi > maxPort || lo > hi {
				return fmt.Errorf("exit policy line %q: ports %d-%d are not within 1-%d", line, lo, hi, maxPort)
			}
		}
		switch verb {
		case "reject":
			count, counted := summaryAddresses(host)
			for p := lo; counted && p <= hi; p++ {
				refused[p] += count
			}
		case "accept":
			if host != "*" {
				continue
			}
			for p := lo; p <= hi; p++ {
				accepted[p] = accepted[p] || refused[p] <= summaryRejectCutoff
			}
		}
	}
	for p := 1; p <= maxPort; p++ {
		if accepted[p] {
			return nil
		}
	}
	return errNoSummaryPort
}

// summaryAddresses is how many addresses a rule's address part refuses and
// whether the summary counts it.
func summaryAddresses(host string) (uint64, bool) {
	if host == "*" {
		return 1 << 32, true
	}
	p, err := netip.ParsePrefix(host)
	if err != nil {
		a, aerr := netip.ParseAddr(host)
		if aerr != nil {
			return 0, false
		}
		p = netip.PrefixFrom(a, 32)
	}
	for _, ignored := range summaryIgnored {
		if p == ignored {
			return 0, false
		}
	}
	return uint64(1) << (32 - p.Bits()), true
}

// portRange reads the port part of a rule ExitPolicyLines renders: a port or lo-hi.
func portRange(s string) (int, int, error) {
	lo, hi, isRange := strings.Cut(s, "-")
	l, err := strconv.Atoi(lo)
	if err != nil {
		return 0, 0, err
	}
	h := l
	if isRange {
		if h, err = strconv.Atoi(hi); err != nil {
			return 0, 0, err
		}
	}
	return l, h, nil
}
