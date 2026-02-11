package namespace

import (
	"fmt"
	"net"
)

// getWireGuardIP returns the IPv4 address of the wg0 interface.
// Used as a fallback when Olric BindAddr is empty or 0.0.0.0.
func getWireGuardIP() (string, error) {
	iface, err := net.InterfaceByName("wg0")
	if err != nil {
		return "", fmt.Errorf("wg0 interface not found: %w", err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("failed to get wg0 addresses: %w", err)
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			return ipnet.IP.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address on wg0")
}
