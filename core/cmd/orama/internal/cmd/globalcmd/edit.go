package globalcmd

import (
	"fmt"
	"io"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
	"github.com/DeBrosOfficial/network/pkg/globalnode"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/spf13/cobra"
)

const (
	// storageFlag and exitFlag are the settings `orama edit` changes on a node.
	storageFlag = "storage-gb"
	exitFlag    = "exit"
	// maxStorageGB bounds a declared capacity: a petabyte, far above any node and
	// far below where the byte count overflows.
	maxStorageGB = 1_000_000
)

var nodeEditFlags struct {
	storageGB uint64
	exit      bool
}

var nodeEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Change this node's public storage size or exit role (run as root)",
	Long: `Change a setting of the global layer installed on this node. 'orama edit' runs it over
SSH with the node's own CLI; it declares the same change on the chain from your
machine.

--storage-gb N sizes the public Kubo for N GB of declared capacity (its StorageMax
becomes N plus 10%) and restarts it; the repo, its identity and token stay. The
capacity itself is declared on the chain with MsgDeclareCapacity, which 'orama edit'
sends first: the chain refuses a capacity the role bond does not back, and one below
the bytes already reserved by deals.

--exit=true|false switches the Tor relay between a plain relay and an exit by
rewriting only the exit section of its torrc, then restarts the relay. An exit
needs a network whose file allows exits, and refuses the destinations listed in
the exit reject list.`,
	Args: cobra.NoArgs,
	RunE: runNodeEdit,
}

func init() {
	f := nodeEditCmd.Flags()
	f.Uint64Var(&nodeEditFlags.storageGB, storageFlag, 0, "Declared public storage capacity, in GB")
	f.BoolVar(&nodeEditFlags.exit, exitFlag, false, "Make the Tor relay an exit (true) or a plain relay (false)")
	MaintCmd.AddCommand(cmdmeta.MarkNodeLocal(nodeEditCmd))
}

func runNodeEdit(cmd *cobra.Command, _ []string) error {
	storage, exit := cmd.Flags().Changed(storageFlag), cmd.Flags().Changed(exitFlag)
	if !storage && !exit {
		return clierr.Usage("name a setting to change: --%s or --%s", storageFlag, exitFlag)
	}
	if storage && (nodeEditFlags.storageGB == 0 || nodeEditFlags.storageGB > maxStorageGB) {
		return clierr.Usage("--%s must be between 1 and %d GB", storageFlag, maxStorageGB)
	}
	if err := clierr.RequireRoot("editing the global layer"); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	life := globalnode.DefaultLifecycle(out)
	host := install.DefaultGlobalHost(func(format string, args ...any) { fmt.Fprintf(out, format+"\n", args...) })
	// Everything that can refuse is checked before anything is written, so a
	// refused exit change does not leave a node whose Kubo was already resized.
	var relay install.RelayExitChange
	if storage {
		if err := install.RequirePublicKubo(host); err != nil {
			return clierr.Failure("%v", err)
		}
	}
	if exit {
		var err error
		if relay, err = install.PlanRelayExit(host, nodeEditFlags.exit); err != nil {
			return clierr.Failure("%v", err)
		}
	}
	if storage {
		if err := resizeKubo(cmd, life, host, out); err != nil {
			return err
		}
	}
	if exit {
		return switchRelay(cmd, life, host, relay, out)
	}
	return nil
}

// resizeKubo sizes the public Kubo and restarts it.
func resizeKubo(cmd *cobra.Command, life globalnode.Lifecycle, host install.GlobalHost, out io.Writer) error {
	bytes := nodeEditFlags.storageGB * bytesPerGB
	if err := install.SetPublicStorage(host, bytes, install.ChainColocated(life.UnitDir)); err != nil {
		return clierr.Failure("%v", err)
	}
	if err := life.Restart(cmd.Context(), []install.GlobalService{install.GlobalServiceIPFS}); err != nil {
		return clierr.Failure("restart the public Kubo with its new size: %v", err)
	}
	fmt.Fprintf(out, "  public Kubo sized for %d GB\n", nodeEditFlags.storageGB)
	return nil
}

// switchRelay writes the planned torrc and restarts the relay.
func switchRelay(cmd *cobra.Command, life globalnode.Lifecycle, host install.GlobalHost, change install.RelayExitChange, out io.Writer) error {
	if !change.Changed {
		fmt.Fprintf(out, "  relay: already %s\n", exitWord(nodeEditFlags.exit))
		return nil
	}
	if err := change.Apply(host); err != nil {
		return clierr.Failure("%v", err)
	}
	if err := life.Restart(cmd.Context(), []install.GlobalService{install.GlobalServiceRelay}); err != nil {
		return clierr.Failure("restart the relay with its new role: %v", err)
	}
	fmt.Fprintf(out, "  relay: now %s\n", exitWord(nodeEditFlags.exit))
	return nil
}

// exitWord names the relay's role.
func exitWord(exit bool) string {
	if exit {
		return "an exit"
	}
	return "a plain relay"
}
