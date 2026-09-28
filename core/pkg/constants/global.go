package constants

// Global-node listeners. They sit outside the index block (10100–10199), the
// tenant block (10000–10099) and the chain block (31000–31004). The cluster
// Kubo API stays IPFSAPIPort (10107); a global node's Kubo API is loopback
// only and is not that port.
const (
	// GlobalIPFSAPIPort is the public Kubo API on 127.0.0.1.
	GlobalIPFSAPIPort = 31107
	// GlobalRelayMetricsPort is the relay's metrics listener on 127.0.0.1.
	GlobalRelayMetricsPort = 31110
)
