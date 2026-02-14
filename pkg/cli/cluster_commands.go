package cli

import (
	"github.com/DeBrosOfficial/network/pkg/cli/cluster"
)

// HandleClusterCommand handles cluster management commands.
func HandleClusterCommand(args []string) {
	cluster.HandleCommand(args)
}
