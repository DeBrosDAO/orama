// Package nodeapi is the contract between a node and the core cluster: the
// paths a node records itself at, and the shapes it sends.
//
// It holds no logic and imports nothing, so both ends can depend on it: the
// gateway handler that serves these paths, and the node client that calls them.
// One definition rather than two is what makes the client's tests able to check
// agreement with the real handler instead of with themselves — and it keeps
// `pkg/node` from importing a gateway handler package to learn its own wire
// format.
package nodeapi

// The paths a node records itself at.
const (
	// PathRegister records this node, or updates the record it already has.
	PathRegister = "/v1/internal/node/register"

	// PathHeartbeat refreshes this node's liveness.
	PathHeartbeat = "/v1/internal/node/heartbeat"
)

// RegisterRequest is a node's account of itself.
//
// It carries no node id: which node this is about comes from the stamp on the
// request, so a node cannot register another one.
type RegisterRequest struct {
	IPAddress      string `json:"ip_address"`
	InternalIP     string `json:"internal_ip"`
	Region         string `json:"region"`
	SSHUser        string `json:"ssh_user,omitempty"`
	Environment    string `json:"environment,omitempty"`
	OperatorWallet string `json:"operator_wallet,omitempty"`
}

// HeartbeatResponse tells a node whether the row it is keeping alive exists.
//
// A heartbeat that matched nothing is not an error: it is a node whose
// registration never landed, or was reaped while it was restarting. It is told
// so, and registers.
type HeartbeatResponse struct {
	Registered bool `json:"registered"`
}
