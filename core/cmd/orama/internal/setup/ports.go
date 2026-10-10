package setup

import (
	"context"
	"math/big"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/dnsdelegation"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/onchain"
	"github.com/DeBrosOfficial/network/pkg/statesync"
)

// Everything run.go does to the world goes through these ports. The real ones
// are in remote*.go, release.go, chain.go and wallet.go; the tests use fakes,
// so the order of the steps, what is skipped and what is refused are tested
// without a machine, a repository or a chain.

// Machine is one enrolled VPS.
type Machine interface {
	Host() string
	// Probe reads what the machine is and already has.
	Probe(ctx context.Context) (Facts, error)
	// StageRelease puts the release on the machine and in place at /opt/orama,
	// after verifying it again there against the signer it was endorsed by.
	StageRelease(ctx context.Context, rel *Release) error
	// InstallCluster runs `orama node install` for the cluster node.
	InstallCluster(ctx context.Context, in ClusterInstall) error
	// MintInvite mints a single-use invite for a new node on this machine, which
	// is already in the cluster, and the archive signers a joiner must expect.
	MintInvite(ctx context.Context) (invite string, signers []string, err error)
	// InstallGlobal installs the global services beside the cluster node, writes
	// the chain config and starts them.
	InstallGlobal(ctx context.Context, in GlobalInstall) error
	// StartGlobal starts the services of a node that do not need it registered
	// (the chain, the public IPFS, a relay); starting what runs is a no-op.
	StartGlobal(ctx context.Context, node NodePlan) error
	// ChainState is one poll of the chain node's sync state.
	ChainState(ctx context.Context) (ChainState, error)
	// Identity reads, and creates where missing, the node keys the registration
	// needs, and signs the hot-key binding.
	Identity(ctx context.Context, in IdentityRequest) (NodeIdentity, error)
	// StartServices starts the services that need the node registered.
	StartServices(ctx context.Context, nodeID string) error
	// Nameservers are the nameserver slots the cluster holds, read on this machine
	// (it is in the cluster and its host key is pinned).
	Nameservers(ctx context.Context) ([]dnsdelegation.Delegation, error)
	// OpenChain makes the node's chain REST API reachable from here.
	OpenChain(ctx context.Context) (rest string, stop func(), err error)
	// WaitNode waits until the cluster node is carrying its share of the
	// cluster (its RQLite has joined and its services answer).
	WaitNode(ctx context.Context, budget time.Duration) error
	// RestartNode restarts the cluster node (`orama node restart`) and waits for
	// it to be carrying its share again. force bypasses the node's quorum check,
	// which a cluster of fewer than three nodes can never pass.
	RestartNode(ctx context.Context, budget time.Duration, force bool) error
	// Close releases the connection and the temporary key material.
	Close()
}

// Enroller reaches a machine over SSH with the operator's RootWallet key.
type Enroller interface {
	// Enroll gives the RootWallet a key on a new machine, pinning its host key.
	Enroll(ctx context.Context, req MachineRequest) (Machine, error)
	// Reach opens a machine enrolled before, asking nothing: its key is in the
	// vault and its host key in the CLI's known hosts, or it is refused.
	Reach(ctx context.Context, ip, user string) (Machine, error)
}

// MachineRequest is one machine to reach.
type MachineRequest struct {
	IP, User, HostKey, BootstrapKey, Password string
	UsePassword                               bool
	Env                                       string
}

// Release is a verified release on this machine, ready to put on a node.
type Release struct {
	Version string
	Arch    string
	// ArchivePath is the archive to put on the node: the verified release with
	// the release root in its manifest, signed by the operator's wallet.
	ArchivePath string
	// ManifestSHA256 and CLISHA256 are the digests of the manifest inside the
	// archive and of its bin/orama: a node that has both already runs this build.
	ManifestSHA256 string
	CLISHA256      string
	Remove         func() error
}

// ReleaseSource fetches and verifies the newest release of a network's channel.
type ReleaseSource interface {
	Fetch(ctx context.Context, n *netregistry.Network, arch string) (*Release, error)
}

// NetworkSource resolves --network to a verified network and its genesis.
type NetworkSource interface {
	Resolve(ctx context.Context, name string) (*netregistry.Network, error)
	// Genesis is the network's genesis, checked against the manifest's digest.
	Genesis(ctx context.Context, n *netregistry.Network) ([]byte, error)
}

// TrustSource finds the state-sync trust point of a network.
type TrustSource interface {
	TrustPoint(ctx context.Context, n *netregistry.Network) (*statesync.TrustPoint, error)
}

// Wallet is the operator's RootWallet.
type Wallet interface {
	// Unlocked fails with what to do when the agent is not running or is locked.
	Unlocked(ctx context.Context) error
	// EVMAddress is the account the cluster's archive trust anchors on.
	EVMAddress(ctx context.Context) (string, error)
	// OramaAddress is the operator account on the chain.
	OramaAddress(ctx context.Context) (string, error)
}

// ClusterInstall is one `orama node install`.
type ClusterInstall struct {
	Create bool
	Name   string
	IP     string
	User   string
	Env    string
	Domain string
	ACMECA string
	Wallet string
	// Invite and Signers are what a joiner needs.
	Invite  string
	Signers []string
}

// GlobalInstall is one `orama global install`, with its start.
type GlobalInstall struct {
	Node    NodePlan
	IP      string
	User    string
	ChainID string
	Genesis []byte
	Trust   *statesync.TrustPoint
	// Contact and TorNetwork are for the relay.
	Contact    string
	TorNetwork []byte
}

// ChainState is a chain node's sync state.
type ChainState struct {
	Height     int64
	CatchingUp bool
	// Running is false while the chain unit is not active.
	Running bool
	// Detail is the tail of the chain's log, for an error message.
	Detail string
}

// IdentityRequest asks a node for its registration facts.
type IdentityRequest struct {
	ChainID  string
	Operator string
	// BindConsensus asks the node to sign, with its consensus key, the binding of
	// that key to Operator. The key signs on the node and never leaves it.
	BindConsensus bool
}

// NodeIdentity is what registering a node on the chain needs from the node.
type NodeIdentity struct {
	// ChainNodeID is the CometBFT node id (40 hex).
	ChainNodeID string
	// ConsensusPubKey is the 32-byte ed25519 key the chain signs blocks with.
	ConsensusPubKey []byte
	// HotKey is the hot key's account address; HotBinding its signed binding.
	HotKey     string
	HotBinding clusterreg.NodeBinding
	// ConsensusBinding is the consensus key's binding, signed on the node for the
	// operator; nil unless the request asked for it.
	ConsensusBinding *clusterreg.NodeBinding
}

// ChainSession is a node's chain, readable and writable from here.
type ChainSession interface {
	ChainReader
	Transactor
	Close()
}

// ChainOpener opens the chain of a machine.
type ChainOpener interface {
	Open(ctx context.Context, m Machine, chainID string) (ChainSession, error)
}

// Transactor sends the operator's transactions; *onchain.Client is one.
type Transactor interface {
	RegisterOperator(ctx context.Context) (*onchain.Receipt, error)
	RegisterNode(ctx context.Context, n clusterreg.NodeRegistration) (*onchain.Receipt, error)
	// UpdateNodeBindings replaces the binding set of a registered node.
	UpdateNodeBindings(ctx context.Context, u clusterreg.NodeUpdate) (*onchain.Receipt, error)
	Bond(ctx context.Context, b clusterreg.Bond) (*onchain.Receipt, error)
	DeclareCapacity(ctx context.Context, c clusterreg.Capacity) (*onchain.Receipt, error)
	CreateValidator(ctx context.Context, spec onchain.ValidatorSpec) (*onchain.Receipt, error)
	ClaimNodeName(ctx context.Context, nodeID, name string) (*onchain.Receipt, error)
}

// ChainReader reads what setup needs to know before it sends a transaction.
type ChainReader interface {
	Params(ctx context.Context) (ChainParams, error)
	// Balance is the account's spendable norama; zero for an account the chain
	// has not seen.
	Balance(ctx context.Context, address string) (*big.Int, error)
	OperatorRegistered(ctx context.Context, address string) (bool, error)
	// Node is the registered node, or nil.
	Node(ctx context.Context, id string) (*RegisteredNode, error)
	ValidatorExists(ctx context.Context, operator string) (bool, error)
	// NodeName is the name the node holds on the chain, or "" when it holds none.
	NodeName(ctx context.Context, nodeID string) (string, error)
}

// ChainParams are the x/nodes parameters the budget needs.
type ChainParams struct {
	// MinBond is the per-role floor in norama, by clusterreg.Role*.
	MinBond map[int]*big.Int
	// BondPerGiB is the storage bond per GiB of declared capacity.
	BondPerGiB *big.Int
	// NameDeposit is the norama locked while a node holds its name.
	NameDeposit *big.Int
}

// RegisteredNode is a node as the chain holds it.
type RegisteredNode struct {
	Roles         []int
	Bonds         map[int]*big.Int
	CapacityBytes uint64
	// Bindings are the node's service-key bindings, as the chain holds them.
	Bindings []clusterreg.NodeBinding
}

// Funder funds a new operator account on a network that has a faucet.
type Funder interface {
	Fund(ctx context.Context, network *netregistry.Manifest, address string, amount *big.Int) error
}

// NameClaimer claims a node's name (<name>.<network>.orama.network) on the
// chain, through the operator's session.
type NameClaimer interface {
	// Claim claims name for the node nodeID of the signing operator. Claiming a
	// name the node already holds is not an error.
	Claim(ctx context.Context, chain NameChain, name, nodeID string) error
}

// NameChain is the part of a chain session a name claim uses; ChainSession is one.
type NameChain interface {
	NodeName(ctx context.Context, nodeID string) (string, error)
	ClaimNodeName(ctx context.Context, nodeID, name string) (*onchain.Receipt, error)
}

// ASNLookup finds the autonomous system number an IP address belongs to.
type ASNLookup func(ctx context.Context, ip string) (uint32, error)

// Recorder remembers what the run set up, for the commands that follow.
type Recorder interface {
	// Cluster records the cluster environment and its nodes.
	Cluster(env, network, gatewayURL string, nodes []RecordedNode) error
	// Operator records the operator account on the environment.
	Operator(env, operator string) error
	// Hosts are the cluster nodes already recorded for env.
	Hosts(env string) []RecordedNode
	// ActiveFor is the active environment when it runs on network, else "".
	ActiveFor(network string) string
}

// RecordedNode is a machine in the cluster environment.
type RecordedNode struct{ Host, User, Role string }

// DomainWaiter handles a private cluster's own domain (C5): the records its
// parent zone needs, and waiting until DNS returns them and the cluster serves
// a certificate for the domain.
type DomainWaiter interface {
	// Records are the NS and glue records to create, read from the cluster through
	// via, a machine in it.
	Records(ctx context.Context, via Machine, domain string) ([]string, error)
	// Wait polls until the parent zone returns the records and the cluster's
	// certificate for the domain is issued, or the deadline passes.
	Wait(ctx context.Context, via Machine, domain string, poll, deadline time.Duration) error
}
