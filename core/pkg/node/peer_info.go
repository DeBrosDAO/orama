package node

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// writePeerInfo records this node's dialable multiaddr for the CLI.
//
// It lives with the libp2p component rather than in main so that the file is
// written as soon as the host exists, instead of only on the one path where
// start-up ran to completion.
func (n *Node) writePeerInfo() error {
	peerID := n.GetPeerID()
	if peerID == "" {
		return fmt.Errorf("libp2p host has no peer ID yet")
	}

	dataDir, err := config.ExpandPath(n.config.Node.DataDir)
	if err != nil {
		return fmt.Errorf("failed to expand data directory path: %w", err)
	}

	addr := fmt.Sprintf("/%s/%s/tcp/%d/p2p/%s",
		advertiseIPProtocol(n.advertiseIP()), n.advertiseIP(), n.p2pPort(), peerID)

	path := filepath.Join(dataDir, "peer.info")
	if err := os.WriteFile(path, []byte(addr), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	n.logger.ComponentInfo(logging.ComponentNode, "Peer info saved",
		zap.String("path", path), zap.String("multiaddr", addr))
	return nil
}

// defaultP2PPort is the libp2p listen port assumed when the configured listen
// address carries none.
const defaultP2PPort = 4001

// p2pPort extracts the TCP port from the first configured listen multiaddr.
func (n *Node) p2pPort() int {
	if len(n.config.Node.ListenAddresses) == 0 {
		return defaultP2PPort
	}
	parts := strings.Split(n.config.Node.ListenAddresses[0], "/")
	for i, part := range parts {
		if part == "tcp" && i+1 < len(parts) {
			if port, err := strconv.Atoi(parts[i+1]); err == nil {
				return port
			}
		}
	}
	return defaultP2PPort
}

// advertiseIP is the address peers should dial, taken from the same config the
// raft transport advertises.
func (n *Node) advertiseIP() string {
	for _, addr := range []string{n.config.Discovery.HttpAdvAddress, n.config.Discovery.RaftAdvAddress} {
		if addr == "" {
			continue
		}
		host, _, err := net.SplitHostPort(addr)
		if err == nil && host != "" && host != "localhost" {
			return host
		}
	}
	return "0.0.0.0"
}

// advertiseIPProtocol returns the multiaddr protocol name for ip.
func advertiseIPProtocol(ip string) string {
	if parsed := net.ParseIP(ip); parsed != nil && parsed.To4() == nil {
		return "ip6"
	}
	return "ip4"
}
