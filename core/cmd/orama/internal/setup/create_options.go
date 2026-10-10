package setup

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/netclass"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/version"
)

const (
	// DefaultCreateNodeName is the base of the committee nodes' names when
	// --name is not given: founder, founder-2, ... (not seed: the chain keeps seed<N> for the labels the zone publishes itself).
	DefaultCreateNodeName = "founder"
	// DefaultReleaseRepo is the release repository a new network takes its
	// software from unless --release-repo says otherwise
	// (website/src/docs/contributor/deployment.mdx).
	DefaultReleaseRepo = "https://releases.orama.network"
	// DefaultPublishDir is where the new network's manifest and genesis are
	// written: the repository's networks directory, from where `make` runs.
	DefaultPublishDir = "networks"
	// seedDomain is the zone the default seed names live in:
	// seed<N>.<network>.orama.network.
	seedDomain = "orama.network"
)

// CreateOptions turns a run into the creation of a network: the machines are its
// bootstrap committee, and the run ends with the files that publish it.
type CreateOptions struct {
	// Name is the network's name, the one `orama network use` and --network take.
	Name string
	// ChainID is the chain's id. A chain id without -stagenet-, -devnet- or
	// -localnet- is a production one (pkg/netclass).
	ChainID string
	// ReleaseRoot is the path of the release-root.json the network's releases
	// are verified against; the manifest pins its digest. Without it the run uses
	// the root of the network's announcement (AnnouncedRoot).
	ReleaseRoot string
	// Announced says the network is announced in the registry, and ChainID,
	// ReleaseRepo, Channel, MinVersion, Seeds, the faucet and AnnouncedRoot that
	// the flags left unset were taken from its manifest (ResolveAnnounced).
	Announced     bool
	AnnouncedRoot []byte
	// ReleaseRepo, Channel and MinVersion default to DefaultReleaseRepo, the
	// channel of the class of the network (nightly for a test network, main for
	// a production one) and this CLI's version.
	ReleaseRepo string
	Channel     string
	MinVersion  string
	// Seeds are the DNS names joiners reach the chain through; the default is
	// seed1.<name>.orama.network, one per machine.
	Seeds []string
	// PublishDir receives networks/<name>/ (manifest, genesis, release root).
	PublishDir string
	// ForceNewGenesis builds a new genesis although the machines already carry
	// one. It is refused once a chain has run.
	ForceNewGenesis bool
	// NoFaucet leaves the test-network faucet out of the genesis.
	NoFaucet bool
}

// Production reports whether the network's chain id is a production one.
func (c CreateOptions) Production() bool { return netclass.IsProduction(c.ChainID) }

// Faucet reports whether the genesis turns the test-network faucet on: on a test
// network, unless --no-faucet. A production chain refuses a faucet.
func (c CreateOptions) Faucet() bool { return !c.Production() && !c.NoFaucet }

// seedNames are the seeds: the ones given, else seed<N>.<name>.orama.network for
// each of n machines.
func (c CreateOptions) seedNames(n int) []string {
	if len(c.Seeds) > 0 {
		return c.Seeds
	}
	seeds := make([]string, n)
	for i := range seeds {
		seeds[i] = fmt.Sprintf("seed%d.%s.%s", i+1, c.Name, seedDomain)
	}
	return seeds
}

// manifest is the network's manifest as far as it is known before the genesis
// exists: its genesis digest is a placeholder until it is built. Validating it
// here refuses a bad name, chain id, seed, channel, version or repository before
// any machine is touched.
func (c CreateOptions) manifest(rootSHA256 string, machines int) (*netregistry.Manifest, error) {
	m := &netregistry.Manifest{
		Name: c.Name, ChainID: c.ChainID, Seeds: c.seedNames(machines), Channel: c.Channel, MinVersion: c.MinVersion,
		ReleaseRepo: c.ReleaseRepo, ReleaseRootSHA256: rootSHA256, Faucet: c.Faucet(),
		GenesisSHA256: strings.Repeat("0", 64),
	}
	if err := m.Validate(); err != nil {
		return nil, clierr.Usage("%v", err)
	}
	return m, nil
}

// prepareCreate checks a creation and fills what it defaults. IPs are already
// normalized.
func (o *Options) prepareCreate() error {
	c := o.Create
	switch {
	case o.Network != "":
		return clierr.Usage("--create-network makes a new network and --network joins one: pass one of them")
	case o.ClusterOnly:
		return clierr.Usage("a network is created with its chain: --cluster-only cannot go with --create-network")
	case o.NoValidator:
		return clierr.Usage("--no-validator has no meaning with --create-network: the machines are the network's bootstrap validators")
	case c.ChainID == "":
		return clierr.Usage("--create-network needs --chain-id, for example orama-%s-stagenet-1 (a chain id without -stagenet-, -devnet- or -localnet- is a production one); an announced network supplies it (orama maint network announce)", c.Name)
	case strings.Contains(c.ChainID, netclass.MarkerLocalnet):
		return clierr.Usage("--chain-id %q is a localnet's: the chain locks no parameter on a localnet, which is for scripts/localnet on one machine; use a -stagenet- or -devnet- chain id", c.ChainID)
	case c.ReleaseRoot == "" && c.AnnouncedRoot == nil:
		return clierr.Usage("--create-network needs --release-root: the release-root.json the network's releases are verified against (an announced network supplies it: orama maint network announce)")
	}
	if err := netclass.CheckCommittee(c.ChainID, len(o.IPs)); err != nil {
		return clierr.Usage("%v", err)
	}
	if o.Name == "" {
		o.Name = DefaultCreateNodeName
	}
	c.ReleaseRepo = orDefaultString(c.ReleaseRepo, DefaultReleaseRepo)
	c.PublishDir = orDefaultString(c.PublishDir, DefaultPublishDir)
	c.MinVersion = orDefaultString(c.MinVersion, version.Current)
	c.Channel = orDefaultString(c.Channel, defaultChannel(c.ChainID))
	_, err := c.manifest(strings.Repeat("0", 64), len(o.IPs))
	return err
}

// defaultChannel is the release channel of a network's class: the nightly feed
// for a test network, the main line for a production one.
func defaultChannel(chainID string) string {
	if netclass.IsProduction(chainID) {
		return netregistry.ChannelMain
	}
	return netregistry.ChannelNightly
}

func orDefaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// commandLine is the flags that repeat the creation; nil receiver is a join.
func (c *CreateOptions) commandLine() []string {
	if c == nil {
		return nil
	}
	var parts []string
	flag := func(name, value string) {
		if value != "" {
			parts = append(parts, "--"+name, shellArg(value))
		}
	}
	flag("create-network", c.Name)
	flag("chain-id", c.ChainID)
	flag("release-root", c.ReleaseRoot)
	if c.ReleaseRepo != DefaultReleaseRepo {
		flag("release-repo", c.ReleaseRepo)
	}
	if c.Channel != defaultChannel(c.ChainID) {
		flag("channel", c.Channel)
	}
	flag("min-version", c.MinVersion)
	for _, seed := range c.Seeds {
		flag("seed", seed)
	}
	if c.PublishDir != DefaultPublishDir {
		flag("publish-dir", c.PublishDir)
	}
	if c.NoFaucet {
		parts = append(parts, "--no-faucet")
	}
	return parts
}
