package networkcmd

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// defaultPublishDir is the repository's networks directory, relative to the
// repository root where a deploy runs.
const defaultPublishDir = "networks"

// MaintCmd is `orama maint network`: what a maintainer does to the registry.
var MaintCmd = &cobra.Command{
	Use:   "network",
	Short: "Maintain the published networks",
}

func init() { MaintCmd.AddCommand(newPublishCmd(), newAnnounceCmd()) }

func newPublishCmd() *cobra.Command {
	var (
		dir, name, chainID, genesisFile, rootFile, torNetworkFile string
		seeds                                                     []string
		channel, minVersion, releaseRepo                          string
		faucet                                                    bool
	)
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Write networks/<name>/ for a chain that was just deployed",
		Long: `Write networks/<name>/ (genesis.json, release-root.json, tor-network.json when the
network has one and, last, manifest.json) from the genesis a deploy built, for
the chain id it was built under. The manifest carries the SHA-256 of the genesis,
of the release root and of the Tor network file, so what a joining operator
fetches can be checked. --tor-network pins the network's Tor network file
(tor-network.json, the private Orama Tor network its relays join), which
'orama setup' then gives to every relay it installs; the Tor network is a
different thing from the chain, so a reset of the chain keeps the file.

An unset field keeps its value from the manifest already published; the first
publish of a network sets --seeds, --channel, --min-version, --release-repo and
--release-root. A chain id that is already published keeps its genesis: a reset
of the network needs a new chain id (orama-stagenet-5 becomes orama-stagenet-6),
and publishing the old id with another genesis is refused.

Afterwards run 'make -C core sync-networks' so the binary embeds the new manifest,
and commit networks/ and core/pkg/netregistry/embedded/ together.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, err := publishInput(dir, name, chainID, genesisFile, rootFile, torNetworkFile)
			if err != nil {
				return err
			}
			in.Seeds, in.Channel, in.MinVersion, in.ReleaseRepo = seeds, channel, minVersion, releaseRepo
			if cmd.Flags().Changed("faucet") {
				in.Faucet = &faucet
			}
			m, err := netregistry.Publish(in)
			if errors.Is(err, netregistry.ErrGenesisChange) {
				return clierr.Conflict("%v", err)
			}
			if err != nil {
				return clierr.Failure("publish network %s: %w", name, err)
			}
			p := printer.For(cmd)
			p.Printf("Published network %s: chain %s, genesis sha256 %s, release root sha256 %s\n", m.Name, m.ChainID, m.GenesisSHA256, m.ReleaseRootSHA256)
			if m.TorNetworkSHA256 != "" {
				p.Printf("Tor network file sha256 %s\n", m.TorNetworkSHA256)
			}
			p.Printf("Next: make -C core sync-networks, then commit %s/%s and core/pkg/netregistry/embedded/\n", dir, m.Name)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&dir, "dir", defaultPublishDir, "The repository's networks directory")
	f.StringVar(&name, "name", "", "Network name, for example stagenet [required]")
	f.StringVar(&chainID, "chain-id", "", "The chain id the genesis was built under [required]")
	f.StringVar(&genesisFile, "genesis", "", "The genesis.json file [required]")
	f.StringVar(&rootFile, "release-root", "", "The release-root.json file (default: the published one)")
	f.StringVar(&torNetworkFile, "tor-network", "", "The Tor network's tor-network.json (default: the published one, if any)")
	f.StringSliceVar(&seeds, "seeds", nil, "Seed DNS names, comma-separated (default: the published ones)")
	f.StringVar(&channel, "channel", "", "Release channel: nightly, main or dev/<branch> (default: the published one)")
	f.StringVar(&minVersion, "min-version", "", "Oldest orama version that may join, X.Y.Z (default: the published one)")
	f.StringVar(&releaseRepo, "release-repo", "", "https base URL of the release repository (default: the published one)")
	f.BoolVar(&faucet, "faucet", false, "Whether the network funds new operators from a faucet (default: the published one)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("chain-id")
	_ = cmd.MarkFlagRequired("genesis")
	return cmd
}

// publishInput reads the genesis, the release root and the Tor network file named by the flags.
func publishInput(dir, name, chainID, genesisFile, rootFile, torNetworkFile string) (netregistry.PublishInput, error) {
	genesis, err := os.ReadFile(genesisFile)
	if err != nil {
		return netregistry.PublishInput{}, clierr.Usage("read the genesis: %v", err)
	}
	in := netregistry.PublishInput{Dir: dir, Name: name, ChainID: chainID, Genesis: genesis}
	if rootFile != "" {
		if in.ReleaseRoot, err = os.ReadFile(rootFile); err != nil {
			return in, clierr.Usage("read the release root: %v", err)
		}
	}
	if torNetworkFile != "" {
		if in.TorNetwork, err = os.ReadFile(torNetworkFile); err != nil {
			return in, clierr.Usage("read the Tor network file: %v", err)
		}
	}
	return in, nil
}
