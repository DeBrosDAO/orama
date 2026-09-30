//go:build e2e_fleet

package chaineconomics

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// The documented schedule (docs/CHAIN.md "The schedule"): whole ORAMA per
// epoch in brackets of 730 completed epochs, then a 274 ORAMA tail.
var (
	bracketOrama = []int64{14848, 7424, 3712, 1856, 928}
	tailOrama    = int64(274)
)

const (
	epochsPerBracket = 730
	// maxSafeEpoch is x/emission/types.MaxSafeEpoch, the query bound.
	maxSafeEpoch = 1_000_000_000
	// cumulativeAt3650Orama is docs/CHAIN.md's 21,000,640 ORAMA.
	cumulativeAt3650Orama = 21_000_640
)

func scheduleOrama(epoch uint64) int64 {
	if epoch == 0 {
		return 0
	}
	b := (epoch - 1) / epochsPerBracket
	if b >= uint64(len(bracketOrama)) {
		return tailOrama
	}
	return bracketOrama[b]
}

// validatorMinted is 60% of the schedule summed over completed epochs
// (every amount is a whole ORAMA x 10^9, so the split is exact).
func validatorMinted(completed uint64) chain.Int {
	total := new(big.Int)
	for e := uint64(1); e <= completed; e++ {
		total.Add(total, big.NewInt(scheduleOrama(e)*chain.NoramaPerOrama/100*60))
	}
	var r chain.Int
	r.Set(total)
	return r
}

type scheduleAt struct {
	Epoch              chain.Int `json:"epoch"`
	MaxMintable        chain.Int `json:"max_mintable"`
	ValidatorShare     chain.Int `json:"validator_share"`
	StorageCeiling     chain.Int `json:"storage_ceiling"`
	RelayCeiling       chain.Int `json:"relay_ceiling"`
	DevelopmentCeiling chain.Int `json:"development_ceiling"`
}

// TestEmission_scheduleMatchesTheDocumentedTable: schedule-at at every
// bracket boundary returns the documented maximum and its exact 60/25/10/5
// split (docs/CHAIN.md "The schedule", "The split"); epoch 0 mints nothing.
func TestEmission_scheduleMatchesTheDocumentedTable(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 0)
	for _, e := range []uint64{0, 1, 729, 730, 731, 1460, 1461, 2190, 2191, 2920, 2921, 3650, 3651, 10_000, maxSafeEpoch} {
		var s scheduleAt
		c.Query(t, n, &s, "emission", "schedule-at", fmt.Sprint(e))
		want := chain.Orama(scheduleOrama(e))
		if s.MaxMintable.Cmp(want) != 0 || uint64(s.Epoch.Int64()) != e {
			t.Errorf("epoch %d: max %s, want %s", e, s.MaxMintable.String(), want.String())
			continue
		}
		parts := map[string][2]chain.Int{
			"storage":     {s.StorageCeiling, pct(want, 25)},
			"relay":       {s.RelayCeiling, pct(want, 10)},
			"development": {s.DevelopmentCeiling, pct(want, 5)},
			"validator":   {s.ValidatorShare, pct(want, 60)},
		}
		for name, got := range parts {
			if got[0].Cmp(got[1]) != 0 {
				t.Errorf("epoch %d: %s share %s, want %s", e, name, got[0].String(), got[1].String())
			}
		}
		if s.ValidatorShare.Add(s.StorageCeiling).Add(s.RelayCeiling).Add(s.DevelopmentCeiling).Cmp(s.MaxMintable) != 0 {
			t.Errorf("epoch %d: the four shares do not sum to the maximum", e)
		}
	}
}

func pct(v chain.Int, p int64) chain.Int {
	var r chain.Int
	r.Mul(&v.Int, big.NewInt(p))
	r.Quo(&r.Int, big.NewInt(100))
	return r
}

// TestEmission_supplyCapSoFar: the cumulative cap is exactly 21,000,640 ORAMA
// at epoch 3,650 (docs/CHAIN.md), 0 at epoch 0, one epoch's maximum at 1,
// and grows by the tail after it.
func TestEmission_supplyCapSoFar(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 0)
	capAt := func(e uint64) chain.Int {
		var r struct {
			SupplyCap chain.Int `json:"supply_cap"`
		}
		c.Query(t, n, &r, "emission", "supply-cap-so-far", fmt.Sprint(e))
		return r.SupplyCap
	}
	cases := map[uint64]chain.Int{
		0:    chain.NewInt(0),
		1:    chain.Orama(14848),
		730:  chain.Orama(14848 * 730),
		3650: chain.Orama(cumulativeAt3650Orama),
		3651: chain.Orama(cumulativeAt3650Orama + tailOrama),
	}
	for e, want := range cases {
		if got := capAt(e); got.Cmp(want) != 0 {
			t.Errorf("supply cap at epoch %d: %s, want %s", e, got.String(), want.String())
		}
	}
}

// TestEmission_queryBoundsRefused: an epoch beyond MaxSafeEpoch is refused
// by both schedule queries, and a non-number never reaches the chain
// (boundary and hostile input).
func TestEmission_queryBoundsRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 0)
	over := fmt.Sprint(uint64(maxSafeEpoch) + 1)
	for _, q := range []string{"schedule-at", "supply-cap-so-far"} {
		if out := c.QueryFails(t, n, "emission", q, over); !strings.Contains(out, "epoch must be at most") {
			t.Errorf("%s %s: refusal does not name the bound: %s", q, over, out)
		}
		for _, bad := range []string{"-1", "abc", "18446744073709551616"} {
			c.QueryFails(t, n, "emission", q, bad)
		}
	}
}

// emissionParams is orama.emission.v1.Params.
type emissionParams struct {
	Params struct {
		EpochDurationSeconds chain.Int `json:"epoch_duration_seconds"`
		MinBlocksPerEpoch    chain.Int `json:"min_blocks_per_epoch"`
		AllowBootstrapStake  bool      `json:"allow_bootstrap_stake"`
	} `json:"params"`
}

// TestEmission_devnetShortEpochParams: the run chain uses the devnet
// exception: allow_bootstrap_stake with an epoch shorter than the 24h /
// 14,400-block production floors (docs/CHAIN.md "Genesis parameters"), and
// genesis supply is exactly zero (no premine).
func TestEmission_devnetShortEpochParams(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 0)
	var p emissionParams
	c.Query(t, n, &p, "emission", "params")
	if !p.Params.AllowBootstrapStake {
		t.Errorf("allow_bootstrap_stake is false on a short-epoch run chain")
	}
	if d := p.Params.EpochDurationSeconds.Int64(); d <= 0 || d >= int64(24*time.Hour/time.Second) {
		t.Errorf("epoch duration %ds, want (0, 24h) on the run chain", d)
	}
	if b := p.Params.MinBlocksPerEpoch.Int64(); b <= 0 || b >= 14400 {
		t.Errorf("min blocks per epoch %d, want (0, 14400)", b)
	}
	if gs := c.Epoch(t, n, 0).GenesisSupply; !gs.IsZero() {
		t.Errorf("genesis supply %s, want 0 (docs/CHAIN.md: a genesis starts at exactly zero supply)", gs.String())
	}
}

// TestEmission_mintedMatchesScheduleAndSupply: at three heights, one per
// epoch (each read at once: the run chain prunes state older than about 100
// blocks), cumulative_minted equals exactly 60% of the schedule over the
// completed epochs, and bank supply equals genesis + minted + development +
// service - burned (docs/CHAIN.md "Invariants"; only x/emission mints
// norama); cumulative-minted agrees with current-epoch.
func TestEmission_mintedMatchesScheduleAndSupply(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 0)
	for i := 0; i < 3; i++ {
		// One below the head: a height whose state every query can read.
		h := c.Height(t) - 1
		e := c.Epoch(t, n, h)
		completed := uint64(e.CurrentEpoch.Int64()) - 1
		if want := validatorMinted(completed); e.CumulativeMinted.Cmp(want) != 0 {
			t.Errorf("height %d (epoch %s): cumulative_minted %s, want %s", h, e.CurrentEpoch.String(), e.CumulativeMinted.String(), want.String())
		}
		want := e.GenesisSupply.Add(e.CumulativeMinted).Add(e.CumulativeDevelopmentMinted).Add(e.CumulativeServiceMinted).Sub(e.CumulativeBurned)
		if got := c.Supply(t, n, h); got.Cmp(want) != 0 {
			t.Errorf("height %d: bank supply %s, want %s", h, got.String(), want.String())
		}
		var cm struct {
			CumulativeMinted chain.Int `json:"cumulative_minted"`
			CumulativeBurned chain.Int `json:"cumulative_burned"`
		}
		c.QueryAt(t, n, h, &cm, "emission", "cumulative-minted")
		if cm.CumulativeMinted.Cmp(e.CumulativeMinted) != 0 || cm.CumulativeBurned.Cmp(e.CumulativeBurned) != 0 {
			t.Errorf("height %d: cumulative-minted %s/%s disagrees with current-epoch %s/%s", h,
				cm.CumulativeMinted.String(), cm.CumulativeBurned.String(), e.CumulativeMinted.String(), e.CumulativeBurned.String())
		}
		waitEpochAfter(t, c, e.CurrentEpoch)
	}
	c.RequireInvariants(t, "three epoch closes")
}

// TestEmission_epochClosesOnlyWhenTimeAndBlocksHold: the block that closes
// an epoch is the first at which BOTH at least epoch_duration has passed
// since the epoch started AND at least min_blocks_per_epoch blocks were
// counted (x/emission/keeper/epoch.go ShouldCloseEpoch); the block before it
// failed at least one of the two. The close mints exactly the closing
// epoch's validator share and starts the next epoch at the block's time with
// a zero block count.
func TestEmission_epochClosesOnlyWhenTimeAndBlocksHold(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 0)
	var p emissionParams
	c.Query(t, n, &p, "emission", "params")
	dur := time.Duration(p.Params.EpochDurationSeconds.Int64()) * time.Second
	minBlocks := p.Params.MinBlocksPerEpoch.Int64()
	// Historical reads stay one below the head, a height whose state every
	// query can read; hi is the head once the next block is committed.
	from := c.Height(t) - 1
	start := c.Epoch(t, n, from).CurrentEpoch
	waitEpochAfter(t, c, start)
	hi := c.Height(t)
	c.WaitHeight(t, hi+1)
	closeAt := firstHeightPast(t, c, from, hi, start)
	before, after := c.Epoch(t, n, closeAt-1), c.Epoch(t, n, closeAt)
	prevHdr, closeHdr := header(t, c, closeAt-1), header(t, c, closeAt)
	startedAt := time.Unix(0, before.EpochStartUnixNano.Int64())
	if closeHdr.Time.Sub(startedAt) < dur || before.BlocksInEpoch.Int64()+1 < minBlocks {
		t.Errorf("epoch %s closed at height %d after %v and %d blocks; needs %v and %d", start.String(), closeAt,
			closeHdr.Time.Sub(startedAt), before.BlocksInEpoch.Int64()+1, dur, minBlocks)
	}
	if prevHdr.Time.Sub(startedAt) >= dur && before.BlocksInEpoch.Int64() >= minBlocks {
		t.Errorf("height %d already met both conditions (%v, %d blocks) but the epoch closed only at %d", closeAt-1,
			prevHdr.Time.Sub(startedAt), before.BlocksInEpoch.Int64(), closeAt)
	}
	if after.EpochStartUnixNano.Int64() != closeHdr.Time.UnixNano() || !after.BlocksInEpoch.IsZero() {
		t.Errorf("epoch %s starts at %d with %s blocks, want the closing block's time %d and 0",
			after.CurrentEpoch.String(), after.EpochStartUnixNano.Int64(), after.BlocksInEpoch.String(), closeHdr.Time.UnixNano())
	}
	minted := after.CumulativeMinted.Sub(before.CumulativeMinted)
	if want := pct(chain.Orama(scheduleOrama(uint64(start.Int64()))), 60); minted.Cmp(want) != 0 {
		t.Errorf("closing epoch %s minted %s, want its validator share %s", start.String(), minted.String(), want.String())
	}
}

// firstHeightPast binary-searches (lo, hi] for the first height whose epoch
// is past epoch.
func firstHeightPast(t *testing.T, c *chain.Chain, lo, hi int64, epoch chain.Int) int64 {
	t.Helper()
	n := c.Node(t, 0)
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if c.Epoch(t, n, mid).CurrentEpoch.Cmp(epoch) > 0 {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

func header(t *testing.T, c *chain.Chain, h int64) chain.Header {
	t.Helper()
	hd, err := c.BlockHeader(t, c.Node(t, 0), h)
	if err != nil {
		t.Fatal(err)
	}
	return hd
}

// waitEpochAfter waits until x/emission's current epoch is past start.
func waitEpochAfter(t *testing.T, c *chain.Chain, start chain.Int) chain.EpochState {
	t.Helper()
	var e chain.EpochState
	eventually.Require(t, chain.PollEvery, chain.EpochBudget, "the epoch after "+start.String(), func() (bool, error) {
		e = c.Epoch(t, c.Node(t, 0), 0)
		if e.CurrentEpoch.Cmp(start) <= 0 {
			return false, fmt.Errorf("epoch %s", e.CurrentEpoch.String())
		}
		return true, nil
	})
	return e
}
