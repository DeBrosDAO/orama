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

	// PathEnrolKey records the public half of this node's own key.
	PathEnrolKey = "/v1/internal/node/enrol-key"
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
	// Role is how this node serves the cluster: "node", "nameserver", or
	// "nameserver-nsN". Empty leaves the stored role alone.
	Role string `json:"role,omitempty"`
	// StampLevel is the stamp protocol this node signs (auth.StampLevel*). It
	// replaces the stored level and is recorded with this registration: a sender
	// that does not know the field (a build from before it) is level 0, which is
	// what it signs.
	StampLevel int `json:"stamp_level"`
}

// HeartbeatRequest refreshes liveness and, when set, the role and environment
// this node was installed with. An empty body is a liveness refresh that reports
// stamp level 0.
type HeartbeatRequest struct {
	Role        string `json:"role,omitempty"`
	Environment string `json:"environment,omitempty"`
	// StampLevel is the stamp protocol this node signs, refreshed with every
	// heartbeat so that an upgrade shows in the registry without a
	// re-registration, and a rollback to a build that does not send it shows as
	// level 0. Like the registration's, it replaces the stored level.
	StampLevel int `json:"stamp_level"`
}

// HeartbeatResponse tells a node whether the row it is keeping alive exists.
//
// A heartbeat that matched nothing is not an error: it is a node whose
// registration never landed, or was reaped while it was restarting. It is told
// so, and registers.
type HeartbeatResponse struct {
	Registered bool `json:"registered"`
}

// EnrolKeyRequest presents the public half of the key a node generated and
// holds. The private half never leaves that machine.
type EnrolKeyRequest struct {
	PublicKey string `json:"public_key"`
}

// EnrolKeyResponse says whether this call was the one that recorded the key.
//
// A node re-asserts its key on every start, so "already on record, unchanged"
// is the normal answer and is not a failure.
type EnrolKeyResponse struct {
	Recorded bool `json:"recorded"`
}
