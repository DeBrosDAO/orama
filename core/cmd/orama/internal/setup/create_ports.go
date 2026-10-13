package setup

import "context"

// Bootstrapper is a Machine that can be a seat of a network being created: it can
// make its chain home and keys without a genesis, build the genesis, take it, and
// be wired to its peers. The machines setup enrols over SSH are all Bootstrappers.
type Bootstrapper interface {
	Machine
	// InitChain runs `orama global install --init-chain` with a placeholder
	// genesis, which makes the chain home and the node's keys. It starts nothing.
	InitChain(ctx context.Context, in InitChainInput) error
	// Seat reads the node's keys and, where missing, makes the seat's account.
	Seat(ctx context.Context) (Seat, error)
	// HomeState says what the chain home holds.
	HomeState(ctx context.Context) (HomeState, error)
	// BuildGenesis runs oramad's genesis commands (GenesisSteps) in a scratch
	// home and returns the genesis they made.
	BuildGenesis(ctx context.Context, steps [][]string) ([]byte, error)
	// ReadGenesis is the genesis in the chain home.
	ReadGenesis(ctx context.Context) ([]byte, error)
	// PutGenesis replaces the genesis in the chain home.
	PutGenesis(ctx context.Context, genesis []byte) error
	// WireChain runs `orama global install` again, now with the persistent peers.
	WireChain(ctx context.Context, in WireInput) error
	// ChainHealth is one poll of the chain: its unit, whether its RPC answers, its height.
	ChainHealth(ctx context.Context) (ChainHealth, error)
	// Epoch is the chain's current epoch, read from its REST API on the machine.
	Epoch(ctx context.Context) (uint64, error)
}

// InitChainInput is the first `orama global install` of a seat.
type InitChainInput struct {
	Node    NodePlan
	IP      string
	User    string
	ChainID string
	// Contact and TorNetwork are for the relay, as in GlobalInstall.
	Contact    string
	TorNetwork []byte
}

// WireInput is the second `orama global install` of a seat.
type WireInput struct {
	Node NodePlan
	IP   string
	User string
	// Peers is the persistent peers, id@host:port,..., of the other seats.
	Peers   string
	Contact string
}

// HomeState is what a machine's chain home holds.
type HomeState struct {
	// Genesis: config/genesis.json exists. Final: it names bootstrap validators,
	// which the placeholder InitChain puts there does not. SHA256 is its digest.
	Genesis bool
	Final   bool
	SHA256  string
	// Started: the chain has run on this machine (it has a block store). A new
	// genesis cannot replace the one a chain started from.
	Started bool
}

// ChainHealth is one poll of a seat's chain.
type ChainHealth struct {
	// Running: the chain unit is active. RPCUp: its RPC answers, whatever its height.
	Running bool
	RPCUp   bool
	Height  int64
	// CatchingUp is the node's own report.
	CatchingUp bool
	// Detail is the end of the chain's log, for an error.
	Detail string
}
