package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/repair"
)

const (
	repairInterval    = 30 * time.Second
	repairHTTPTimeout = 5 * time.Minute
)

type repairFlags struct {
	rpc, home string
	interval  time.Duration
}

func repairCmd() *cobra.Command {
	var fl repairFlags
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Rebuild evicted replicas of the deals that name this repair delegate",
		Long: `repair rebuilds a slot that lost its provider. For every deal file in
<home>/deals/<deal id>.json (mode 0600, {"deal_id": N, "repair_seed": "<hex>"}),
it finds slots that are assigned but not yet accepted while another slot is
active, fetches a surviving replica, rewraps it for the new slot with the repair
seed, checks the piece root, and uploads it to the new provider. It never
recovers the plaintext. <home>/operator holds the address deals name as
repair_delegate; x/storage never assigns such a deal's slots to that operator. The seeds are read
again every step, so a new deal file needs no restart.`,
		RunE: func(cmd *cobra.Command, _ []string) error { return runRepair(cmd.Context(), fl) },
	}
	f := cmd.Flags()
	f.StringVar(&fl.rpc, "rpc", defaultRPC, "oramad CometBFT RPC")
	f.StringVar(&fl.home, "home", ".", "Repair delegate state directory")
	f.DurationVar(&fl.interval, "interval", repairInterval, "Time between repair passes")
	return cmd
}

func runRepair(ctx context.Context, fl repairFlags) error {
	if fl.interval <= 0 {
		return errors.New("--interval must be positive")
	}
	operator, err := readOneLine(filepath.Join(fl.home, "operator"), "the repair delegate's operator address")
	if err != nil {
		return err
	}
	client, err := node.Dial(fl.rpc)
	if err != nil {
		return err
	}
	d, err := repair.New(repair.NodeChain{Client: client}, repair.HTTP{Client: repair.PublicHTTPClient(repairHTTPTimeout)}, operator)
	if err != nil {
		return err
	}
	dir := filepath.Join(fl.home, "deals")
	tick := time.NewTicker(fl.interval)
	defer tick.Stop()
	for {
		if err := repairPass(ctx, d, dir); err != nil && ctx.Err() == nil {
			slog.Error("repair pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func repairPass(ctx context.Context, d *repair.Delegate, dir string) error {
	seeds, loadErr := repair.LoadSeeds(dir)
	ids := make([]uint64, 0, len(seeds))
	for id := range seeds {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	errs := []error{loadErr}
	for _, id := range ids {
		start := time.Now()
		done, err := d.RepairDeal(ctx, id, seeds[id])
		for _, r := range done {
			slog.Info("replica restored", "deal", r.DealID, "slot", r.Slot, "from", r.From, "provider", r.Provider, "took", time.Since(start), "blocks_since_assigned", r.BlocksSinceAssigned)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("deal %d: %w", id, err))
		}
	}
	return errors.Join(errs...)
}
