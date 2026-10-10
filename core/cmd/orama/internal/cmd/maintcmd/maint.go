// Package maintcmd is `orama maint`: the commands of the people who build,
// release and repair a network, kept out of an operator's `orama --help`.
//
// A command moves here, it is not copied: it has one definition, in its own
// package, and this group mounts it. The commands a systemd unit that is
// already installed on a node runs keep their old path as a hidden alias in
// their own group (see cmdmeta.HiddenAlias).
package maintcmd

import (
	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/buildcmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/clustercmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/globalcmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/inspectcmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/invitecmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/networkcmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/node"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/operatorcmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/pushcmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/rolloutcmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/sandboxcmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/vpncmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
)

// New returns the hidden maintainer group. It is hidden from `orama --help` and
// listed in the command reference: every command in it works, is documented and
// has an end-to-end test.
func New() *cobra.Command {
	maint := cmdmeta.MarkListed(&cobra.Command{
		Use:    "maint",
		Short:  "Maintainer commands: build, release, inspect, install and repair",
		Hidden: true,
		Long: `Commands for the people who build, release and repair a network. An operator
who joins a network does not need any of them; they are here, out of the way,
and they all work.

  build, push, rollout       build a signed archive and roll it onto a cluster
  inspect, sandbox           check a cluster over SSH; throwaway Hetzner clusters
  invite                     mint an invite for a node to join a cluster
  operator, cluster          the cluster's operator wallets, creators and update policy
  vpn                        a Tor client for an Orama Tor network
  node                       install and stage a node, auto-update, recovery, migration
  global                     validator keys, chain binary staging, the Tor network, tx gate
  network                    publish a network's manifest`,
	})
	maint.AddCommand(
		buildcmd.Cmd, pushcmd.Cmd, rolloutcmd.Cmd,
		inspectcmd.Cmd, sandboxcmd.Cmd,
		invitecmd.Cmd,
		operatorcmd.Cmd, clustercmd.MaintCmd,
		vpncmd.Cmd,
		node.MaintCmd, globalcmd.MaintCmd,
		networkcmd.MaintCmd,
	)
	return maint
}
