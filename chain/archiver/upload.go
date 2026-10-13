package archiver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/archive/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

const (
	// uploadAttempts bounds the tries one slot gets in one pass. A provider refuses a root with
	// a 403 until its runner has read the assignment, which takes a block or two; the next pass
	// tries again for as long as the chain still assigns the slot to the same node.
	uploadAttempts = 4
	// uploadFirstBackoff doubles after every failed try.
	uploadFirstBackoff = time.Second
)

// Uploader sends piece bytes to a provider's HTTP root: POST <base>/pieces/<hex root>.
// repair.HTTP over repair.PublicHTTPClient is the production one: it dials public addresses
// only and follows no redirects.
type Uploader interface {
	Upload(ctx context.Context, base string, root, body []byte) error
}

// uploadKey names one slot held by one node. A slot the chain moves to another node is a new key,
// so the bytes go to the new provider too.
func uploadKey(dealID uint64, slot uint32, nodeID string) string {
	return strconv.FormatUint(dealID, 10) + "/" + strconv.FormatUint(uint64(slot), 10) + "/" + nodeID
}

// uploadDeals sends the bundle to the provider of every slot of the range's live deals that is
// assigned and not yet accepted. The provider cannot prove a piece it never received, so a slot
// nobody uploads to is evicted and the deal fails. It checks the piece root the chain assigned
// against the bundle before any byte leaves this machine.
func (r *Runner) uploadDeals(ctx context.Context, rec types.RangeRecord, st *dealState) error {
	if len(st.DealIDs) == 0 {
		return nil
	}
	var body []byte
	var commit piece.Commitment
	var errs []error
	for _, id := range st.DealIDs {
		slots, err := r.chain.DealSlots(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("deal %d: %w", id, err))
			continue
		}
		for _, slot := range slots {
			key := uploadKey(id, slot.Index, slot.NodeId)
			if slot.NodeId == "" || slot.Status != storagetypes.SlotStatus_SLOT_STATUS_ASSIGNED || slot.Accepted ||
				slices.Contains(st.Uploaded, key) {
				continue
			}
			if body == nil {
				if body, commit, err = r.readBundle(rec); err != nil {
					return errors.Join(append(errs, err)...)
				}
			}
			if err := r.uploadSlot(ctx, slot, body, commit); err != nil {
				errs = append(errs, fmt.Errorf("deal %d slot %d to %s: %w", id, slot.Index, slot.NodeId, err))
				continue
			}
			st.Uploaded = append(st.Uploaded, key)
			r.piecesUploaded++
		}
	}
	return errors.Join(errs...)
}

func (r *Runner) readBundle(rec types.RangeRecord) ([]byte, piece.Commitment, error) {
	body, err := os.ReadFile(BundlePath(r.dir, rec.StartHeight, rec.EndHeight))
	if err != nil {
		return nil, piece.Commitment{}, fmt.Errorf("read the bundle: %w", err)
	}
	commit, err := piece.Commit(body)
	if err != nil {
		return nil, piece.Commitment{}, fmt.Errorf("commit the bundle: %w", err)
	}
	return body, commit, nil
}

func (r *Runner) uploadSlot(ctx context.Context, slot storagetypes.Slot, body []byte, commit piece.Commitment) error {
	if !bytes.Equal(slot.PieceRoot, commit.Root) {
		r.uploadFailures++
		return fmt.Errorf("the chain assigned piece root %x but the bundle's root is %x; nothing was sent", slot.PieceRoot, commit.Root)
	}
	base, err := r.chain.ProviderURL(ctx, slot.NodeId)
	if err != nil {
		r.uploadFailures++
		return err
	}
	backoff := uploadFirstBackoff
	for attempt := 1; ; attempt++ {
		err = r.uploader.Upload(ctx, base, slot.PieceRoot, body)
		if err == nil {
			return nil
		}
		if attempt == uploadAttempts || ctx.Err() != nil {
			r.uploadFailures++
			return err
		}
		if err := r.sleep(ctx, backoff); err != nil {
			return err
		}
		backoff *= 2
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
