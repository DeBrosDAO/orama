// Package pushcmd defines the push command.
//
// The same command is mounted as `orama push` and as `orama node push`. They
// used to be separate implementations: this one uploaded sequentially unless
// --fanout was given, the other fanned out unless --direct was given, and their
// fanouts differed in how each target's key reached the hub. Same command name,
// opposite default, two security models.
package pushcmd

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/push"
	"github.com/spf13/cobra"
)

// Cmd is the top-level "push" command.
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

By default the archive is uploaded once to a hub node, which then distributes
it to the others server-to-server. Use --direct to upload from this machine to
each node in turn.

'orama push' and 'orama node push' are the same command.

--archive names the build: the path 'orama build' printed. There is no
default — the newest archive in /tmp may be another checkout's build.

Examples:
  orama push --env devnet --archive /tmp/orama-0.200.0-linux-amd64.tar.gz
  orama push --env devnet --archive <path> --direct    # Upload to each node in turn
  orama push --env devnet --archive <path> --node 1.2.3.4
  orama push --host 1.2.3.4 --archive <path>           # A node not in the inventory yet
  orama push --env devnet --archive <path> --trust-signers 0xYourWallet  # Nodes from before archive signing

Each node verifies the archive with its installed orama before anything under
/opt/orama changes: the manifest signature must recover to an address in the
node's /etc/orama/archive-signers and every file must match the manifest.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return push.Run(&flags)
		},
	}

	f := cmd.Flags()
	f.StringVar(&flags.Archive, "archive", "", "The build archive to push (the path `orama build` printed) [required]")
	f.StringVar(&flags.Env, "env", "", "Target environment (default: active)")
	f.StringVar(&flags.Node, "node", "", "Push to a single node IP from the inventory")
	f.StringVar(&flags.Host, "host", "", "Push to a node that is not in the inventory yet")
	f.StringVar(&flags.User, "user", "", "SSH user for --host (default: root)")
	f.BoolVar(&flags.Direct, "direct", false, "Upload from here to each node in turn, instead of fanning out")
	f.StringSliceVar(&flags.TrustSigners, "trust-signers", nil,
		"Create the archive trust anchor on nodes that have none (installed before archive signing); never changes an existing one")

	// --ip and --fanout are what the top-level command used to take. Fanning
	// out is now the default, so --fanout is accepted and ignored rather than
	// silently meaning the opposite of what it used to.
	f.StringVar(&flags.Host, "ip", "", "Deprecated: use --host")
	f.Bool("fanout", false, "Deprecated: fanning out is the default; use --direct to opt out")
	_ = f.MarkDeprecated("ip", "use --host")
	_ = f.MarkDeprecated("fanout", "fanning out is now the default; use --direct to opt out")

	return cmd
}
