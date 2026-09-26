package upgrade

import (
	"fmt"
	"net"

	oramainstall "github.com/DeBrosOfficial/network/pkg/install"
)

// routeProbeAddr is any public address. Connecting a UDP socket to it sends
// nothing; it makes the kernel pick the source address of the default route,
// which is the address orama-node registers for itself in dns_nodes
// (Node.getNodeIPAddress) and so the one the cluster already knows it by.
const routeProbeAddr = "8.8.8.8:80"

// resolvePublicIP decides node.public_ip for this upgrade: the operator's
// --public-ip, else the address node.yaml records, else the source address of
// the default route. Each is checked to be a public IPv4 address — node.yaml
// is written by the orama user and is not trusted as-is — and a node with none
// fails the upgrade: `orama node invite` cannot mint a join URL without it.
func resolvePublicIP(flagIP string, recorded func() (string, error), detect func() (net.IP, error)) (string, error) {
	if flagIP != "" {
		if err := oramainstall.ValidatePublicIP(flagIP); err != nil {
			return "", fmt.Errorf("--public-ip: %w", err)
		}
		return flagIP, nil
	}
	current, err := recorded()
	if err != nil {
		return "", fmt.Errorf("read node.public_ip: %w", err)
	}
	if current != "" {
		if err := oramainstall.ValidatePublicIP(current); err != nil {
			return "", fmt.Errorf("node.yaml records node.public_ip %q, which is not usable (%v); re-run with --public-ip <this node's public IP>", current, err)
		}
		return current, nil
	}
	ip, err := detect()
	if err != nil {
		return "", fmt.Errorf("node.yaml records no node.public_ip and it could not be detected (%v); re-run with --public-ip <this node's public IP>", err)
	}
	if err := oramainstall.ValidatePublicIP(ip.String()); err != nil {
		return "", fmt.Errorf("node.yaml records no node.public_ip and the default route's source address %s is not public (%v) — "+
			"a node behind NAT has to be told: re-run with --public-ip <this node's public IP>", ip, err)
	}
	return ip.String(), nil
}

// routeSourceIP is the source address of this host's default route.
func routeSourceIP() (net.IP, error) {
	conn, err := net.Dial("udp", routeProbeAddr)
	if err != nil {
		return nil, fmt.Errorf("find the default route's source address: %w", err)
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return nil, fmt.Errorf("the default route's local address is a %T, not UDP", conn.LocalAddr())
	}
	return addr.IP, nil
}
