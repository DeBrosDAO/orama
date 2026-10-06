// Command orama-global runs the global-node services that sit beside oramad:
// the storage provider, the repair delegate, the history archiver, and the
// chain indexer. Each runs as its own systemd unit and user
// (core/pkg/install/global_units.go) and reaches the chain only through the
// loopback CometBFT RPC.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// defaultRPC is oramad's CometBFT RPC on the same host (constants.ChainRPCPort in core).
const defaultRPC = "tcp://127.0.0.1:31001"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := rootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "orama-global",
		Short:         "Global-node services beside oramad",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(providerCmd(), repairCmd(), archiverCmd(), historyCmd(), indexerCmd())
	return root
}
