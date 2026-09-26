package installers

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// IPFSClusterInstaller handles IPFS Cluster Service installation
type IPFSClusterInstaller struct {
	*BaseInstaller
}

// NewIPFSClusterInstaller creates a new IPFS Cluster installer
func NewIPFSClusterInstaller(arch string, logWriter io.Writer) *IPFSClusterInstaller {
	return &IPFSClusterInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
	}
}

// InitializeConfig initializes IPFS Cluster configuration (unified - no bootstrap/node distinction)
// This runs `ipfs-cluster-service init` to create the service.json configuration file.
// For existing installations, it ensures the cluster secret is up to date.
// clusterPeers should be in format:
// ["/ip4/<wg-ip>/tcp/<constants.IPFSClusterSwarmPort>/p2p/<cluster-peer-id>"]
//
// swarmIP is this node's WireGuard address, where the cluster's
// peer-to-peer listener binds (see ClusterSwarmListenAddr).
//
// root is the anchor clusterPath lives under (rootfs): the cluster directory
// is the orama user's, so root writes it without following symlinks.
func (ici *IPFSClusterInstaller) InitializeConfig(root rootfs.Root, clusterPath, clusterSecret string, ipfsAPIPort int, swarmIP string, clusterPeers []string) error {
	if strings.TrimSpace(clusterSecret) == "" {
		return fmt.Errorf("CLUSTER_SECRET is empty; refusing to initialize IPFS Cluster")
	}
	listenAddr, err := ClusterSwarmListenAddr(swarmIP)
	if err != nil {
		return err
	}
	serviceJSONPath := filepath.Join(clusterPath, "service.json")
	configExists := false
	if _, err := os.Stat(serviceJSONPath); err == nil {
		configExists = true
		fmt.Fprintf(ici.logWriter, "    IPFS Cluster config already exists, ensuring it's up to date...\n")
	} else {
		fmt.Fprintf(ici.logWriter, "    Preparing IPFS Cluster path...\n")
	}

	if err := root.MkdirAll(clusterPath, 0755); err != nil {
		return fmt.Errorf("failed to create IPFS Cluster directory: %w", err)
	}

	// Resolve ipfs-cluster-service binary path
	clusterBinary, err := ResolveBinaryPath("ipfs-cluster-service", "/usr/local/bin/ipfs-cluster-service", "/usr/bin/ipfs-cluster-service")
	if err != nil {
		return fmt.Errorf("ipfs-cluster-service binary not found: %w", err)
	}

	// Initialize cluster config if it doesn't exist
	if !configExists {
		// ipfs-cluster-service init creates service.json with every section.
		// It runs as the orama user (runas.go): the directory is that user's,
		// and the tool resolves paths in it without pkg/rootfs.
		fmt.Fprintf(ici.logWriter, "    Initializing IPFS Cluster config...\n")
		if err := giveToServiceUser(root, clusterPath); err != nil {
			return err
		}
		env := []string{"IPFS_CLUSTER_PATH=" + clusterPath, "CLUSTER_SECRET=" + clusterSecret}
		if err := runAsServiceUser(env, clusterBinary, "init", "--force"); err != nil {
			return fmt.Errorf("failed to initialize IPFS Cluster config: %w", err)
		}
	}

	fmt.Fprintf(ici.logWriter, "    Updating cluster secret, listeners, IPFS port, and peer addresses...\n")
	if err := ici.updateConfig(root, clusterPath, clusterSecret, ipfsAPIPort, listenAddr, clusterPeers); err != nil {
		return fmt.Errorf("failed to update cluster config: %w", err)
	}

	if err := ici.verifySecret(root, clusterPath, clusterSecret); err != nil {
		return fmt.Errorf("cluster secret verification failed: %w", err)
	}
	fmt.Fprintf(ici.logWriter, "    ✓ Cluster secret verified\n")

	return nil
}

// updateConfig updates the secret, the listeners, the IPFS port, and the peer
// addresses in IPFS Cluster service.json. listenAddr is the peer-to-peer
// listener (ClusterSwarmListenAddr).
func (ici *IPFSClusterInstaller) updateConfig(root rootfs.Root, clusterPath, secret string, ipfsAPIPort int, listenAddr string, bootstrapClusterPeers []string) error {
	serviceJSONPath := filepath.Join(clusterPath, "service.json")

	// Read existing config
	data, err := root.ReadFile(serviceJSONPath, rootfs.SmallFileLimit)
	if err != nil {
		return fmt.Errorf("failed to read service.json: %w", err)
	}

	// Parse JSON
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse service.json: %w", err)
	}

	// Update cluster secret, listen_multiaddress, and peer addresses
	if cluster, ok := config["cluster"].(map[string]interface{}); ok {
		cluster["secret"] = secret
		cluster["listen_multiaddress"] = []interface{}{listenAddr}
		// Configure peer addresses for cluster discovery
		// This allows nodes to find and connect to each other
		// Merge new peers with existing peers (preserves manually configured peers)
		if len(bootstrapClusterPeers) > 0 {
			existingPeers := ici.extractExistingPeers(cluster)
			mergedPeers := ici.mergePeerAddresses(existingPeers, bootstrapClusterPeers)
			cluster["peer_addresses"] = mergedPeers
		}
		// If no new peers provided, preserve existing peer_addresses (don't overwrite)
	} else {
		clusterConfig := map[string]interface{}{
			"secret":              secret,
			"listen_multiaddress": []interface{}{listenAddr},
		}
		if len(bootstrapClusterPeers) > 0 {
			clusterConfig["peer_addresses"] = bootstrapClusterPeers
		}
		config["cluster"] = clusterConfig
	}

	// The IPFS proxy is an unauthenticated copy of the Kubo API that also
	// hijacks pin and unpin and runs them against the whole cluster. Loopback
	// does not keep a tenant's deployment off it. ipfs-cluster disables the
	// component when the section is absent, which is the only off switch it
	// has — an empty listen address is rejected, not a disable.
	if api, ok := config["api"].(map[string]interface{}); ok {
		delete(api, "ipfsproxy")
	}

	// ipfs-cluster v1.1.2 dials this address with no Authorization header, and
	// its config has no field for one (an unknown key is ignored). Kubo's RPC
	// refuses that. The cluster unit listens on the socket and forwards to
	// the RPC port below with the bearer; the connector dials the socket.
	if ipfsAPIPort != constants.IPFSAPIPort {
		return fmt.Errorf("ipfs API port %d is not %d; the cluster proxy forwards to that port", ipfsAPIPort, constants.IPFSAPIPort)
	}
	setKuboConnector(config, ipfs.KuboProxyMultiaddr)

	if err := bindClusterAPIsToLoopback(config); err != nil {
		return err
	}
	if err := requireClusterAPIAuth(config, secret); err != nil {
		return err
	}

	// Write back
	updatedData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal service.json: %w", err)
	}

	if err := root.WriteFile(serviceJSONPath, updatedData, serviceJSONMode); err != nil {
		return fmt.Errorf("failed to write service.json: %w", err)
	}

	return nil
}

// serviceJSONMode keeps service.json to the orama user: it holds the cluster
// secret and the REST API password. It was written 0644.
const serviceJSONMode = 0o600

// setKuboConnector points ipfs-cluster's ipfshttp connector at multiaddr,
// creating the section when init did not. The address is the unit's proxy
// socket, not Kubo's TCP port.
func setKuboConnector(config map[string]interface{}, multiaddr string) {
	conn, _ := config["ipfs_connector"].(map[string]interface{})
	if conn == nil {
		conn = map[string]interface{}{}
		config["ipfs_connector"] = conn
	}
	httpc, _ := conn["ipfshttp"].(map[string]interface{})
	if httpc == nil {
		httpc = map[string]interface{}{}
		conn["ipfshttp"] = httpc
	}
	httpc["node_multiaddress"] = multiaddr
}

// requireClusterAPIAuth makes the REST API — and the pinning-service API when
// the file configures one — require the basic-auth credentials every consumer
// derives from the cluster secret (ipfs.ClusterRESTPassword). Loopback was
// their only guard, and every process on the node is on loopback.
func requireClusterAPIAuth(config map[string]interface{}, clusterSecret string) error {
	password, err := ipfs.ClusterRESTPassword(clusterSecret)
	if err != nil {
		return err
	}
	credentials := map[string]interface{}{ipfs.ClusterRESTUser: password}
	api := config["api"].(map[string]interface{}) // bindClusterAPIsToLoopback created it
	api["restapi"].(map[string]interface{})["basic_auth_credentials"] = credentials
	if pinsvc, ok := api["pinsvcapi"].(map[string]interface{}); ok {
		pinsvc["basic_auth_credentials"] = credentials
	}
	return nil
}

// ClusterSwarmListenAddr is the cluster's peer-to-peer listener: this node's
// WireGuard address on constants.IPFSClusterSwarmPort.
//
// This is the one writer of that listener. orama-node used to rewrite it on
// every start to 0.0.0.0 on a port it derived from the REST API URL (10114)
// while install wrote 0.0.0.0:9100, so what a node listened on depended on
// which had run last, and the addresses peers were told to dial did not match
// it. Binding the WireGuard address keeps the swarm off the public interface
// by construction, as the Kubo swarm is: the firewall admits only the mesh,
// and no public rule opens this port.
func ClusterSwarmListenAddr(wgIP string) (string, error) {
	ip := net.ParseIP(wgIP).To4()
	if ip == nil || !constants.WireGuardOverlay().Contains(netip.AddrFrom4([4]byte(ip))) {
		return "", fmt.Errorf("the IPFS Cluster swarm binds this node's WireGuard address, and %q is not one inside %s",
			wgIP, constants.WireGuardSubnet)
	}
	return fmt.Sprintf("/ip4/%s/tcp/%d", ip, constants.IPFSClusterSwarmPort), nil
}

// loopbackIPv4 is where every IPFS and IPFS Cluster API binds: every consumer
// reaches them on localhost. Loopback keeps them off the network; it does not
// keep them from other processes on the node, which is why the cluster's
// REST API also requires credentials (requireClusterAPIAuth).
const loopbackIPv4 = "127.0.0.1"

// bindClusterAPIsToLoopback binds the cluster's HTTP APIs to loopback in a
// service.json: the REST API on constants.IPFSClusterAPIPort, where every
// consumer looks for it, and the pinning-service API on whatever port it
// has. The IPFS proxy is not bound: updateConfig deletes that section,
// because the proxy authenticates nobody. It runs on every install and
// upgrade, so a node whose REST API was bound to 0.0.0.0 is rebound. The
// cluster's own libp2p listener (cluster.listen_multiaddress) faces the
// peers and is not an API.
func bindClusterAPIsToLoopback(config map[string]interface{}) error {
	api, ok := config["api"].(map[string]interface{})
	if !ok {
		api = map[string]interface{}{}
		config["api"] = api
	}
	restapi, ok := api["restapi"].(map[string]interface{})
	if !ok {
		restapi = map[string]interface{}{}
		api["restapi"] = restapi
	}
	restapi["http_listen_multiaddress"] = fmt.Sprintf("/ip4/%s/tcp/%d", loopbackIPv4, constants.IPFSClusterAPIPort)

	for _, field := range []struct{ section, key string }{
		{"pinsvcapi", "http_listen_multiaddress"},
	} {
		section, ok := api[field.section].(map[string]interface{})
		if !ok {
			continue // absent: ipfs-cluster's default is loopback
		}
		addr, ok := section[field.key].(string)
		if !ok {
			continue
		}
		bound, err := loopbackListenAddr(addr)
		if err != nil {
			return fmt.Errorf("api.%s.%s: %w", field.section, field.key, err)
		}
		section[field.key] = bound
	}
	return nil
}

// loopbackListenAddr is a /ip4|ip6/<host>/tcp/<port> listen address moved to
// loopback, keeping its port.
func loopbackListenAddr(addr string) (string, error) {
	parts := strings.Split(addr, "/")
	if len(parts) != 5 || parts[0] != "" || (parts[1] != "ip4" && parts[1] != "ip6") || parts[3] != "tcp" {
		return "", fmt.Errorf("%q is not a /ip4/<host>/tcp/<port> listen address", addr)
	}
	port, err := strconv.Atoi(parts[4])
	if err != nil || port <= 0 || port > 65535 {
		return "", fmt.Errorf("%q has no valid TCP port", addr)
	}
	return fmt.Sprintf("/ip4/%s/tcp/%d", loopbackIPv4, port), nil
}

// extractExistingPeers extracts existing peer addresses from cluster config
func (ici *IPFSClusterInstaller) extractExistingPeers(cluster map[string]interface{}) []string {
	var peers []string
	if peerAddrs, ok := cluster["peer_addresses"].([]interface{}); ok {
		for _, addr := range peerAddrs {
			if addrStr, ok := addr.(string); ok && addrStr != "" {
				peers = append(peers, addrStr)
			}
		}
	}
	return peers
}

// mergePeerAddresses merges existing and new peer addresses, removing duplicates
func (ici *IPFSClusterInstaller) mergePeerAddresses(existing, new []string) []string {
	seen := make(map[string]bool)
	var merged []string

	// Add existing peers first
	for _, peer := range existing {
		if !seen[peer] {
			seen[peer] = true
			merged = append(merged, peer)
		}
	}

	// Add new peers (if not already present)
	for _, peer := range new {
		if !seen[peer] {
			seen[peer] = true
			merged = append(merged, peer)
		}
	}

	return merged
}

// verifySecret verifies that the secret in service.json matches the expected value
func (ici *IPFSClusterInstaller) verifySecret(root rootfs.Root, clusterPath, expectedSecret string) error {
	serviceJSONPath := filepath.Join(clusterPath, "service.json")

	data, err := root.ReadFile(serviceJSONPath, rootfs.SmallFileLimit)
	if err != nil {
		return fmt.Errorf("failed to read service.json for verification: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse service.json for verification: %w", err)
	}

	if cluster, ok := config["cluster"].(map[string]interface{}); ok {
		if secret, ok := cluster["secret"].(string); ok {
			if subtle.ConstantTimeCompare([]byte(secret), []byte(expectedSecret)) != 1 {
				return fmt.Errorf("secret mismatch in service.json")
			}
			return nil
		}
		return fmt.Errorf("secret not found in cluster config")
	}

	return fmt.Errorf("cluster section not found in service.json")
}
