package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/chain/archiver"
	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/repair"
)

const (
	archiverInterval   = time.Minute
	historyHTTPLimit   = 1 << 30
	historyHTTPTimeout = 5 * time.Minute
)

func archiverCmd() *cobra.Command {
	var rpc, home string
	var width int64
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "archiver",
		Short: "Bundle finalised block ranges, attest them to x/archive and open their archive deals",
		Long: `archiver reads each finalised --range-blocks range from oramad over RPC, writes
<home>/bundles/<start>-<end>.orbh, and submits MsgAttest with the bundle CID, its
SHA-256 and the block-hash Merkle root. Every archiver of a chain must use the
same range width. <home>/cursor is the last attested height; a restart resumes
after it. <home>/hot-key is the signing key, created on first start (mode 0600);
it must be the hot key of the x/nodes node named in <home>/node-id, which needs
an active ARCHIVER role bond.
For each attested range it then opens the ARCHIVE storage deals the range lacks
(MsgCreateArchiveDeal, priced and timed by the chain) and, once x/storage has
given a deal a provider, uploads the bundle to each assigned provider's public
/pieces endpoint (a provider cannot prove bytes it never received) and records the
deal (MsgAttachReplicas). x/archive marks the
range archived at three attesting operators and three recorded deals.
<home>/monitor.json reports the attested height, the chain's last archived
height, the tip and the lag between them. The archiver does not hold CometBFT's
retain height: the chain's Commit never returns one above the last archived
height.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runArchiver(cmd.Context(), rpc, home, width, interval)
		},
	}
	cmd.Flags().StringVar(&rpc, "rpc", defaultRPC, "oramad CometBFT RPC")
	cmd.Flags().StringVar(&home, "home", ".", "Archiver state directory")
	cmd.Flags().Int64Var(&width, "range-blocks", archiver.DefaultRangeBlocks, "Blocks per archived range")
	cmd.Flags().DurationVar(&interval, "interval", archiverInterval, "Time between passes")
	return cmd
}

func runArchiver(ctx context.Context, rpc, home string, width int64, interval time.Duration) error {
	if interval <= 0 {
		return errors.New("--interval must be positive")
	}
	key, created, err := loadOrCreateHotKey(filepath.Join(home, "hot-key"))
	if err != nil {
		return err
	}
	if created {
		slog.Info("created the archiver key; fund it before it can attest", "address", key.Address)
	}
	nodeID, err := readNodeID(filepath.Join(home, "node-id"))
	if err != nil {
		return err
	}
	client, err := node.Dial(rpc)
	if err != nil {
		return err
	}
	chain, err := archiver.NewNodeChain(client, key)
	if err != nil {
		return err
	}
	r, err := archiver.NewRunner(chain, repair.HTTP{Client: repair.PublicHTTPClient(repairHTTPTimeout)}, key.Address, nodeID, home, width)
	if err != nil {
		return err
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		n, err := r.Step(ctx)
		if n > 0 {
			slog.Info("attested ranges", "count", n)
		}
		if err != nil && ctx.Err() == nil {
			slog.Error("archiver pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func historyCmd() *cobra.Command {
	history := &cobra.Command{Use: "history", Short: "Read archived chain history"}
	var rpc, from, out string
	var height, width int64
	get := &cobra.Command{
		Use:   "get",
		Short: "Fetch the bundle holding a height and verify it against the chain",
		Long: `get loads the bundle whose range holds --height from --from (an archiver home
directory or an http(s) base that serves bundles/<start>-<end>.orbh), checks its
SHA-256, every block's header hash, and the Merkle root against the x/archive
range record, and writes that block's protobuf bytes to --out. A bundle that
does not verify writes nothing.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHistoryGet(cmd, rpc, from, out, height, width)
		},
	}
	get.Flags().StringVar(&rpc, "rpc", defaultRPC, "oramad CometBFT RPC")
	get.Flags().StringVar(&from, "from", "", "Archiver home directory or http(s) base [required]")
	get.Flags().StringVar(&out, "out", "", "File for the block's protobuf bytes [required]")
	get.Flags().Int64Var(&height, "height", 0, "Block height [required]")
	get.Flags().Int64Var(&width, "range-blocks", archiver.DefaultRangeBlocks, "Blocks per archived range")
	for _, name := range []string{"from", "out", "height"} {
		_ = get.MarkFlagRequired(name)
	}
	history.AddCommand(get)
	return history
}

func runHistoryGet(cmd *cobra.Command, rpc, from, out string, height, width int64) error {
	if height < 1 || width < 1 {
		return errors.New("--height and --range-blocks must be positive")
	}
	start := (height-1)/width*width + 1
	end := start + width - 1
	client, err := node.Dial(rpc)
	if err != nil {
		return err
	}
	rec, found, err := archiver.QueryRange(cmd.Context(), client, start, end)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("range %d-%d is not attested on chain", start, end)
	}
	body, err := loadBundle(cmd.Context(), from, start, end)
	if err != nil {
		return err
	}
	blocks, err := archiver.Verify(body, rec)
	if err != nil {
		return err
	}
	b := blocks[height-start]
	if err := os.WriteFile(out, b.Proto, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "block %d hash %X verified against range %d-%d (%d attesters, archived=%t)\n",
		b.Height, b.Hash, start, end, len(rec.Archivers), rec.Archived)
	return nil
}

func loadBundle(ctx context.Context, from string, start, end int64) ([]byte, error) {
	if !strings.HasPrefix(from, "http://") && !strings.HasPrefix(from, "https://") {
		return os.ReadFile(archiver.BundlePath(from, start, end))
	}
	url := fmt.Sprintf("%s/bundles/%d-%d.orbh", strings.TrimRight(from, "/"), start, end)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: historyHTTPTimeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, historyHTTPLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if len(body) > historyHTTPLimit {
		return nil, fmt.Errorf("bundle at %s is larger than %d bytes", url, historyHTTPLimit)
	}
	return body, nil
}
