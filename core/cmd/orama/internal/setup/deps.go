package setup

import (
	"fmt"
	"os"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
	"github.com/DeBrosOfficial/network/pkg/statesync"
)

// NewDeps wires the real ports: SSH to the machines, the network registry, the
// signed release repository, the seeds' light-client routes, the chain through
// an SSH tunnel to a node, and the operator's RootWallet. names is the name
// claimer, nil until the chain has the transaction.
func NewDeps(report Reporter, names NameClaimer) Deps {
	wallet := newAgentWallet()
	return Deps{
		Networks: registryNetworks{load: cli.LoadNetworks, active: activeNetwork, client: netregistry.NewHTTPClient()},
		Releases: newReleaseFetcher(),
		Trust:    seedTrust{client: statesync.NewHTTPClient()},
		Wallet:   wallet,
		Enroll:   sshEnroller{wallet: wallet, report: report},
		Chain:    tunneledChain{signer: rwagent.New(os.Getenv("RW_AGENT_SOCK"))},
		Funder:   newSSHFaucet(),
		Names:    names,
		ASN:      OriginASN,
		Record:   cliRecorder{},
		Domain:   newClusterDomain(),
		Report:   report,
		Timing:   DefaultTiming(),
	}
}

// activeNetwork is the registry network the active environment runs on.
func activeNetwork() (string, error) {
	env, err := cli.GetActiveEnvironment()
	if err != nil {
		return "", fmt.Errorf("no active environment: %w", err)
	}
	if env.Network != "" {
		return env.Network, nil
	}
	return env.Name, nil
}
