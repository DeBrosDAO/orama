package setupcmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
)

// createFlags are the flags that make setup create a network. --create-network
// turns the others on; they mean nothing without it.
type createFlags struct {
	name, chainID, releaseRoot, releaseRepo, channel, minVersion, publishDir string
	seeds                                                                    []string
	forceNewGenesis, noFaucet                                                bool
}

// createOnly are the flags that apply to a creation alone.
var createOnly = []string{"chain-id", "release-root", "release-repo", "channel", "min-version", "seed", "publish-dir", "force-new-genesis", "no-faucet"}

func (c *createFlags) bind(f *pflag.FlagSet) {
	f.StringVar(&c.name, "create-network", "", "Create a network of this name instead of joining one: the machines are its bootstrap validators")
	f.StringVar(&c.chainID, "chain-id", "", "With --create-network: the chain id (default: the announced network's). A test network's carries -stagenet-, -devnet- or -localnet-; any other id is a production one, which setup refuses (it creates test networks only)")
	f.StringVar(&c.releaseRoot, "release-root", "", "With --create-network: the release-root.json the network's releases are verified against; the manifest pins its digest (default: the announced network's)")
	f.StringVar(&c.releaseRepo, "release-repo", "", "With --create-network: the https base URL of the release repository (default "+setup.DefaultReleaseRepo+")")
	f.StringVar(&c.channel, "channel", "", "With --create-network: the release channel, nightly, main or dev/<branch> (default nightly, main for a production chain id)")
	f.StringVar(&c.minVersion, "min-version", "", "With --create-network: the oldest orama version that may join, X.Y.Z (default: this CLI's version)")
	f.StringArrayVar(&c.seeds, "seed", nil, "With --create-network: a seed DNS name (repeatable; default seed1.<name>.orama.network, one per machine)")
	f.StringVar(&c.publishDir, "publish-dir", "", "With --create-network: where networks/<name>/ is written (default ./"+setup.DefaultPublishDir+")")
	f.BoolVar(&c.forceNewGenesis, "force-new-genesis", false, "With --create-network: build a new genesis although the machines carry one. Refused once a chain has run")
	f.BoolVar(&c.noFaucet, "no-faucet", false, "With --create-network: leave the test-network faucet out of the genesis")
}

// options is the creation the flags describe, nil for a join. A flag that only
// a creation has, given without --create-network, is an error rather than
// something quietly ignored.
func (c *createFlags) options(cmd *cobra.Command) (*setup.CreateOptions, error) {
	if c.name == "" {
		for _, name := range createOnly {
			if cmd.Flags().Changed(name) {
				return nil, clierr.Usage("--%s applies to --create-network; without it setup joins a network", name)
			}
		}
		return nil, nil
	}
	return &setup.CreateOptions{
		Name: c.name, ChainID: c.chainID, ReleaseRoot: c.releaseRoot, ReleaseRepo: c.releaseRepo, Channel: c.channel,
		MinVersion: c.minVersion, Seeds: append([]string(nil), c.seeds...), PublishDir: c.publishDir,
		ForceNewGenesis: c.forceNewGenesis, NoFaucet: c.noFaucet,
	}, nil
}
