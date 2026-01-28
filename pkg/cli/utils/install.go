package utils

import (
	"fmt"
	"strings"
)

// IPFSPeerInfo holds IPFS peer information for configuring Peering.Peers
type IPFSPeerInfo struct {
	PeerID string
	Addrs  []string
}

// IPFSClusterPeerInfo contains IPFS Cluster peer information for cluster discovery
type IPFSClusterPeerInfo struct {
	PeerID string
	Addrs  []string
}

// AnyoneRelayDryRunInfo contains Anyone relay info for dry-run summary
type AnyoneRelayDryRunInfo struct {
	Enabled  bool
	Exit     bool
	Nickname string
	Contact  string
	Wallet   string
	ORPort   int
}

// ShowDryRunSummary displays what would be done during installation without making changes
func ShowDryRunSummary(vpsIP, domain, branch string, peers []string, joinAddress string, isFirstNode bool, oramaDir string) {
	ShowDryRunSummaryWithRelay(vpsIP, domain, branch, peers, joinAddress, isFirstNode, oramaDir, nil)
}

// ShowDryRunSummaryWithRelay displays what would be done during installation with optional relay info
func ShowDryRunSummaryWithRelay(vpsIP, domain, branch string, peers []string, joinAddress string, isFirstNode bool, oramaDir string, relayInfo *AnyoneRelayDryRunInfo) {
	fmt.Print("\n" + strings.Repeat("=", 70) + "\n")
	fmt.Printf("DRY RUN - No changes will be made\n")
	fmt.Print(strings.Repeat("=", 70) + "\n\n")

	fmt.Printf("📋 Installation Summary:\n")
	fmt.Printf("  VPS IP:        %s\n", vpsIP)
	fmt.Printf("  Domain:        %s\n", domain)
	fmt.Printf("  Branch:        %s\n", branch)
	if isFirstNode {
		fmt.Printf("  Node Type:     First node (creates new cluster)\n")
	} else {
		fmt.Printf("  Node Type:     Joining existing cluster\n")
		if joinAddress != "" {
			fmt.Printf("  Join Address:  %s\n", joinAddress)
		}
		if len(peers) > 0 {
			fmt.Printf("  Peers:         %d peer(s)\n", len(peers))
			for _, peer := range peers {
				fmt.Printf("                 - %s\n", peer)
			}
		}
	}

	fmt.Printf("\n📁 Directories that would be created:\n")
	fmt.Printf("  %s/configs/\n", oramaDir)
	fmt.Printf("  %s/secrets/\n", oramaDir)
	fmt.Printf("  %s/data/ipfs/repo/\n", oramaDir)
	fmt.Printf("  %s/data/ipfs-cluster/\n", oramaDir)
	fmt.Printf("  %s/data/rqlite/\n", oramaDir)
	fmt.Printf("  %s/logs/\n", oramaDir)
	fmt.Printf("  %s/tls-cache/\n", oramaDir)

	fmt.Printf("\n🔧 Binaries that would be installed:\n")
	fmt.Printf("  - Go (if not present)\n")
	fmt.Printf("  - RQLite 8.43.0\n")
	fmt.Printf("  - IPFS/Kubo 0.38.2\n")
	fmt.Printf("  - IPFS Cluster (latest)\n")
	fmt.Printf("  - Olric 0.7.0\n")
	if relayInfo != nil && relayInfo.Enabled {
		fmt.Printf("  - anon (relay binary via apt)\n")
	} else {
		fmt.Printf("  - anyone-client (npm)\n")
	}
	fmt.Printf("  - DeBros binaries (built from %s branch)\n", branch)

	fmt.Printf("\n🔐 Secrets that would be generated:\n")
	fmt.Printf("  - Cluster secret (64-hex)\n")
	fmt.Printf("  - IPFS swarm key\n")
	fmt.Printf("  - Node identity (Ed25519 keypair)\n")

	fmt.Printf("\n📝 Configuration files that would be created:\n")
	fmt.Printf("  - %s/configs/node.yaml\n", oramaDir)
	fmt.Printf("  - %s/configs/olric/config.yaml\n", oramaDir)

	fmt.Printf("\n⚙️  Systemd services that would be created:\n")
	fmt.Printf("  - debros-ipfs.service\n")
	fmt.Printf("  - debros-ipfs-cluster.service\n")
	fmt.Printf("  - debros-olric.service\n")
	fmt.Printf("  - debros-node.service (includes embedded gateway + RQLite)\n")
	if relayInfo != nil && relayInfo.Enabled {
		fmt.Printf("  - debros-anyone-relay.service (relay operator mode)\n")
	} else {
		fmt.Printf("  - debros-anyone-client.service\n")
	}

	fmt.Printf("\n🌐 Ports that would be used:\n")
	fmt.Printf("  External (must be open in firewall):\n")
	fmt.Printf("    - 80   (HTTP for ACME/Let's Encrypt)\n")
	fmt.Printf("    - 443  (HTTPS gateway)\n")
	fmt.Printf("    - 4101 (IPFS swarm)\n")
	fmt.Printf("    - 7001 (RQLite Raft)\n")
	if relayInfo != nil && relayInfo.Enabled {
		fmt.Printf("    - %d  (Anyone ORPort - relay traffic)\n", relayInfo.ORPort)
	}
	fmt.Printf("  Internal (localhost only):\n")
	fmt.Printf("    - 4501 (IPFS API)\n")
	fmt.Printf("    - 5001 (RQLite HTTP)\n")
	fmt.Printf("    - 6001 (Unified gateway)\n")
	fmt.Printf("    - 8080 (IPFS gateway)\n")
	fmt.Printf("    - 9050 (Anyone SOCKS5)\n")
	fmt.Printf("    - 9094 (IPFS Cluster API)\n")
	fmt.Printf("    - 3320/3322 (Olric)\n")

	// Show relay-specific configuration
	if relayInfo != nil && relayInfo.Enabled {
		fmt.Printf("\n🔗 Anyone Relay Configuration:\n")
		fmt.Printf("  Mode:     Relay Operator\n")
		fmt.Printf("  Nickname: %s\n", relayInfo.Nickname)
		fmt.Printf("  Contact:  %s\n", relayInfo.Contact)
		fmt.Printf("  Wallet:   %s\n", relayInfo.Wallet)
		fmt.Printf("  ORPort:   %d\n", relayInfo.ORPort)
		if relayInfo.Exit {
			fmt.Printf("  Exit:     Yes (legal implications apply)\n")
		} else {
			fmt.Printf("  Exit:     No (non-exit relay)\n")
		}
		fmt.Printf("\n  ⚠️  IMPORTANT: You need 100 $ANYONE tokens in wallet to receive rewards\n")
		fmt.Printf("  Register at: https://dashboard.anyone.io\n")
	}

	fmt.Print("\n" + strings.Repeat("=", 70) + "\n")
	fmt.Printf("To proceed with installation, run without --dry-run\n")
	fmt.Print(strings.Repeat("=", 70) + "\n\n")
}
