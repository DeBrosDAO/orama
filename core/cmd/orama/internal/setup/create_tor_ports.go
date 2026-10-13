package setup

import (
	"context"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tornet"
)

// TorHost is a Machine that can run a role of the Orama Tor network a creation
// makes. The machines setup enrols over SSH all are.
type TorHost interface {
	Machine
	// AuthorityKeys makes the keys of the directory authority this machine will run, on
	// this machine, or reads back the ones it has (`orama maint global tor
	// authority-keys`). The identity key never leaves it.
	AuthorityKeys(ctx context.Context, in AuthorityKeysInput) (tornet.AuthorityKeys, error)
	// InstallTor puts the network file beside the staged release and runs `orama global
	// install` for the role. It starts nothing.
	InstallTor(ctx context.Context, in TorInstall) error
	// StartTor starts the role's units (`orama global start`); starting what runs is a no-op.
	StartTor(ctx context.Context, role TorRole) error
	// RestartTor restarts the role (`orama global restart`). A directory authority
	// that is restarted too soon after another one started is refused by the machine
	// with exit code clierr.CodeNotYet (tornet.ErrAuthorityRoll), and nothing changes.
	RestartTor(ctx context.Context, role TorRole) error
	// TorState reads what the machine has of the role.
	TorState(ctx context.Context, role TorRole) (TorState, error)
}

// AuthorityKeysInput is the authority a machine makes the keys of.
type AuthorityKeysInput struct {
	Nickname string
	Address  string
}

// TorInstall is one `orama global install` of a Tor role.
type TorInstall struct {
	Role TorRole
	IP   string
	// Contact is the ContactInfo the descriptor publishes.
	Contact string
	// NodeID is the relay's on-chain node name, its nickname is derived from it.
	NodeID string
	Exit   bool
	// Network is the tor-network.json the role joins.
	Network []byte
	// Bundle is the authority's key bundle on the machine (AuthorityKeys.Bundle).
	Bundle string
}

// TorState is what a machine has of a Tor role.
type TorState struct {
	// Installed: the role's unit is installed. Active: it runs.
	Installed, Active bool
	// NetworkSHA256 is the digest of the network file installed on the machine, empty
	// when it has none.
	NetworkSHA256 string
	// RestartPending: the role's torrc is newer than its last start, so tor runs an
	// older configuration than the one installed (an install that was not followed by
	// its restart).
	RestartPending bool
	// Now is the machine's clock, the one its consensus is judged by.
	Now time.Time
	// Info is what the role's tor process knows: its fingerprint and the consensus it
	// holds. Nil when the role has no DataDirectory yet.
	Info *tornet.NodeInfo
}
