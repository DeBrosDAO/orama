package upgrade

import (
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	oramainstall "github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// nodeConfigPath is this node's node.yaml.
func (o *Orchestrator) nodeConfigPath() string {
	return filepath.Join(o.oramaDir, "configs", "node.yaml")
}

// readNodeConfig reads node.yaml, which belongs to the orama user, without
// following a symlink and only up to a size limit.
func (o *Orchestrator) readNodeConfig() ([]byte, error) {
	return oramainstall.OramaRoot(o.oramaDir).ReadFile(o.nodeConfigPath(), rootfs.SmallFileLimit)
}

func (o *Orchestrator) extractPeers() []string {
	var peers []string
	if data, err := o.readNodeConfig(); err == nil {
		configStr := string(data)
		inPeersList := false
		for _, line := range strings.Split(configStr, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "bootstrap_peers:") || strings.HasPrefix(trimmed, "peers:") {
				inPeersList = true
				continue
			}
			if inPeersList {
				if strings.HasPrefix(trimmed, "-") {
					// Extract multiaddr after the dash
					parts := strings.SplitN(trimmed, "-", 2)
					if len(parts) > 1 {
						peer := strings.TrimSpace(parts[1])
						peer = strings.Trim(peer, "\"'")
						if peer != "" && strings.HasPrefix(peer, "/") {
							peers = append(peers, peer)
						}
					}
				} else if trimmed == "" || !strings.HasPrefix(trimmed, "-") {
					// End of peers list
					break
				}
			}
		}
	}
	return peers
}

// Loopback advertise addresses mean "not yet configured with a real IP", so
// extractNetworkConfig must not mistake them for the node's VPS address.
var (
	localRQLiteHTTPAddr = net.JoinHostPort("localhost", strconv.Itoa(constants.RQLiteHTTPPort))
	localRQLiteRaftAddr = net.JoinHostPort("localhost", strconv.Itoa(constants.RQLiteRaftPort))
)

func (o *Orchestrator) extractNetworkConfig() (vpsIP, joinAddress string) {
	if data, err := o.readNodeConfig(); err == nil {
		configStr := string(data)
		for _, line := range strings.Split(configStr, "\n") {
			trimmed := strings.TrimSpace(line)
			// Try to extract VPS IP from http_adv_address or raft_adv_address
			if vpsIP == "" && (strings.HasPrefix(trimmed, "http_adv_address:") || strings.HasPrefix(trimmed, "raft_adv_address:")) {
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) > 1 {
					addr := strings.TrimSpace(parts[1])
					addr = strings.Trim(addr, "\"'")
					if addr != "" && addr != "null" && addr != localRQLiteHTTPAddr && addr != localRQLiteRaftAddr {
						// Extract IP from address (format: "IP:PORT" or "[IPv6]:PORT")
						if host, _, err := net.SplitHostPort(addr); err == nil && host != "" && host != "localhost" {
							vpsIP = host
						}
					}
				}
			}
			// Extract join address
			if strings.HasPrefix(trimmed, "rqlite_join_address:") {
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) > 1 {
					joinAddress = strings.TrimSpace(parts[1])
					joinAddress = strings.Trim(joinAddress, "\"'")
					if joinAddress == "null" || joinAddress == "" {
						joinAddress = ""
					}
				}
			}
		}
	}
	return vpsIP, joinAddress
}

func (o *Orchestrator) extractGatewayConfig() (enableHTTPS bool, domain string, baseDomain string) {
	// enableHTTPS is always false: public TLS is Caddy. A leftover domain:
	// line in node.yaml must not re-enable gateway autocert.
	if data, err := o.readNodeConfig(); err == nil {
		configStr := string(data)
		for _, line := range strings.Split(configStr, "\n") {
			trimmed := strings.TrimSpace(line)
			if domain == "" && strings.HasPrefix(trimmed, "domain:") && !strings.HasPrefix(trimmed, "domain_") {
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) > 1 {
					d := strings.TrimSpace(parts[1])
					d = strings.Trim(d, "\"'")
					if d != "" && d != "null" {
						domain = d
					}
				}
			}
			if strings.HasPrefix(trimmed, "base_domain:") {
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) > 1 {
					baseDomain = strings.TrimSpace(parts[1])
					baseDomain = strings.Trim(baseDomain, "\"'")
					if baseDomain == "null" || baseDomain == "" {
						baseDomain = ""
					}
				}
			}
		}
	}

	return false, domain, baseDomain
}

func (o *Orchestrator) regenerateConfigs() error {
	peers := o.extractPeers()
	vpsIP, joinAddress := o.extractNetworkConfig()
	enableHTTPS, domain, baseDomain := o.extractGatewayConfig()

	fmt.Printf("  Preserving existing configuration:\n")
	if len(peers) > 0 {
		fmt.Printf("    - Peers: %d peer(s) preserved\n", len(peers))
	}
	if vpsIP != "" {
		fmt.Printf("    - VPS IP: %s\n", vpsIP)
	}
	if domain != "" {
		fmt.Printf("    - Domain: %s\n", domain)
	}
	if baseDomain != "" {
		fmt.Printf("    - Base domain: %s\n", baseDomain)
	}
	if joinAddress != "" {
		fmt.Printf("    - Join address: %s\n", joinAddress)
	}

	// node.public_ip is what `orama node invite` builds the join URL from.
	// The pre-stop step resolved it (and handed it to this process with
	// --public-ip after a re-exec); this records it for Phase 4.
	if err := o.resolveAndSetPublicIP(); err != nil {
		return err
	}

	// Phase 4: Generate configs.
	//
	// Fatal. "Existing configs preserved" was a warning describing an upgrade
	// that did not upgrade anything: the node restarts onto the new binary with
	// the old config, which is the combination that has to work and the one
	// least likely to have been tested.
	return o.setup.Phase4GenerateConfigs(peers, vpsIP, enableHTTPS, domain, baseDomain, joinAddress)
}
