package development

import "fmt"

// NodeSpec defines configuration for a single dev environment node
type NodeSpec struct {
	Name              string // node-1, node-2, node-3, node-4, node-5
	ConfigFilename    string // node-1.yaml, node-2.yaml, etc.
	DataDir           string // relative path from .orama root
	P2PPort           int    // LibP2P listen port
	IPFSAPIPort       int    // IPFS API port
	IPFSSwarmPort     int    // IPFS Swarm port
	IPFSGatewayPort   int    // IPFS HTTP Gateway port
	RQLiteHTTPPort    int    // RQLite HTTP API port
	RQLiteRaftPort    int    // RQLite Raft consensus port
	ClusterAPIPort    int    // IPFS Cluster REST API port
	ClusterPort       int    // IPFS Cluster P2P port
	UnifiedGatewayPort int   // Unified gateway port (proxies all services)
	RQLiteJoinTarget  string // which node's RQLite Raft port to join (empty for first node)
	ClusterJoinTarget string // which node's cluster to join (empty for first node)
}

// Topology defines the complete development environment topology
type Topology struct {
	Nodes           []NodeSpec
	GatewayPort     int
	OlricHTTPPort   int
	OlricMemberPort int
	AnonSOCKSPort   int
}

// DefaultTopology returns the default five-node dev environment topology
func DefaultTopology() *Topology {
	return &Topology{
		Nodes: []NodeSpec{
		{
			Name:              "node-1",
			ConfigFilename:    "node-1.yaml",
			DataDir:           "node-1",
			P2PPort:           4001,
			IPFSAPIPort:       4501,
			IPFSSwarmPort:     4101,
			IPFSGatewayPort:   7501,
			RQLiteHTTPPort:    5001,
			RQLiteRaftPort:    7001,
			ClusterAPIPort:    9094,
			ClusterPort:       9096,
			UnifiedGatewayPort: 6001,
			RQLiteJoinTarget:  "",       // First node - creates cluster
			ClusterJoinTarget: "",
		},
		{
			Name:              "node-2",
			ConfigFilename:    "node-2.yaml",
			DataDir:           "node-2",
			P2PPort:           4011,
			IPFSAPIPort:       4511,
			IPFSSwarmPort:     4111,
			IPFSGatewayPort:   7511,
			RQLiteHTTPPort:    5011,
			RQLiteRaftPort:    7011,
			ClusterAPIPort:    9104,
			ClusterPort:       9106,
			UnifiedGatewayPort: 6002,
			RQLiteJoinTarget:  "localhost:7001",
			ClusterJoinTarget: "localhost:9096",
		},
		{
			Name:              "node-3",
			ConfigFilename:    "node-3.yaml",
			DataDir:           "node-3",
			P2PPort:           4002,
			IPFSAPIPort:       4502,
			IPFSSwarmPort:     4102,
			IPFSGatewayPort:   7502,
			RQLiteHTTPPort:    5002,
			RQLiteRaftPort:    7002,
			ClusterAPIPort:    9114,
			ClusterPort:       9116,
			UnifiedGatewayPort: 6003,
			RQLiteJoinTarget:  "localhost:7001",
			ClusterJoinTarget: "localhost:9096",
		},
		{
			Name:              "node-4",
			ConfigFilename:    "node-4.yaml",
			DataDir:           "node-4",
			P2PPort:           4003,
			IPFSAPIPort:       4503,
			IPFSSwarmPort:     4103,
			IPFSGatewayPort:   7503,
			RQLiteHTTPPort:    5003,
			RQLiteRaftPort:    7003,
			ClusterAPIPort:    9124,
			ClusterPort:       9126,
			UnifiedGatewayPort: 6004,
			RQLiteJoinTarget:  "localhost:7001",
			ClusterJoinTarget: "localhost:9096",
		},
		{
			Name:              "node-5",
			ConfigFilename:    "node-5.yaml",
			DataDir:           "node-5",
			P2PPort:           4004,
			IPFSAPIPort:       4504,
			IPFSSwarmPort:     4104,
			IPFSGatewayPort:   7504,
			RQLiteHTTPPort:    5004,
			RQLiteRaftPort:    7004,
			ClusterAPIPort:    9134,
			ClusterPort:       9136,
			UnifiedGatewayPort: 6005,
			RQLiteJoinTarget:  "localhost:7001",
			ClusterJoinTarget: "localhost:9096",
		},
		},
		GatewayPort:     6000,  // Main gateway on 6000 (nodes use 6001-6005)
		OlricHTTPPort:   3320,
		OlricMemberPort: 3322,
		AnonSOCKSPort:   9050,
	}
}

// AllPorts returns a slice of all ports used in the topology
func (t *Topology) AllPorts() []int {
	var ports []int

	// Node-specific ports
	for _, node := range t.Nodes {
		ports = append(ports,
			node.P2PPort,
			node.IPFSAPIPort,
			node.IPFSSwarmPort,
			node.IPFSGatewayPort,
			node.RQLiteHTTPPort,
			node.RQLiteRaftPort,
			node.ClusterAPIPort,
			node.ClusterPort,
			node.UnifiedGatewayPort,
		)
	}

	// Shared service ports
	ports = append(ports,
		t.GatewayPort,
		t.OlricHTTPPort,
		t.OlricMemberPort,
		t.AnonSOCKSPort,
	)

	return ports
}

// PortMap returns a human-readable mapping of ports to services
func (t *Topology) PortMap() map[int]string {
	portMap := make(map[int]string)

	for _, node := range t.Nodes {
		portMap[node.P2PPort] = fmt.Sprintf("%s P2P", node.Name)
		portMap[node.IPFSAPIPort] = fmt.Sprintf("%s IPFS API", node.Name)
		portMap[node.IPFSSwarmPort] = fmt.Sprintf("%s IPFS Swarm", node.Name)
		portMap[node.IPFSGatewayPort] = fmt.Sprintf("%s IPFS Gateway", node.Name)
		portMap[node.RQLiteHTTPPort] = fmt.Sprintf("%s RQLite HTTP", node.Name)
		portMap[node.RQLiteRaftPort] = fmt.Sprintf("%s RQLite Raft", node.Name)
		portMap[node.ClusterAPIPort] = fmt.Sprintf("%s IPFS Cluster API", node.Name)
		portMap[node.ClusterPort] = fmt.Sprintf("%s IPFS Cluster P2P", node.Name)
		portMap[node.UnifiedGatewayPort] = fmt.Sprintf("%s Unified Gateway", node.Name)
	}

	portMap[t.GatewayPort] = "Gateway"
	portMap[t.OlricHTTPPort] = "Olric HTTP API"
	portMap[t.OlricMemberPort] = "Olric Memberlist"
	portMap[t.AnonSOCKSPort] = "Anon SOCKS Proxy"

	return portMap
}

// GetFirstNode returns the first node (the one that creates the cluster)
func (t *Topology) GetFirstNode() *NodeSpec {
	if len(t.Nodes) > 0 {
		return &t.Nodes[0]
	}
	return nil
}

// GetJoiningNodes returns all nodes except the first one (they join the cluster)
func (t *Topology) GetJoiningNodes() []NodeSpec {
	if len(t.Nodes) > 1 {
		return t.Nodes[1:]
	}
	return nil
}

// GetNodeByName returns a node by its name, or nil if not found
func (t *Topology) GetNodeByName(name string) *NodeSpec {
	for i, node := range t.Nodes {
		if node.Name == name {
			return &t.Nodes[i]
		}
	}
	return nil
}
