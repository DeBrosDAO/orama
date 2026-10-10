// Package pushcmd defines `orama maint push`.
//
// It used to be mounted twice, as `orama push` and as `orama node push`, as two
// separate implementations with opposite defaults and two security models. It is
// one definition, in `orama maint`.
package pushcmd

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/push"
	"github.com/spf13/cobra"
)

// Cmd is the "push" command, mounted under `orama maint`.
var Cmd = NewCmd("push")

// NewCmd builds the push command under the given name. Each call returns a
// command with its own flag storage; cobra commands cannot be shared between
// parents.
func NewCmd(use string) *cobra.Command {
	var flags push.Flags

	cmd := &cobra.Command{
		Use:   use,
		Short: "Push the binary archive to your nodes",
		Long: `Upload the pre-built binary archive to nodes and extract it.

The archive is uploaded from this machine to each node in turn: node SSH keys
never leave it, and no node is a hub. --direct is accepted and changes nothing.

--archive names the build: the path 'orama maint build' printed. There is no
default — the newest archive in /tmp may be another checkout's build.

Examples:
  orama maint push --env devnet --archive /tmp/orama-0.200.0-linux-amd64.tar.gz
  orama maint push --env devnet --archive <path> --node 1.2.3.4
  orama maint push --host 1.2.3.4 --archive <path>           # A node not in the inventory yet
  orama maint push --env devnet --archive <path> --trust-signers 0xYourWallet  # Nodes from before archive signing

Each node verifies the archive with its installed orama before anything under
/opt/orama changes: the manifest signature must recover to an address in the
node's /etc/orama/archive-signers and every file must match the manifest.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return push.Run(&flags)
		},
	}

	f := cmd.Flags()
	f.StringVar(&flags.Archive, "archive", "", "The build archive to push (the path `orama maint build` printed) [required]")
	f.StringVar(&flags.Env, "env", "", "Target environment (default: active)")
	f.StringVar(&flags.Node, "node", "", "Push to a single node IP from the inventory")
	f.StringVar(&flags.Host, "host", "", "Push to a node that is not in the inventory yet")
	f.StringVar(&flags.User, "user", "", "SSH user for --host (default: root)")
	f.BoolVar(&flags.Direct, "direct", false, "Accepted and ignored: every push uploads from this machine to each node in turn")
	f.StringSliceVar(&flags.TrustSigners, "trust-signers", nil,
		"Create the archive trust anchor on nodes that have none (installed before archive signing); never changes an existing one")

	return cmd
}
