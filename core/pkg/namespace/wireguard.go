package namespace

import "github.com/DeBrosOfficial/network/pkg/wireguard"

// getWireGuardIP returns the IPv4 address of the wg0 interface.
// Used as a fallback when Olric BindAddr is empty or 0.0.0.0.
func getWireGuardIP() (string, error) {
	return wireguard.GetIP()
}
