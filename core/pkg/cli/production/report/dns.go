package report

import (
	"context"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// collectDNS gathers CoreDNS, Caddy, and DNS resolution health information.
// Only called when /etc/coredns exists.
func collectDNS() *DNSReport {
	r := &DNSReport{}

	// Set TLS days to -1 by default (failure state).
	r.BaseTLSDaysLeft = -1
	r.WildTLSDaysLeft = -1

	// 1. CoreDNSActive: systemctl is-active coredns
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "systemctl", "is-active", "coredns"); err == nil {
			r.CoreDNSActive = strings.TrimSpace(out) == "active"
		}
	}

	// 2. CaddyActive: systemctl is-active caddy
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "systemctl", "is-active", "caddy"); err == nil {
			r.CaddyActive = strings.TrimSpace(out) == "active"
		}
	}

	// 3. Port53Bound: check :53 in ss -ulnp
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ss", "-ulnp"); err == nil {
			r.Port53Bound = strings.Contains(out, ":53 ") || strings.Contains(out, ":53\t")
		}
	}

	// 4. Port80Bound and Port443Bound: check in ss -tlnp
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ss", "-tlnp"); err == nil {
			r.Port80Bound = strings.Contains(out, ":80 ") || strings.Contains(out, ":80\t")
			r.Port443Bound = strings.Contains(out, ":443 ") || strings.Contains(out, ":443\t")
		}
	}

	// 5. CoreDNSMemMB: ps -C coredns -o rss=
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ps", "-C", "coredns", "-o", "rss=", "--no-headers"); err == nil {
			line := strings.TrimSpace(out)
			if line != "" {
				first := strings.Fields(line)[0]
				if kb, err := strconv.Atoi(first); err == nil {
					r.CoreDNSMemMB = kb / 1024
				}
			}
		}
	}

	// 6. CoreDNSRestarts: systemctl show coredns --property=NRestarts
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "systemctl", "show", "coredns", "--property=NRestarts"); err == nil {
			props := parseProperties(out)
			r.CoreDNSRestarts = parseInt(props["NRestarts"])
		}
	}

	// 7. LogErrors: grep errors from coredns journal (last 5 min)
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "bash", "-c",
			`journalctl -u coredns --no-pager -n 100 --since "5 min ago" 2>/dev/null | grep -ciE "(error|ERR)" || echo 0`); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				r.LogErrors = n
			}
		}
	}

	// 8. CorefileExists: check /etc/coredns/Corefile
	if _, err := os.Stat("/etc/coredns/Corefile"); err == nil {
		r.CorefileExists = true
	}

	// Parse domain from Corefile for DNS resolution tests.
	domain := parseDomain()
	if domain == "" {
		return r
	}

	// 9. SOAResolves: dig SOA
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "dig", "@127.0.0.1", "SOA", domain, "+short", "+time=2"); err == nil {
			r.SOAResolves = strings.TrimSpace(out) != ""
		}
	}

	// 10. NSResolves and NSRecordCount: dig NS
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "dig", "@127.0.0.1", "NS", domain, "+short", "+time=2"); err == nil {
			out = strings.TrimSpace(out)
			if out != "" {
				r.NSResolves = true
				lines := strings.Split(out, "\n")
				count := 0
				for _, l := range lines {
					if strings.TrimSpace(l) != "" {
						count++
					}
				}
				r.NSRecordCount = count
			}
		}
	}

	// 11. WildcardResolves: dig A test.<domain>
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "dig", "@127.0.0.1", "A", "test."+domain, "+short", "+time=2"); err == nil {
			r.WildcardResolves = strings.TrimSpace(out) != ""
		}
	}

	// 12. BaseAResolves: dig A <domain>
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "dig", "@127.0.0.1", "A", domain, "+short", "+time=2"); err == nil {
			r.BaseAResolves = strings.TrimSpace(out) != ""
		}
	}

	// 13. BaseTLSDaysLeft: check TLS cert expiry for base domain
	r.BaseTLSDaysLeft = checkTLSDaysLeft(domain, domain)

	// 14. WildTLSDaysLeft: check TLS cert expiry for wildcard
	r.WildTLSDaysLeft = checkTLSDaysLeft("*."+domain, domain)

	return r
}

// parseDomain reads /etc/coredns/Corefile and extracts the base domain.
// It looks for zone block declarations like "example.com {" or "*.example.com {"
// and returns the base domain (without wildcard prefix).
func parseDomain() string {
	data, err := os.ReadFile("/etc/coredns/Corefile")
	if err != nil {
		return ""
	}

	content := string(data)

	// Look for domain patterns in the Corefile.
	// Common patterns:
	//   example.com {
	//   *.example.com {
	//   example.com:53 {
	// We want to find a real domain, not "." (root zone).
	domainRe := regexp.MustCompile(`(?m)^\s*\*?\.?([a-zA-Z0-9][-a-zA-Z0-9]*\.[a-zA-Z0-9][-a-zA-Z0-9.]*[a-zA-Z])(?::\d+)?\s*\{`)
	matches := domainRe.FindStringSubmatch(content)
	if len(matches) >= 2 {
		return matches[1]
	}

	// Fallback: look for any line that looks like a domain block declaration.
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Strip trailing "{" and port suffix.
		line = strings.TrimSuffix(line, "{")
		line = strings.TrimSpace(line)

		// Remove port if present.
		if idx := strings.LastIndex(line, ":"); idx > 0 {
			if _, err := strconv.Atoi(line[idx+1:]); err == nil {
				line = line[:idx]
			}
		}

		// Strip wildcard prefix.
		line = strings.TrimPrefix(line, "*.")

		// Check if it looks like a domain (has at least one dot and no spaces).
		if strings.Contains(line, ".") && !strings.Contains(line, " ") && line != "." {
			return strings.TrimSpace(line)
		}
	}

	return ""
}

// checkTLSDaysLeft uses openssl to check the TLS certificate expiry date
// for a given servername connecting to localhost:443.
// Returns days until expiry, or -1 on any failure.
func checkTLSDaysLeft(servername, domain string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	cmd := `echo | openssl s_client -servername ` + servername + ` -connect localhost:443 2>/dev/null | openssl x509 -noout -enddate 2>/dev/null`
	out, err := runCmd(ctx, "bash", "-c", cmd)
	if err != nil {
		return -1
	}

	// Output looks like: "notAfter=Mar 15 12:00:00 2025 GMT"
	out = strings.TrimSpace(out)
	if !strings.HasPrefix(out, "notAfter=") {
		return -1
	}

	dateStr := strings.TrimPrefix(out, "notAfter=")
	dateStr = strings.TrimSpace(dateStr)

	// Parse the date. OpenSSL uses the format: "Jan  2 15:04:05 2006 GMT"
	layouts := []string{
		"Jan 2 15:04:05 2006 GMT",
		"Jan  2 15:04:05 2006 GMT",
		"Jan 02 15:04:05 2006 GMT",
	}

	for _, layout := range layouts {
		t, err := time.Parse(layout, dateStr)
		if err == nil {
			days := int(math.Floor(time.Until(t).Hours() / 24))
			return days
		}
	}

	return -1
}
