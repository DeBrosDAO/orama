package networkcmd

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/netclass"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/version"
)

func newAnnounceCmd() *cobra.Command {
	var (
		dir, name, chainID, rootFile, releaseRepo, channel, minVersion string
		seeds                                                          []string
		faucet                                                         bool
	)
	cmd := &cobra.Command{
		Use:   "announce",
		Short: "Write networks/<name>/ for a network that does not exist yet",
		Long: `Write networks/<name>/ (release-root.json and, last, manifest.json) for a
network whose chain has not been created: its name, the chain id it will have, the
channel, the release repository and the release root its releases are verified
against, whether it has a faucet, and its seeds. The manifest pins no genesis, so
the network is listed but cannot be joined: 'orama setup --network <name>' says it
has not been created yet.

The person who creates the network then runs 'orama setup --create-network <name>'
with no --release-root: the chain id, the release repository, the channel and the
release root come from the announcement (a flag overrides). When the chain exists
the creation publishes the full manifest, with the genesis, over the announcement.

An announcement can be written again until the network is created. A network that
is already created is refused: a reset needs a new chain id from the creation.

Afterwards run 'make -C core sync-networks' so the binary embeds the manifest, and
commit networks/ and core/pkg/netregistry/embedded/ together.`,
		Example: `  orama maint network announce --name stagenet --chain-id orama-stagenet-7 \
    --release-repo https://releases.orama.network --release-root release-root.json \
    --channel nightly --faucet --seed seed1.stagenet.orama.network`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if faucet && netclass.IsProduction(chainID) {
				return clierr.Usage("--faucet: %q is a production chain id, and a production chain has no faucet", chainID)
			}
			root, err := os.ReadFile(rootFile)
			if err != nil {
				return clierr.Usage("read the release root: %v", err)
			}
			m, err := netregistry.Announce(netregistry.AnnounceInput{
				Dir: dir, Name: name, ChainID: chainID, ReleaseRoot: root, Channel: channel, MinVersion: minVersion,
				ReleaseRepo: releaseRepo, Seeds: seeds, Faucet: faucet,
			})
			if errors.Is(err, netregistry.ErrAlreadyCreated) {
				return clierr.Conflict("%v", err)
			}
			if err != nil {
				return clierr.Usage("announce network %s: %v", name, err)
			}
			p := printer.For(cmd)
			p.Printf("Announced network %s: chain %s, release root sha256 %s, no genesis yet\n", m.Name, m.ChainID, m.ReleaseRootSHA256)
			p.Printf("Next: make -C core sync-networks, then commit %s/%s and core/pkg/netregistry/embedded/\n", dir, m.Name)
			p.Printf("Its creator runs: orama setup --create-network %s --ip <address> ... (no --release-root needed)\n", m.Name)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&dir, "dir", defaultPublishDir, "The repository's networks directory")
	f.StringVar(&name, "name", "", "Network name, for example stagenet [required]")
	f.StringVar(&chainID, "chain-id", "", "The chain id the network will have [required]")
	f.StringVar(&releaseRepo, "release-repo", "", "https base URL of the release repository [required]")
	f.StringVar(&rootFile, "release-root", "", "The release-root.json file its releases are verified against [required]")
	f.StringVar(&channel, "channel", "", "Release channel: nightly, main or dev/<branch> [required]")
	f.StringVar(&minVersion, "min-version", version.Current, "Oldest orama version that may join, X.Y.Z")
	f.StringArrayVar(&seeds, "seed", nil, "A seed DNS name (repeatable; default: the creator's seed<N>.<name>.orama.network)")
	f.BoolVar(&faucet, "faucet", false, "The network funds new operators from a faucet")
	for _, required := range []string{"name", "chain-id", "release-repo", "release-root", "channel"} {
		_ = cmd.MarkFlagRequired(required)
	}
	return cmd
}
