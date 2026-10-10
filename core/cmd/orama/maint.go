package main

import (
	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/chaincmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/clustercmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/globalcmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/maintcmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/node"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/nodescmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/releasecmd"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
)

// newMaintCmd returns `orama maint` with everything mounted under it.
func newMaintCmd() *cobra.Command {
	maint := maintcmd.New()
	maint.AddCommand(releasecmd.NewCommand())
	return maint
}

// hideReplacedGroups hides, from `orama --help`, the operator command groups that
// `orama setup`, `status`, `upgrade` and `remove` replace. They keep working at
// the same paths, stay in the command reference and keep their end-to-end
// coverage; a newcomer's help just does not list them. A group leaves this list
// when the command that replaces it lands and the group is removed.
func hideReplacedGroups() {
	for _, group := range []*cobra.Command{
		node.Cmd, globalcmd.Cmd, clustercmd.Cmd, chaincmd.Cmd, nodescmd.Cmd,
	} {
		group.Hidden = true
		cmdmeta.MarkListed(group)
	}
}
