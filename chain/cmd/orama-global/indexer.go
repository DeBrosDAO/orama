package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/indexer"
)

const (
	// defaultIndexerListen is constants.GlobalIndexerPort in core, on loopback.
	defaultIndexerListen = "127.0.0.1:31015"
	indexerInterval      = 2 * time.Second
	indexerReadTimeout   = 10 * time.Second
	indexerWriteTimeout  = 30 * time.Second
	indexerShutdown      = 5 * time.Second
	indexerDBDir         = "index"
)

func indexerCmd() *cobra.Command {
	var rpc, home, listen string
	var start int64
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "indexer",
		Short: "Index blocks, transactions, accounts and cNFTs, and serve them read-only on loopback",
		Long: `indexer follows oramad over its loopback RPC from --start-height and keeps a
Pebble index in <home>/index: blocks, transactions with their events, an
address → transaction index from event attributes, and x/cnft assets rebuilt
from x/cnft and x/market messages. The last indexed height is stored with each
block, so a restart resumes after it. If the next block is pruned on the node,
the indexer stops with an error instead of skipping it. An index keeps the
start height it was created with; another --start-height needs a new --home.
The read API (GET /index/v1/...) listens on --listen, which must be loopback, or on a co-located machine the namespace address 198.18.0.2.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runIndexer(cmd.Context(), rpc, home, listen, start, interval)
		},
	}
	cmd.Flags().StringVar(&rpc, "rpc", defaultRPC, "oramad CometBFT RPC")
	cmd.Flags().StringVar(&home, "home", ".", "Indexer state directory")
	cmd.Flags().StringVar(&listen, "listen", defaultIndexerListen, "Loopback address (or, co-located, the namespace address) for the read API")
	cmd.Flags().Int64Var(&start, "start-height", 1, "First block to index")
	cmd.Flags().DurationVar(&interval, "interval", indexerInterval, "Time between passes once caught up")
	return cmd
}

func runIndexer(ctx context.Context, rpc, home, listen string, start int64, interval time.Duration) error {
	if interval <= 0 {
		return errors.New("--interval must be positive")
	}
	if err := requireLocalOnly(listen); err != nil {
		return err
	}
	client, err := node.Dial(rpc)
	if err != nil {
		return err
	}
	store, err := indexer.Open(filepath.Join(home, indexerDBDir))
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			slog.Error("failed to close the index", "err", err)
		}
	}()
	follower, err := indexer.NewFollower(client, store, start)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", listen, err)
	}
	srv := &http.Server{
		Handler:           indexer.NewAPI(store, client),
		ReadHeaderTimeout: indexerReadTimeout,
		WriteTimeout:      indexerWriteTimeout,
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	followErr := follow(ctx, follower, interval, served)
	shutCtx, cancel := context.WithTimeout(context.Background(), indexerShutdown)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		return errors.Join(followErr, fmt.Errorf("failed to stop the index API: %w", err))
	}
	return followErr
}

// follow runs the follower until ctx ends, the API stops, or the index can
// no longer advance without a gap. Other errors (oramad restarting, a
// timeout) are logged and the next pass retries from the same cursor.
func follow(ctx context.Context, f *indexer.Follower, interval time.Duration, served <-chan error) error {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		n, err := f.Step(ctx)
		if errors.Is(err, indexer.ErrPruned) {
			return err
		}
		if err != nil && ctx.Err() == nil {
			slog.Error("indexer pass failed", "err", err)
		}
		if n == indexer.MaxBlocksPerStep {
			slog.Info("indexed blocks, still behind the tip", "count", n)
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-served:
			return fmt.Errorf("index API stopped: %w", err)
		case <-tick.C:
		}
	}
}

// namespaceAddr is the co-located machine's orama-global namespace address
// (core/pkg/constants, GlobalNetnsAddr). There the read API listens on it so
// the host's gateway can reach it; that address is on a veth pair the
// namespace firewall opens to the host alone, and it is not published.
const namespaceAddr = "198.18.0.2"

// requireLocalOnly refuses a listen address whose host is neither a loopback
// IP nor the co-located namespace address. The API is for the gateway on this
// host, not for the network.
func requireLocalOnly(listen string) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("--listen %q: %w", listen, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsLoopback() && host != namespaceAddr) {
		return fmt.Errorf("--listen %q must be a loopback IP such as 127.0.0.1, or the co-located namespace address %s", listen, namespaceAddr)
	}
	return nil
}
