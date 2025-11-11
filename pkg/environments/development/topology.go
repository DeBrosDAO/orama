package development

import "fmt"

// NodeSpec defines configuration for a single dev environment node
type NodeSpec struct {
	Name              string // bootstrap, bootstrap2, node2, node3, node4
	Role              string // "bootstrap" or "node"
	ConfigFilename    string // bootstrap.yaml, bootstrap2.yaml, node2.yaml, etc.
	DataDir           string // relative path from .debros root
	P2PPort           int    // LibP2P listen port
	IPFSAPIPort       int    // IPFS API port
	IPFSSwarmPort     int    // IPFS Swarm port
	IPFSGatewayPort   int    // IPFS HTTP Gateway port
	RQLiteHTTPPort    int    // RQLite HTTP API port
	RQLiteRaftPort    int    // RQLite Raft consensus port
	ClusterAPIPort    int    // IPFS Cluster REST API port
	ClusterPort       int    // IPFS Cluster P2P port
	RQLiteJoinTarget  string // which bootstrap RQLite port to join (leave empty for bootstraps that lead)
	ClusterJoinTarget string // which bootstrap cluster to join (leave empty for bootstrap that leads)
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
				Name:              "bootstrap",
				Role:              "bootstrap",
				ConfigFilename:    "bootstrap.yaml",
				DataDir:           "bootstrap",
				P2PPort:           4001,
				IPFSAPIPort:       4501,
				IPFSSwarmPort:     4101,
				IPFSGatewayPort:   7501,
				RQLiteHTTPPort:    5001,
				RQLiteRaftPort:    7001,
				ClusterAPIPort:    9094,
				ClusterPort:       9096,
				RQLiteJoinTarget:  "",
				ClusterJoinTarget: "",
			},
			{
				Name:              "bootstrap2",
				Role:              "bootstrap",
				ConfigFilename:    "bootstrap2.yaml",
				DataDir:           "bootstrap2",
				P2PPort:           4011,
				IPFSAPIPort:       4511,
				IPFSSwarmPort:     4111,
				IPFSGatewayPort:   7511,
				RQLiteHTTPPort:    5011,
				RQLiteRaftPort:    7011,
				ClusterAPIPort:    9104,
				ClusterPort:       9106,
				RQLiteJoinTarget:  "localhost:7001",
				ClusterJoinTarget: "localhost:9096",
			},
			{
				Name:              "node2",
				Role:              "node",
				ConfigFilename:    "node2.yaml",
				DataDir:           "node2",
				P2PPort:           4002,
				IPFSAPIPort:       4502,
				IPFSSwarmPort:     4102,
				IPFSGatewayPort:   7502,
				RQLiteHTTPPort:    5002,
				RQLiteRaftPort:    7002,
				ClusterAPIPort:    9114,
				ClusterPort:       9116,
				RQLiteJoinTarget:  "localhost:7001",
				ClusterJoinTarget: "localhost:9096",
			},
			{
				Name:              "node3",
				Role:              "node",
				ConfigFilename:    "node3.yaml",
				DataDir:           "node3",
				P2PPort:           4003,
				IPFSAPIPort:       4503,
				IPFSSwarmPort:     4103,
				IPFSGatewayPort:   7503,
				RQLiteHTTPPort:    5003,
				RQLiteRaftPort:    7003,
				ClusterAPIPort:    9124,
				ClusterPort:       9126,
				RQLiteJoinTarget:  "localhost:7001",
				ClusterJoinTarget: "localhost:9096",
			},
			{
				Name:              "node4",
				Role:              "node",
				ConfigFilename:    "node4.yaml",
				DataDir:           "node4",
				P2PPort:           4004,
				IPFSAPIPort:       4504,
				IPFSSwarmPort:     4104,
				IPFSGatewayPort:   7504,
				RQLiteHTTPPort:    5004,
				RQLiteRaftPort:    7004,
				ClusterAPIPort:    9134,
				ClusterPort:       9136,
				RQLiteJoinTarget:  "localhost:7001",
				ClusterJoinTarget: "localhost:9096",
			},
		},
		GatewayPort:     6001,
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
	}

	portMap[t.GatewayPort] = "Gateway"
	portMap[t.OlricHTTPPort] = "Olric HTTP API"
	portMap[t.OlricMemberPort] = "Olric Memberlist"
	portMap[t.AnonSOCKSPort] = "Anon SOCKS Proxy"

	return portMap
}

// GetBootstrapNodes returns only the bootstrap nodes
func (t *Topology) GetBootstrapNodes() []NodeSpec {
	var bootstraps []NodeSpec
	for _, node := range t.Nodes {
		if node.Role == "bootstrap" {
			bootstraps = append(bootstraps, node)
		}
	}
	return bootstraps
}

// GetRegularNodes returns only the regular (non-bootstrap) nodes
func (t *Topology) GetRegularNodes() []NodeSpec {
	var regulars []NodeSpec
	for _, node := range t.Nodes {
		if node.Role == "node" {
			regulars = append(regulars, node)
		}
	}
	return regulars
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
