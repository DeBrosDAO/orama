package validation

import (
	"fmt"
	"net"
	"strings"
)

// ValidateSNIDNSRecords checks if the required SNI DNS records exist
// It tries to resolve the key SNI hostnames for IPFS, IPFS Cluster, and Olric
// Note: Raft no longer uses SNI - it uses direct RQLite TLS on port 7002
// All should resolve to the same IP (the node's public IP or domain)
// Returns a warning string if records are missing (empty string if all OK)
func ValidateSNIDNSRecords(domain string) string {
	// List of SNI services that need DNS records
	// Note: raft.domain is NOT included - RQLite uses direct TLS on port 7002
	sniServices := []string{
		fmt.Sprintf("ipfs.%s", domain),
		fmt.Sprintf("ipfs-cluster.%s", domain),
		fmt.Sprintf("olric.%s", domain),
	}

	// Try to resolve the main domain first to get baseline
	mainIPs, err := net.LookupHost(domain)
	if err != nil {
		// Main domain doesn't resolve - this is just a warning now
		return fmt.Sprintf("Warning: could not resolve main domain %s: %v", domain, err)
	}

	if len(mainIPs) == 0 {
		return fmt.Sprintf("Warning: main domain %s resolved to no IP addresses", domain)
	}

	// Check each SNI service
	var unresolvedServices []string
	for _, service := range sniServices {
		ips, err := net.LookupHost(service)
		if err != nil || len(ips) == 0 {
			unresolvedServices = append(unresolvedServices, service)
		}
	}

	if len(unresolvedServices) > 0 {
		serviceList := strings.Join(unresolvedServices, ", ")
		return fmt.Sprintf(
			"⚠️  SNI DNS records not found for: %s\n"+
				"   For multi-node clustering, add wildcard CNAME: *.%s -> %s\n"+
				"   (Continuing anyway - single-node setup will work)",
			serviceList, domain, domain,
		)
	}

	return ""
}
