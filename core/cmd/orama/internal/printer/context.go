package printer

import (
	"github.com/spf13/cobra"
)

// JSONFlag is the name of the persistent flag that selects machine-readable
// output. It is defined on the root so every command answers to it, rather
// than each command inventing its own — which is how three commands had it and
// the rest did not.
const JSONFlag = "json"

// Register adds --json to a command's persistent flags.
func Register(root *cobra.Command) {
	root.PersistentFlags().Bool(JSONFlag, false,
		"Print machine-readable JSON instead of tables and status lines")
}

// For returns the Printer a command should write through.
//
// It writes to the command's own streams rather than the process's, so a test
// can capture output by calling SetOut, and it honours --json from anywhere in
// the tree because the flag is persistent.
func For(cmd *cobra.Command) *Printer {
	p := New(cmd.OutOrStdout(), cmd.ErrOrStderr())

	// Lookup rather than GetBool: a command reached before the root's
	// persistent flags are registered has no flag, and that is not an error.
	if f := cmd.Flags().Lookup(JSONFlag); f != nil {
		p = p.WithJSON(f.Value.String() == "true")
	}
	return p
}
