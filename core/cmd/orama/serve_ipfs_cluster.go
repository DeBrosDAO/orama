package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
)

// serveIPFSClusterCmd is the cluster unit's ExecStart. It is not an operator
// command: ipfs-cluster cannot present Kubo's bearer, so this process does.
func serveIPFSClusterCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "serve-ipfs-cluster",
		Short:        "Run ipfs-cluster behind the Kubo bearer proxy",
		Hidden:       true,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer stop()
			return ipfs.ServeCluster(ctx, ipfs.ServeClusterConfig{})
		},
	}
}
