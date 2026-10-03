package report

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	// coreDNSUnit and caddyUnit are the nameserver's DNS and TLS front end.
	// They run from the namespace templates; the host units coredns.service
	// and caddy.service they replaced are retired by the index migration, so
	// asking systemd about those names reports a live nameserver as down.
	coreDNSUnit = CoreDNSUnit
	caddyUnit   = "orama-namespace-caddy@index"

	// corefilePath is CoreDNS's configuration, which names the zone served.
	corefilePath = "/etc/coredns/Corefile"

	// dnsLogWindow is how far back CoreDNS's journal is scanned for errors.
	dnsLogWindow = "5 min ago"
)

// collectDNS gathers CoreDNS, Caddy, and DNS resolution health information.
// Only called when /etc/coredns exists.
func collectDNS() *DNSReport {
	r := &DNSReport{BaseTLSDaysLeft: -1, WildTLSDaysLeft: -1}
	ctx, cancel := context.WithTimeout(context.Background(), dnsCollectTimeout)
	defer cancel()

	r.CoreDNSActive = unitActive(ctx, coreDNSUnit)
	r.CaddyActive = unitActive(ctx, caddyUnit)
	collectDNSPorts(ctx, r)
	collectCoreDNSProcess(ctx, r)
	if _, err := os.Stat(corefilePath); err == nil {
		r.CorefileExists = true
	}

	domain := parseDomain()
	if domain == "" {
		return r
	}
	probeZone(ctx, r, domain)
	r.BaseTLSDaysLeft, r.BaseTLSExpired = tlsDaysLeft(ctx, domain)
	r.WildTLSDaysLeft, r.WildTLSExpired = tlsDaysLeft(ctx, wildcardProbeLabel+"."+domain)
	return r
}

// unitActive reports whether systemd says unit is active. is-active exits
// non-zero for every other state, so its output, not its exit code, is read.
func unitActive(ctx context.Context, unit string) bool {
	out, _ := runCmd(ctx, "systemctl", "is-active", unit)
	return strings.TrimSpace(out) == "active"
}

func collectDNSPorts(ctx context.Context, r *DNSReport) {
	if out, err := runCmd(ctx, "ss", "-ulnp"); err == nil {
		r.Port53Bound = strings.Contains(out, ":53 ") || strings.Contains(out, ":53\t")
	}
	if out, err := runCmd(ctx, "ss", "-tlnp"); err == nil {
		r.Port80Bound = strings.Contains(out, ":80 ") || strings.Contains(out, ":80\t")
		r.Port443Bound = strings.Contains(out, ":443 ") || strings.Contains(out, ":443\t")
	}
}

func collectCoreDNSProcess(ctx context.Context, r *DNSReport) {
	if out, err := runCmd(ctx, "ps", "-C", "coredns", "-o", "rss=", "--no-headers"); err == nil {
		if fields := strings.Fields(out); len(fields) > 0 {
			if kb, err := strconv.Atoi(fields[0]); err == nil {
				r.CoreDNSMemMB = kb / 1024
			}
		}
	}
	if out, err := runCmd(ctx, "systemctl", "show", coreDNSUnit, "--property=NRestarts"); err == nil {
		r.CoreDNSRestarts = parseInt(parseProperties(out)["NRestarts"])
	}
	if out, err := runCmd(ctx, "journalctl", "-u", coreDNSUnit, "--no-pager", "-n", "100",
		"--since", dnsLogWindow, "-o", "cat"); err == nil {
		r.LogErrors = countErrorLines(out)
	}
}

// countErrorLines counts journal lines that mention an error.
func countErrorLines(out string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if l := strings.ToLower(line); strings.Contains(l, "error") || strings.Contains(line, "ERR") {
			n++
		}
	}
	return n
}

// domainRe matches a zone block declaration: "example.com {", "*.example.com {",
// "example.com:53 {". The root zone "." never matches.
var domainRe = regexp.MustCompile(`(?m)^\s*\*?\.?([a-zA-Z0-9][-a-zA-Z0-9]*\.[a-zA-Z0-9][-a-zA-Z0-9.]*[a-zA-Z])(?::\d+)?\s*\{`)

// parseDomain reads the Corefile and returns the zone it serves, "" when it
// names none.
func parseDomain() string {
	data, err := os.ReadFile(corefilePath)
	if err != nil {
		return ""
	}
	return domainFromCorefile(string(data))
}

func domainFromCorefile(content string) string {
	if m := domainRe.FindStringSubmatch(content); len(m) >= 2 {
		return m[1]
	}
	return ""
}

// CoreDNSUnit is the nameserver's DNS unit. It runs only on nameservers, and
// every node's report lists it among the core services.
const CoreDNSUnit = "orama-namespace-coredns@nameserver"
