package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// maxReleaseRootBytes bounds the release-root.json a creation reads: a TUF root is
// a few KiB (netregistry's own bound is the same).
const maxReleaseRootBytes = 1 << 20

// BuildCreatePlan decides what each IP gets when the network is created: every
// machine is a full node and a seat of the bootstrap committee, the first creates
// the cluster, and none creates a validator of its own (the committee seats are
// validators from block 1 without a self-bond).
func BuildCreatePlan(o Options, m *netregistry.Manifest, env string) (*Plan, error) {
	if o.Create == nil {
		return nil, fmt.Errorf("not a network creation")
	}
	p, err := BuildPlan(PlanInput{Options: o, Network: m, Env: env})
	if err != nil {
		return nil, err
	}
	for i := range p.Nodes {
		p.Nodes[i].Validator = false
		p.Nodes[i].BindConsensus = p.Nodes[i].Full()
	}
	p.Notes = append(p.Notes, createNotes(o.Create, m, len(p.Nodes))...)
	return p, nil
}

// createNotes tell the operator what a creation does beyond a join.
func createNotes(c *CreateOptions, m *netregistry.Manifest, machines int) []string {
	notes := []string{
		fmt.Sprintf("CREATES the network %s (chain %s): the %d machines are its bootstrap committee, validators from block 1 with no self-bond", m.Name, m.ChainID, machines),
		fmt.Sprintf("release %s: repository %s, root %s", m.Channel, m.ReleaseRepo, m.ReleaseRootSHA256),
		"seeds: " + strings.Join(m.Seeds, ", ") + " (they must resolve to these machines before anyone can join)",
		fmt.Sprintf("writes %s/%s/ (manifest, genesis, release root) for you to publish", c.PublishDir, m.Name),
		"each seat's account is a key in oramad's test keyring on its machine (unencrypted, never copied off it)",
	}
	if c.Announced {
		notes = append(notes, "the network is announced in the registry: what no flag set (chain id, release repository, channel, minimum version, seeds, faucet and release root) comes from its announcement, and the genesis built now is published over it")
	}
	if c.Production() {
		return append(notes, "production chain id: the chain's own epochs, no faucet, and the operator account must be funded by hand")
	}
	faucet := "no faucet"
	if c.Faucet() {
		faucet = "faucet on"
	}
	return append(notes, fmt.Sprintf("test network: epochs of %s or %d blocks, %s", testEpochDuration, testMinBlocksPerEpoch, faucet))
}

// createNetwork is the network a creation will publish, as far as it is known
// before the genesis exists, and the CLI environment the cluster is recorded
// under: the network's name unless --env says otherwise, which is where the
// faucet looks for a node to sign on.
func createNetwork(opts Options) (*netregistry.Network, string, error) {
	c := opts.Create
	root, err := c.releaseRoot()
	if err != nil {
		return nil, "", err
	}
	m, err := c.manifest(netregistry.Digest(root), len(opts.IPs))
	if err != nil {
		return nil, "", err
	}
	env := opts.Env
	if env == "" {
		env = m.Name
	}
	return &netregistry.Network{Manifest: m, Root: root}, env, nil
}

// releaseRoot is the release root of the new network: the file --release-root
// names, else the root of the network's announcement.
func (c *CreateOptions) releaseRoot() ([]byte, error) {
	if c.ReleaseRoot == "" {
		return c.AnnouncedRoot, nil
	}
	return readReleaseRoot(c.ReleaseRoot)
}

// readReleaseRoot reads --release-root, at most maxReleaseRootBytes.
func readReleaseRoot(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, clierr.Usage("read --release-root: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxReleaseRootBytes+1))
	if err != nil {
		return nil, clierr.Usage("read --release-root %s: %v", path, err)
	}
	if len(data) > maxReleaseRootBytes || !json.Valid(data) {
		return nil, clierr.Usage("--release-root %s is not a release-root.json (JSON, at most %d bytes)", path, maxReleaseRootBytes)
	}
	return data, nil
}

// checkRecordedHosts refuses a creation into an environment that already records
// machines the run does not name: the new network needs a cluster of its own.
func checkRecordedHosts(env string, recorded []RecordedNode, ips []string) error {
	var others []string
	for _, n := range recorded {
		if !slices.Contains(ips, n.Host) {
			others = append(others, n.Host)
		}
	}
	if len(others) > 0 {
		return clierr.Usage("the environment %q already records the nodes %s, which are not among these machines: a new network needs a cluster of its own; pass --env <name> for another environment", env, strings.Join(others, ", "))
	}
	return nil
}

// planForCreate is the plan of a creation, touching no machine; the wizard shows
// it before it asks to go ahead.
func planForCreate(_ context.Context, opts Options, d Deps) (*Plan, error) {
	n, env, err := createNetwork(opts)
	if err != nil {
		return nil, err
	}
	if err := checkRecordedHosts(env, d.Record.Hosts(env), opts.IPs); err != nil {
		return nil, err
	}
	return BuildCreatePlan(opts, n.Manifest, env)
}
