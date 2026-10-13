package setup

import (
	"context"
	"fmt"
	"math/big"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/chaincmd"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// sshFaucet funds an account through `orama chain faucet`'s plumbing: the drip is
// signed on a node of the network, over SSH, with that node's seat key. It is the
// funder of a creation: the network's seeds serve no public faucet until the
// maintainer turns it on (chain/scripts/stagenet/deploy.sh faucet), and the
// creator holds the SSH access to the machines it has just made seats of. It
// works for whoever has such a node in the CLI's configuration under the
// network's name; for nobody else can sign a drip, and for them the run stops and
// says what to send (NotFundedError).
type sshFaucet struct {
	// fund and environment are test seams.
	fund        func(env, recipient string, amount *big.Int) error
	environment func(name string) (*cli.Environment, error)
}

func newSSHFaucet() sshFaucet {
	return sshFaucet{fund: chaincmd.Fund, environment: cli.GetEnvironmentByName}
}

// Fund asks the faucet for amount norama for address.
func (f sshFaucet) Fund(_ context.Context, network *netregistry.Manifest, address string, amount *big.Int) error {
	env, err := f.environment(network.Name)
	if err != nil || len(env.Nodes) == 0 {
		return fmt.Errorf("this machine has no SSH access to a node of %s to sign a faucet drip: the CLI has no environment %q with nodes (the faucet signs on one of the network's own nodes)", network.Name, network.Name)
	}
	return f.fund(network.Name, address, amount)
}
