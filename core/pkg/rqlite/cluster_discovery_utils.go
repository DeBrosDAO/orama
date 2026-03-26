package rqlite

import (
	"net"
	"net/netip"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/discovery"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"
)

// adjustPeerAdvertisedAddresses adjusts peer metadata addresses
func (c *ClusterDiscoveryService) adjustPeerAdvertisedAddresses(peerID peer.ID, meta *discovery.RQLiteNodeMetadata) (bool, string) {
	ip := c.selectPeerIP(peerID)
	if ip == "" {
		return false, ""
	}

	changed, stale := rewriteAdvertisedAddresses(meta, ip, true)
	if changed {
		c.logger.Debug("Addresses normalized",
			zap.String("peer", shortPeerID(peerID)),
			zap.String("raft", meta.RaftAddress),
			zap.String("http_address", meta.HTTPAddress))
	}
	return changed, stale
}

// adjustSelfAdvertisedAddresses adjusts our own metadata addresses
func (c *ClusterDiscoveryService) adjustSelfAdvertisedAddresses(meta *discovery.RQLiteNodeMetadata) bool {
	ip := c.selectSelfIP()
	if ip == "" {
		return false
	}

	changed, _ := rewriteAdvertisedAddresses(meta, ip, true)
	if !changed {
		return false
	}

	c.mu.Lock()
	c.raftAddress = meta.RaftAddress
	c.httpAddress = meta.HTTPAddress
	c.mu.Unlock()

	if c.rqliteManager != nil {
		c.rqliteManager.UpdateAdvertisedAddresses(meta.RaftAddress, meta.HTTPAddress)
	}

	return true
}

// selectPeerIP selects the best IP address for a peer
func (c *ClusterDiscoveryService) selectPeerIP(peerID peer.ID) string {
	var fallback string

	for _, conn := range c.host.Network().ConnsToPeer(peerID) {
		if ip, public := ipFromMultiaddr(conn.RemoteMultiaddr()); ip != "" {
			if shouldReplaceHost(ip) {
				continue
			}
			if public {
				return ip
			}
			if fallback == "" {
				fallback = ip
			}
		}
	}

	for _, addr := range c.host.Peerstore().Addrs(peerID) {
		if ip, public := ipFromMultiaddr(addr); ip != "" {
			if shouldReplaceHost(ip) {
				continue
			}
			if public {
				return ip
			}
			if fallback == "" {
				fallback = ip
			}
		}
	}

	return fallback
}

// selectSelfIP selects the best IP address for ourselves
func (c *ClusterDiscoveryService) selectSelfIP() string {
	var fallback string

	for _, addr := range c.host.Addrs() {
		if ip, public := ipFromMultiaddr(addr); ip != "" {
			if shouldReplaceHost(ip) {
				continue
			}
			if public {
				return ip
			}
			if fallback == "" {
				fallback = ip
			}
		}
	}

	return fallback
}

// rewriteAdvertisedAddresses rewrites RaftAddress and HTTPAddress in metadata
func rewriteAdvertisedAddresses(meta *discovery.RQLiteNodeMetadata, newHost string, allowNodeIDRewrite bool) (bool, string) {
	if meta == nil || newHost == "" {
		return false, ""
	}

	originalNodeID := meta.NodeID
	changed := false
	nodeIDChanged := false

	if newAddr, replaced := replaceAddressHost(meta.RaftAddress, newHost); replaced {
		if meta.RaftAddress != newAddr {
			meta.RaftAddress = newAddr
			changed = true
		}
	}

	if newAddr, replaced := replaceAddressHost(meta.HTTPAddress, newHost); replaced {
		if meta.HTTPAddress != newAddr {
			meta.HTTPAddress = newAddr
			changed = true
		}
	}

	if allowNodeIDRewrite {
		if meta.RaftAddress != "" && (meta.NodeID == "" || meta.NodeID == originalNodeID || shouldReplaceHost(hostFromAddress(meta.NodeID))) {
			if meta.NodeID != meta.RaftAddress {
				meta.NodeID = meta.RaftAddress
				nodeIDChanged = meta.NodeID != originalNodeID
				if nodeIDChanged {
					changed = true
				}
			}
		}
	}

	if nodeIDChanged {
		return changed, originalNodeID
	}
	return changed, ""
}

// replaceAddressHost replaces the host part of an address
func replaceAddressHost(address, newHost string) (string, bool) {
	if address == "" || newHost == "" {
		return address, false
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return address, false
	}

	if !shouldReplaceHost(host) {
		return address, false
	}

	return net.JoinHostPort(newHost, port), true
}

// shouldReplaceHost returns true if the host should be replaced
func shouldReplaceHost(host string) bool {
	if host == "" {
		return true
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.IsLoopback() || addr.IsUnspecified() {
			return true
		}
	}

	return false
}

// hostFromAddress extracts the host part from a host:port address
func hostFromAddress(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	return host
}

// ipFromMultiaddr extracts an IP address from a multiaddr and returns (ip, isPublic)
func ipFromMultiaddr(addr multiaddr.Multiaddr) (string, bool) {
	if addr == nil {
		return "", false
	}

	if v4, err := addr.ValueForProtocol(multiaddr.P_IP4); err == nil {
		return v4, isPublicIP(v4)
	}
	if v6, err := addr.ValueForProtocol(multiaddr.P_IP6); err == nil {
		return v6, isPublicIP(v6)
	}
	return "", false
}

// isPublicIP returns true if the IP is a public address
func isPublicIP(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	if addr.IsLoopback() || addr.IsUnspecified() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsPrivate() {
		return false
	}
	return true
}

// shortPeerID returns a shortened version of a peer ID
func shortPeerID(id peer.ID) string {
	s := id.String()
	if len(s) <= 8 {
		return s
	}
	return s[:8] + "..."
}

