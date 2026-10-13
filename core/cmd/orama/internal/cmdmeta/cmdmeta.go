// Package cmdmeta holds what a command declares about how it runs, read by the
// root command before it executes one.
package cmdmeta

import "github.com/spf13/cobra"

// nodeLocalKey is the annotation of a command a node runs for itself, from a
// systemd unit: it reads only the node's own state and uses no operator
// environment, so it runs with no home directory.
const nodeLocalKey = "orama.node-local"

// MarkNodeLocal declares cmd node-local and returns it.
func MarkNodeLocal(cmd *cobra.Command) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[nodeLocalKey] = "true"
	return cmd
}

// IsNodeLocal reports whether cmd was declared node-local.
func IsNodeLocal(cmd *cobra.Command) bool {
	return cmd.Annotations[nodeLocalKey] == "true"
}
