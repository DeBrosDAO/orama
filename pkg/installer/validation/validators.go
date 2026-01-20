package validation

import (
	"fmt"
	"net"
	"regexp"
)

// ValidateIP validates an IP address
func ValidateIP(ip string) error {
	if ip == "" {
		return fmt.Errorf("IP address is required")
	}
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP address format")
	}
	return nil
}

// ValidateDomain validates a domain name
func ValidateDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("domain is required")
	}
	// Basic domain validation
	domainRegex := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*$`)
	if !domainRegex.MatchString(domain) {
		return fmt.Errorf("invalid domain format")
	}
	return nil
}

// ValidateClusterSecret validates a cluster secret (64 hex characters)
func ValidateClusterSecret(secret string) error {
	if len(secret) != 64 {
		return fmt.Errorf("cluster secret must be 64 hex characters")
	}
	secretRegex := regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
	if !secretRegex.MatchString(secret) {
		return fmt.Errorf("cluster secret must be valid hexadecimal")
	}
	return nil
}

// DetectPublicIP attempts to detect the server's public IP address
func DetectPublicIP() string {
	// Try to detect public IP from common interfaces
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil && !ipnet.IP.IsPrivate() {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}
