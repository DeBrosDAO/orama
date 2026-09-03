package checks

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func init() {
	inspector.RegisterChecker("dns", CheckDNS)
}

const dnsSub = "dns"

// CheckDNS runs all DNS/CoreDNS health checks against cluster data.
func CheckDNS(data *inspector.ClusterData) []inspector.CheckResult {
	var results []inspector.CheckResult

	for _, nd := range data.Nodes {
		if nd.DNS == nil {
			continue
		}
		results = append(results, checkDNSPerNode(nd)...)
	}

	results = append(results, checkDNSCrossNode(data)...)

	return results
}

func checkDNSPerNode(nd *inspector.NodeData) []inspector.CheckResult {
	var r []inspector.CheckResult
	dns := nd.DNS
	node := nd.Node.Name()

	// 4.1 CoreDNS service running
	if dns.CoreDNSActive {
		r = append(r, inspector.Pass("dns.coredns_active", "CoreDNS service active", dnsSub, node,
			"orama-namespace-coredns@nameserver is active", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("dns.coredns_active", "CoreDNS service active", dnsSub, node,
			"orama-namespace-coredns@nameserver is not active", inspector.Critical))
		return r
	}

	// 4.47 Caddy service running
	if dns.CaddyActive {
		r = append(r, inspector.Pass("dns.caddy_active", "Caddy service active", dnsSub, node,
			"caddy is active", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("dns.caddy_active", "Caddy service active", dnsSub, node,
			"caddy is not active", inspector.Critical))
	}

	// 4.8 DNS port 53 bound
	if dns.Port53Bound {
		r = append(r, inspector.Pass("dns.port_53", "DNS port 53 bound", dnsSub, node,
			"UDP 53 is listening", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("dns.port_53", "DNS port 53 bound", dnsSub, node,
			"UDP 53 is NOT listening", inspector.Critical))
	}

	// 4.10 HTTP port 80
	if dns.Port80Bound {
		r = append(r, inspector.Pass("dns.port_80", "HTTP port 80 bound", dnsSub, node,
			"TCP 80 is listening", inspector.High))
	} else {
		r = append(r, inspector.Warn("dns.port_80", "HTTP port 80 bound", dnsSub, node,
			"TCP 80 is NOT listening", inspector.High))
	}

	// 4.11 HTTPS port 443
	if dns.Port443Bound {
		r = append(r, inspector.Pass("dns.port_443", "HTTPS port 443 bound", dnsSub, node,
			"TCP 443 is listening", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("dns.port_443", "HTTPS port 443 bound", dnsSub, node,
			"TCP 443 is NOT listening", inspector.Critical))
	}

	// 4.3 CoreDNS memory
	if dns.CoreDNSMemMB > 0 {
		if dns.CoreDNSMemMB < 100 {
			r = append(r, inspector.Pass("dns.coredns_memory", "CoreDNS memory healthy", dnsSub, node,
				fmt.Sprintf("RSS=%dMB", dns.CoreDNSMemMB), inspector.Medium))
		} else if dns.CoreDNSMemMB < 200 {
			r = append(r, inspector.Warn("dns.coredns_memory", "CoreDNS memory healthy", dnsSub, node,
				fmt.Sprintf("RSS=%dMB (elevated)", dns.CoreDNSMemMB), inspector.Medium))
		} else {
			r = append(r, inspector.Fail("dns.coredns_memory", "CoreDNS memory healthy", dnsSub, node,
				fmt.Sprintf("RSS=%dMB (high)", dns.CoreDNSMemMB), inspector.High))
		}
	}

	// 4.4 CoreDNS restart count
	if dns.CoreDNSRestarts == 0 {
		r = append(r, inspector.Pass("dns.coredns_restarts", "CoreDNS low restart count", dnsSub, node,
			"NRestarts=0", inspector.High))
	} else if dns.CoreDNSRestarts <= 3 {
		r = append(r, inspector.Warn("dns.coredns_restarts", "CoreDNS low restart count", dnsSub, node,
			fmt.Sprintf("NRestarts=%d", dns.CoreDNSRestarts), inspector.High))
	} else {
		r = append(r, inspector.Fail("dns.coredns_restarts", "CoreDNS low restart count", dnsSub, node,
			fmt.Sprintf("NRestarts=%d (crash-looping?)", dns.CoreDNSRestarts), inspector.High))
	}

	// 4.7 CoreDNS log error rate
	if dns.LogErrors == 0 {
		r = append(r, inspector.Pass("dns.coredns_log_errors", "No recent CoreDNS errors", dnsSub, node,
			"0 errors in last 5 minutes", inspector.High))
	} else if dns.LogErrors < 5 {
		r = append(r, inspector.Warn("dns.coredns_log_errors", "No recent CoreDNS errors", dnsSub, node,
			fmt.Sprintf("%d errors in last 5 minutes", dns.LogErrors), inspector.High))
	} else {
		r = append(r, inspector.Fail("dns.coredns_log_errors", "No recent CoreDNS errors", dnsSub, node,
			fmt.Sprintf("%d errors in last 5 minutes", dns.LogErrors), inspector.High))
	}

	// 4.14 Corefile exists
	if dns.CorefileExists {
		r = append(r, inspector.Pass("dns.corefile_exists", "Corefile exists", dnsSub, node,
			"/etc/coredns/Corefile present", inspector.High))
	} else {
		r = append(r, inspector.Fail("dns.corefile_exists", "Corefile exists", dnsSub, node,
			"/etc/coredns/Corefile NOT found", inspector.High))
	}

	// 4.20 SOA resolution
	if dns.SOAResolves {
		r = append(r, inspector.Pass("dns.soa_resolves", "SOA record resolves", dnsSub, node,
			"dig SOA returned result", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("dns.soa_resolves", "SOA record resolves", dnsSub, node,
			"dig SOA returned no result", inspector.Critical))
	}

	// 4.21 NS records resolve
	if dns.NSResolves {
		r = append(r, inspector.Pass("dns.ns_resolves", "NS records resolve", dnsSub, node,
			fmt.Sprintf("%d NS records returned", dns.NSRecordCount), inspector.Critical))
	} else {
		r = append(r, inspector.Fail("dns.ns_resolves", "NS records resolve", dnsSub, node,
			"dig NS returned no results", inspector.Critical))
	}

	// 4.23 Wildcard DNS resolution
	if dns.WildcardResolves {
		r = append(r, inspector.Pass("dns.wildcard_resolves", "Wildcard DNS resolves", dnsSub, node,
			"test-wildcard.<domain> returned IP", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("dns.wildcard_resolves", "Wildcard DNS resolves", dnsSub, node,
			"test-wildcard.<domain> returned no IP", inspector.Critical))
	}

	// 4.24 Base domain A record
	if dns.BaseAResolves {
		r = append(r, inspector.Pass("dns.base_a_resolves", "Base domain A record resolves", dnsSub, node,
			"<domain> A record returned IP", inspector.High))
	} else {
		r = append(r, inspector.Warn("dns.base_a_resolves", "Base domain A record resolves", dnsSub, node,
			"<domain> A record returned no IP", inspector.High))
	}

	// 4.50 TLS certificate - base domain
	if dns.BaseTLSDaysLeft >= 0 {
		if dns.BaseTLSDaysLeft > 30 {
			r = append(r, inspector.Pass("dns.tls_base", "Base domain TLS cert valid", dnsSub, node,
				fmt.Sprintf("%d days until expiry", dns.BaseTLSDaysLeft), inspector.Critical))
		} else if dns.BaseTLSDaysLeft > 7 {
			r = append(r, inspector.Warn("dns.tls_base", "Base domain TLS cert valid", dnsSub, node,
				fmt.Sprintf("%d days until expiry (expiring soon)", dns.BaseTLSDaysLeft), inspector.Critical))
		} else {
			r = append(r, inspector.Fail("dns.tls_base", "Base domain TLS cert valid", dnsSub, node,
				fmt.Sprintf("%d days until expiry (CRITICAL)", dns.BaseTLSDaysLeft), inspector.Critical))
		}
	}

	// 4.51 TLS certificate - wildcard
	if dns.WildTLSDaysLeft >= 0 {
		if dns.WildTLSDaysLeft > 30 {
			r = append(r, inspector.Pass("dns.tls_wildcard", "Wildcard TLS cert valid", dnsSub, node,
				fmt.Sprintf("%d days until expiry", dns.WildTLSDaysLeft), inspector.Critical))
		} else if dns.WildTLSDaysLeft > 7 {
			r = append(r, inspector.Warn("dns.tls_wildcard", "Wildcard TLS cert valid", dnsSub, node,
				fmt.Sprintf("%d days until expiry (expiring soon)", dns.WildTLSDaysLeft), inspector.Critical))
		} else {
			r = append(r, inspector.Fail("dns.tls_wildcard", "Wildcard TLS cert valid", dnsSub, node,
				fmt.Sprintf("%d days until expiry (CRITICAL)", dns.WildTLSDaysLeft), inspector.Critical))
		}
	}

	return r
}

func checkDNSCrossNode(data *inspector.ClusterData) []inspector.CheckResult {
	var r []inspector.CheckResult

	activeCount := 0
	totalNS := 0
	for _, nd := range data.Nodes {
		if nd.DNS == nil {
			continue
		}
		totalNS++
		if nd.DNS.CoreDNSActive {
			activeCount++
		}
	}

	if totalNS == 0 {
		return r
	}

	if activeCount == totalNS {
		r = append(r, inspector.Pass("dns.all_ns_active", "All nameservers running CoreDNS", dnsSub, "",
			fmt.Sprintf("%d/%d nameservers active", activeCount, totalNS), inspector.Critical))
	} else {
		r = append(r, inspector.Fail("dns.all_ns_active", "All nameservers running CoreDNS", dnsSub, "",
			fmt.Sprintf("%d/%d nameservers active", activeCount, totalNS), inspector.Critical))
	}

	return r
}
