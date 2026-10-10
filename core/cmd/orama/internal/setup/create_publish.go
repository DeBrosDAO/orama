package setup

import (
	"fmt"
	"net"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// CreatedNetwork is what a creation leaves for its maintainer to publish.
type CreatedNetwork struct {
	// Manifest is the network as written to Dir.
	Manifest *netregistry.Manifest
	// Dir is networks/<name>/ as written: manifest.json, genesis.json and
	// release-root.json.
	Dir string
	// Machines are the committee's addresses in order, for the seed records.
	Machines []string
	// Announced says the network was announced in the registry, so the files
	// written replace its announcement.
	Announced bool
}

// ManifestURL is where the website serves the manifest once the network is
// published there: the address `orama network add` takes.
func (c *CreatedNetwork) ManifestURL() string {
	return netregistry.PublishedBaseURL + c.Manifest.Name + "/" + netregistry.ManifestFile
}

// SeedRecords are the DNS records the seeds need: each seed an A record for the
// machine of the same position when there is one per machine, else a line saying
// each must resolve to one of the machines.
func (c *CreatedNetwork) SeedRecords() []string {
	seeds := c.Manifest.Seeds
	records := make([]string, 0, len(seeds))
	for i, s := range seeds {
		switch {
		case len(seeds) == len(c.Machines) && net.ParseIP(c.Machines[i]) != nil:
			records = append(records, fmt.Sprintf("%s.\tIN\tA\t%s", s, c.Machines[i]))
		default:
			records = append(records, fmt.Sprintf("%s must resolve to one of %v", s, c.Machines))
		}
	}
	return records
}

// replaces says, for a network that was announced, that the copy takes the place
// of its announcement.
func (c *CreatedNetwork) replaces() string {
	if c.Announced {
		return " (replacing its announcement: the manifest now pins the genesis)"
	}
	return ""
}

// NextSteps are the lines that tell the maintainer what makes the network
// joinable, in order.
func (c *CreatedNetwork) NextSteps() []string {
	m := c.Manifest
	steps := []string{
		fmt.Sprintf("The network %s (chain %s) is running, and its description is written to %s/.", m.Name, m.ChainID, c.Dir),
		"To make it joinable:",
		"  1. Create these DNS records (a joiner reaches the chain through the seeds):",
	}
	for _, r := range c.SeedRecords() {
		steps = append(steps, "       "+r)
	}
	steps = append(steps,
		fmt.Sprintf("  2. In the repository: copy %s to networks/%s/%s, run `make -C core sync-networks`, and commit networks/ and core/pkg/netregistry/embedded/.", filepath.Clean(c.Dir), m.Name, c.replaces()),
		"     The next CLI release then knows the network by name.",
		"  3. Deploy the website (website/deploy.sh): it serves networks/ at "+netregistry.PublishedBaseURL+".",
		"Until a CLI release carries it, anyone can join by adding the network from the website's copy:",
		"  orama network add "+c.ManifestURL(),
		fmt.Sprintf("  orama setup --network %s --name <name> --ip <address> ...", m.Name),
		"A chain id is used once: a reset of this network needs a new chain id and clean machines.",
	)
	return steps
}
