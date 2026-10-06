package archiver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/archive/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// dealState is what this archiver keeps for a range that is attested but not yet archived: the
// ids of the ARCHIVE deals it opened that x/archive does not record yet. A deal is OPEN until
// x/storage gives it a provider in the next block, and only then can MsgAttachReplicas record it.
type dealState struct {
	DealIDs []uint64 `json:"deal_ids"`
	// Uploaded lists the "<deal>/<slot>/<node>" slots whose provider took the bundle, so a
	// restart does not send it again.
	Uploaded []string `json:"uploaded,omitempty"`
}

func dealStatePath(dir string, start, end int64) string {
	return filepath.Join(dir, "deals", fmt.Sprintf("%d-%d.json", start, end))
}

func loadDealState(path string) (dealState, error) {
	var st dealState
	body, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(body, &st); err != nil {
		return dealState{}, fmt.Errorf("read %s: %w", path, err)
	}
	return st, nil
}

func saveDealState(path string, st dealState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create deal directory: %w", err)
	}
	body, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeAtomic(path, body, 0o640)
}

// track starts following a range until x/archive marks it archived.
func (r *Runner) track(start, end int64) error {
	path := dealStatePath(r.dir, start, end)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return saveDealState(path, dealState{})
}

// pendingRanges lists the tracked ranges, oldest first.
func (r *Runner) pendingRanges() ([][2]int64, error) {
	entries, err := os.ReadDir(filepath.Join(r.dir, "deals"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out [][2]int64
	for _, e := range entries {
		var start, end int64
		if _, err := fmt.Sscanf(e.Name(), "%d-%d.json", &start, &end); err == nil {
			out = append(out, [2]int64{start, end})
		}
	}
	slices.SortFunc(out, func(a, b [2]int64) int { return int(a[0] - b[0]) })
	return out, nil
}

// advanceDeals moves every tracked range one step toward archived: it opens the ARCHIVE deals the
// range still lacks, then records the ones x/storage has given a provider. It returns how many
// ranges are still not archived.
func (r *Runner) advanceDeals(ctx context.Context) (int, error) {
	ranges, err := r.pendingRanges()
	if err != nil {
		return 0, err
	}
	var errs []error
	open := 0
	for _, rng := range ranges {
		archived, err := r.advanceRange(ctx, rng[0], rng[1])
		if err != nil {
			errs = append(errs, fmt.Errorf("range %d-%d: %w", rng[0], rng[1], err))
		}
		if !archived {
			open++
		}
	}
	return open, errors.Join(errs...)
}

func (r *Runner) advanceRange(ctx context.Context, start, end int64) (bool, error) {
	path := dealStatePath(r.dir, start, end)
	rec, found, err := r.chain.Range(ctx, start, end)
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("range is tracked but not attested on chain")
	}
	if rec.Archived {
		return true, os.Remove(path)
	}
	if !rec.Decided {
		// No tuple has been attested by enough operators yet: deals open only for the winner.
		return false, nil
	}
	if lost, err := r.lostRange(rec); lost || err != nil {
		return lost, err
	}
	st, err := loadDealState(path)
	if err != nil {
		return false, err
	}
	st.DealIDs, err = r.liveDeals(ctx, rec, st.DealIDs)
	if err != nil {
		return false, err
	}
	if err := r.openDeals(ctx, rec, &st); err != nil {
		return false, errors.Join(err, saveDealState(path, st))
	}
	if err := r.uploadDeals(ctx, rec, &st); err != nil {
		return false, errors.Join(err, saveDealState(path, st))
	}
	recorded, err := r.recordDeals(ctx, rec, &st)
	if err != nil {
		return false, errors.Join(err, saveDealState(path, st))
	}
	if recorded {
		// The attach may have completed the quorum; report that now, not a pass later.
		if rec, found, err = r.chain.Range(ctx, start, end); err != nil {
			return false, errors.Join(err, saveDealState(path, st))
		}
		if found && rec.Archived {
			return true, os.Remove(path)
		}
	}
	return false, saveDealState(path, st)
}

// lostRange reports whether another tuple won the range this archiver attested. It has nothing to
// open deals for then: it stops following the range, and records the two tuples once. An archiver
// whose bundle is the winner, or that never attested the range, is not affected.
func (r *Runner) lostRange(rec types.RangeRecord) (bool, error) {
	if _, attested := rec.AttestedBy(r.archiver); attested {
		return false, nil
	}
	body, err := os.ReadFile(BundlePath(r.dir, rec.StartHeight, rec.EndHeight))
	if err != nil {
		return false, fmt.Errorf("read the bundle: %w", err)
	}
	blocks, err := Decode(body)
	if err != nil {
		return false, err
	}
	hashes := make([][]byte, len(blocks))
	for i, b := range blocks {
		hashes[i] = b.Hash
	}
	bundle, err := Pack(rec.StartHeight, hashes, body)
	if err != nil {
		return false, err
	}
	local, err := tupleOf(bundle, body)
	if err != nil {
		return false, err
	}
	if local.Equal(rec.Winner()) {
		return false, nil
	}
	return true, errors.Join(r.recordConflict(rec.StartHeight, rec.EndHeight, local, rec.Winner()), os.Remove(dealStatePath(r.dir, rec.StartHeight, rec.EndHeight)))
}

// liveDeals keeps the deals x/archive does not record yet and x/storage still runs. An ended
// deal is dropped so the range can be given a new one.
func (r *Runner) liveDeals(ctx context.Context, rec types.RangeRecord, ids []uint64) ([]uint64, error) {
	var live []uint64
	for _, id := range ids {
		if slices.Contains(rec.DealIds, strconv.FormatUint(id, 10)) {
			continue
		}
		status, found, err := r.chain.DealStatus(ctx, id)
		if err != nil {
			return nil, err
		}
		if found && (status == storagetypes.DealStatus_DEAL_STATUS_OPEN || status == storagetypes.DealStatus_DEAL_STATUS_ACTIVE) {
			live = append(live, id)
		}
	}
	return live, nil
}

// openDeals creates the ARCHIVE deals a range still lacks. Other archivers of the range do the
// same, so the chain may refuse one as over the range's allowance; that means the range has
// enough deals and is not an error.
func (r *Runner) openDeals(ctx context.Context, rec types.RangeRecord, st *dealState) error {
	// The chain opens deals only for the tuple that won the range; before that this archiver
	// waits for the others instead of sending a message that would be refused.
	if !rec.Decided {
		return nil
	}
	need := types.MaxLiveDealsPerRange - len(rec.DealIds) - len(st.DealIDs)
	if need <= 0 {
		return nil
	}
	body, err := os.ReadFile(BundlePath(r.dir, rec.StartHeight, rec.EndHeight))
	if err != nil {
		return fmt.Errorf("read the bundle: %w", err)
	}
	pc, err := piece.Commit(body)
	if err != nil {
		return fmt.Errorf("commit the bundle: %w", err)
	}
	if !rec.PieceOf().Equal(types.Piece{Root: pc.Root, RealLeafCount: pc.RealLeafCount, PaddedLeafCount: pc.PaddedLeafCount, PieceBytes: uint64(len(body))}) {
		return fmt.Errorf("the bundle on disk does not match the piece commitment of the winning tuple of range %d-%d", rec.StartHeight, rec.EndHeight)
	}
	for ; need > 0; need-- {
		id, err := r.chain.CreateArchiveDeal(ctx, &types.MsgCreateArchiveDeal{
			Archiver: r.archiver, NodeId: r.nodeID, StartHeight: rec.StartHeight, EndHeight: rec.EndHeight,
			PieceRoot: pc.Root, RealLeafCount: pc.RealLeafCount, PaddedLeafCount: pc.PaddedLeafCount, PieceBytes: uint64(len(body)),
		})
		if err != nil {
			if strings.Contains(err.Error(), types.ErrDealsFull.Error()) {
				return nil
			}
			return err
		}
		st.DealIDs = append(st.DealIDs, id)
		r.dealsOpened++
	}
	return nil
}

// recordDeals attaches the deals that have a provider now to the range. It reports whether it
// recorded any.
func (r *Runner) recordDeals(ctx context.Context, rec types.RangeRecord, st *dealState) (bool, error) {
	var ready []string
	var rest []uint64
	for _, id := range st.DealIDs {
		status, found, err := r.chain.DealStatus(ctx, id)
		if err != nil {
			return false, err
		}
		if found && status == storagetypes.DealStatus_DEAL_STATUS_ACTIVE {
			ready = append(ready, strconv.FormatUint(id, 10))
		} else {
			rest = append(rest, id)
		}
	}
	if len(ready) == 0 {
		return false, nil
	}
	if err := r.chain.Submit(ctx, &types.MsgAttachReplicas{
		Archiver: r.archiver, NodeId: r.nodeID, StartHeight: rec.StartHeight, EndHeight: rec.EndHeight, DealIds: ready,
	}); err != nil {
		return false, err
	}
	st.DealIDs = rest
	return true, nil
}
