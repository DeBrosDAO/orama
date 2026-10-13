package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/spf13/cobra"
)

// Environment variables of the GC units (orama-namespace-ipfs-gc@.service and
// orama-global-ipfs-gc.service): the daemon's RPC address and credential. The
// credential is in an EnvironmentFile and never on a command line.
const (
	ipfsGCAPIEnv  = "IPFS_API"
	ipfsGCAuthEnv = "IPFS_API_AUTH"
)

var ipfsGCCmd = &cobra.Command{
	Use:    "ipfs-gc",
	Short:  "Garbage-collect an IPFS repo through its daemon (run by the namespace and global IPFS GC units)",
	Hidden: true,
	Long: `Garbage-collect the repo of the running Kubo daemon through its RPC API. The
daemon's address and credential are IPFS_API and IPFS_API_AUTH.

It is the ExecStart of the GC oneshots (a namespace's, and the public Kubo's on
a global node), not an operator command. 'ipfs repo gc'
did this before, and a stop of the unit (orama node restart, an upgrade, a node
stop: the unit requires the daemon and is part of the node) never ended it
cleanly: Kubo's CLI answers the first SIGTERM by waiting for the collection, so
the stop ran into TimeoutStopSec and the unit stayed failed until the next run.
Here SIGTERM cancels the request and the command exits 0. A collection that was
stopped has removed what it removed, and the next timer run takes up the rest.
Every other failure exits non-zero.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, os.Interrupt)
		defer stop()
		return runIPFSGC(ctx, cmd.OutOrStdout(), os.Getenv, ipfs.RepoGC)
	},
}

// runIPFSGC collects once. ctx is done when the process was asked to stop; the
// collection then ends and that is the outcome, not an error.
func runIPFSGC(ctx context.Context, out io.Writer, getenv func(string) string,
	gc func(ctx context.Context, baseURL, token string) (int, error)) error {
	baseURL, token, err := ipfs.APIEndpoint(getenv(ipfsGCAPIEnv), getenv(ipfsGCAuthEnv))
	if err != nil {
		return err
	}
	removed, err := gc(ctx, baseURL, token)
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		fmt.Fprintf(out, "stopped before the collection finished, after %d blocks; the next scheduled run continues\n", removed)
		return nil
	}
	if err != nil {
		return fmt.Errorf("garbage collection of the IPFS repo at %s failed: %w", baseURL, err)
	}
	fmt.Fprintf(out, "removed %d blocks\n", removed)
	return nil
}

func init() {
	Cmd.AddCommand(cmdmeta.MarkNodeLocal(ipfsGCCmd))
}
