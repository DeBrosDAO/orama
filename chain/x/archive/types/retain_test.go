package types_test

import (
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/archive"
	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

func TestRetainHeight_minOfWindowAndLastArchived(t *testing.T) {
	t.Parallel()

	if got, want := types.RetainHeight(1_000, 100, 50), int64(50); got != want {
		t.Fatalf("RetainHeight = %d, want %d (never above last archived)", got, want)
	}
	if got, want := types.RetainHeight(1_000, 100, 5_000), int64(900); got != want {
		t.Fatalf("RetainHeight = %d, want %d (14-day window binds)", got, want)
	}
	if got := types.RetainHeight(1_000, 100, 5_000); got > 5_000 {
		t.Fatalf("RetainHeight = %d, above last archived", got)
	}
	if got, want := archive.RetainHeight(80, 100, 40), int64(-20); got != want {
		t.Fatalf("RetainHeight = %d, want min(80-100, 40) = %d", got, want)
	}
	if types.PruneAllowed(1, 80, 100, 0) {
		t.Fatal("PruneAllowed pruned a block when nothing is archived")
	}
}

func TestRetainHeight_yearStallDoesNotMovePastLastArchived(t *testing.T) {
	t.Parallel()

	const last int64 = 1_000
	blocks := types.DefaultBlocksIn14Days
	year := blocks * 365 / 14
	tip := last + year

	got := types.RetainHeight(tip, blocks, last)
	if got != last {
		t.Fatalf("RetainHeight = %d, want last archived %d while the tip is a year ahead", got, last)
	}
	if got > last {
		t.Fatalf("RetainHeight %d is above last archived %d", got, last)
	}

	naive := tip - blocks
	if naive <= last {
		t.Fatalf("year-ahead window %d is not past the archive", naive)
	}
	if types.PruneAllowed(last, tip, blocks, last) {
		t.Fatal("PruneAllowed deleted the block at the retain height")
	}
	if types.PruneAllowed(last+1, tip, blocks, last) {
		t.Fatal("PruneAllowed pruned past the last archived height")
	}
	if types.PruneAllowed(naive-1, tip, blocks, last) {
		t.Fatal("PruneAllowed pruned a year of unarchived blocks")
	}
	if !types.PruneAllowed(last-1, tip, blocks, last) {
		t.Fatal("PruneAllowed refused a block strictly below the last archived height")
	}
}

func TestPruneAllowed_keepsFourteenDayWindow(t *testing.T) {
	t.Parallel()

	const (
		tip    int64 = 500_000
		blocks int64 = 1_000
		last   int64 = 500_000
	)
	retain := types.RetainHeight(tip, blocks, last)
	if retain != tip-blocks {
		t.Fatalf("RetainHeight = %d, want %d", retain, tip-blocks)
	}
	if types.PruneAllowed(retain, tip, blocks, last) {
		t.Fatal("pruning the retain height itself was allowed")
	}
	if !types.PruneAllowed(retain-1, tip, blocks, last) {
		t.Fatal("pruning strictly below the retain height was refused")
	}
	if types.PruneAllowed(tip-1, tip, blocks, last) {
		t.Fatal("pruning inside the 14-day window was allowed")
	}
}
