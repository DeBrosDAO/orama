package archiver

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
)

// Monitor is <home>/monitor.json: how far this archiver has got and how far the chain lets nodes
// prune. RetainLagBlocks is the tip minus the last archived height; it grows while archiving
// stalls, and every validator's block store grows with it, so it is the number to alert on.
type Monitor struct {
	// AttestedHeight is the last height this archiver attested (its cursor).
	AttestedHeight int64 `json:"attested_height"`
	// LastArchivedHeight is x/archive's contiguous archived prefix: the chain never prunes above it.
	LastArchivedHeight int64 `json:"last_archived_height"`
	TipHeight          int64 `json:"tip_height"`
	RetainLagBlocks    int64 `json:"retain_lag_blocks"`
	// UnarchivedRanges counts the attested ranges x/archive has not marked archived.
	UnarchivedRanges int `json:"unarchived_ranges"`
	// DealsOpened counts ARCHIVE deals this process has opened since it started.
	DealsOpened uint64 `json:"deals_opened"`
}

func (r *Runner) writeMonitor(ctx context.Context, unarchived int) error {
	cursor, err := LoadCursor(r.cursorPath())
	if err != nil {
		return err
	}
	tip, err := r.chain.LatestHeight(ctx)
	if err != nil {
		return err
	}
	archived, err := r.chain.LastArchivedHeight(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(Monitor{
		AttestedHeight: cursor, LastArchivedHeight: archived, TipHeight: tip,
		RetainLagBlocks: max(tip-archived, 0), UnarchivedRanges: unarchived, DealsOpened: r.dealsOpened,
	})
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(r.dir, "monitor.json"), body, 0o640); err != nil {
		return fmt.Errorf("write monitor file: %w", err)
	}
	return nil
}
