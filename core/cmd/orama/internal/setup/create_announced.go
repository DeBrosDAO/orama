package setup

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// ResolveAnnounced fills a creation from the announcement of the network, when
// the registry has one under the name: the chain id, the release repository, the
// channel, the minimum version, the seeds, the faucet and the release root that
// the flags left unset. A flag overrides the announcement. A name the registry
// does not know, or knows as a created network, changes nothing. It copies the
// creation, so the caller's options are not touched.
func ResolveAnnounced(ctx context.Context, o *Options, d Deps) error {
	if o.Create == nil || o.Create.Name == "" {
		return nil
	}
	c := *o.Create
	o.Create = &c
	n, err := d.Networks.Resolve(ctx, c.Name)
	if errors.Is(err, netregistry.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("look for an announcement of the network %q: %w", c.Name, err)
	}
	if n.Manifest.Announced() {
		c.takeAnnounced(n)
	}
	return nil
}

// takeAnnounced fills what the flags left unset from the announcement n.
func (c *CreateOptions) takeAnnounced(n *netregistry.Network) {
	m := n.Manifest
	c.Announced = true
	c.ChainID = orDefaultString(c.ChainID, m.ChainID)
	c.ReleaseRepo = orDefaultString(c.ReleaseRepo, m.ReleaseRepo)
	c.Channel = orDefaultString(c.Channel, m.Channel)
	c.MinVersion = orDefaultString(c.MinVersion, m.MinVersion)
	if len(c.Seeds) == 0 {
		c.Seeds = slices.Clone(m.Seeds)
	}
	if c.ReleaseRoot == "" {
		c.AnnouncedRoot = n.Root
	}
	c.NoFaucet = c.NoFaucet || !m.Faucet
}

// checkJoinable refuses to join a network that is only announced: it has no
// genesis, so no chain to join, until its creator has run
// `orama setup --create-network`.
func checkJoinable(n *netregistry.Network) error {
	if err := n.Manifest.CheckCreated(); err != nil {
		return clierr.Wrap(clierr.CodeUsage, err)
	}
	return nil
}
