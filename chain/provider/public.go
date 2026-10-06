package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// Pinner is the public Kubo the provider pins public pieces through.
type Pinner interface {
	Add(ctx context.Context, data []byte) (string, error)
	Pin(ctx context.Context, cid string) error
	Unpin(ctx context.Context, cid string) error
}

// ReasonPrivateRoot is the decline reason for a public deal whose piece root
// a PRIVATE deal's slot already holds here: that ciphertext is never published.
const ReasonPrivateRoot = "root held for a private deal"

// isPublicClass is the deal classes whose bytes anyone must be able to fetch
// by CID. A PRIVATE deal's ciphertext never reaches the public Kubo.
func isPublicClass(c types.DealClass) bool {
	return c == types.DealClass_DEAL_CLASS_PUBLIC_PIN || c == types.DealClass_DEAL_CLASS_ARCHIVE
}

// pinPublic makes a stored piece of a public deal fetchable by CID: it is
// pinned in the public Kubo under every CID it was fetched by through
// POST /pins, and, when it has none, under the CIDv1 (raw leaves) Kubo makes of
// the bytes. It returns a decline reason instead of an error when the
// operator's denylist names a CID, or when a PRIVATE deal's slot holds the same
// root on this node. A slot of a private class instead unpublishes the piece:
// its ciphertext is never left in the public Kubo. A node with no public Kubo
// configured does nothing.
func (r *Runner) pinPublic(ctx context.Context, dealID uint64, slot uint32, root []byte) (string, error) {
	if r.pins == nil {
		return "", nil
	}
	deal, err := r.chain.Deal(ctx, dealID)
	if err != nil {
		return "", fmt.Errorf("read deal %d: %w", dealID, err)
	}
	name, ok, err := r.store.FindByRoot(root)
	if err != nil || !ok {
		return "", err
	}
	if !isPublicClass(deal.Class) {
		return "", r.store.Unpublish(name)
	}
	if held, err := r.heldPrivately(ctx, name, dealID, slot); err != nil || held {
		if err == nil {
			return ReasonPrivateRoot, nil
		}
		return "", err
	}
	pins, err := r.store.IPFSPins(name)
	if err != nil {
		return "", err
	}
	for _, p := range pins {
		if r.store.Denied(p.CID) {
			return ReasonDenylist, nil
		}
	}
	if len(pins) == 0 {
		data, err := r.store.ReadPiece(name)
		if err != nil {
			return "", err
		}
		cid, err := r.pins.Add(ctx, data)
		if err != nil {
			return "", err
		}
		// Record the CID before anything else can fail, so a later unpin
		// (release, discard) knows what to remove.
		if err := r.store.AddIPFS(name, cid); err != nil {
			return "", errors.Join(err, r.pins.Unpin(ctx, cid))
		}
		if r.store.Denied(cid) {
			return ReasonDenylist, nil
		}
		return "", r.store.MarkPinned(name, cid)
	}
	for _, p := range pins {
		if p.Pinned {
			continue
		}
		if err := r.pins.Pin(ctx, p.CID); err != nil {
			return "", err
		}
		if err := r.store.MarkPinned(name, p.CID); err != nil {
			return "", err
		}
	}
	return "", nil
}

// heldPrivately reports whether another slot on this node that holds or waits
// for the piece belongs to a deal of a non-public class. It reads the class
// from the chain for every bound slot and takes the class recorded for each
// waiting one, so it does not depend on the order the slots are decided in or
// on a mark an earlier build never wrote. A waiting slot whose class is not
// read yet is an error: the caller retries once it is.
func (r *Runner) heldPrivately(ctx context.Context, name string, dealID uint64, slot uint32) (bool, error) {
	bound, err := r.store.Assignments()
	if err != nil {
		return false, err
	}
	for _, a := range bound {
		if a.CID != name || (a.DealID == dealID && a.Slot == slot) {
			continue
		}
		deal, err := r.chain.Deal(ctx, a.DealID)
		if err != nil {
			return false, fmt.Errorf("read deal %d: %w", a.DealID, err)
		}
		if !isPublicClass(deal.Class) {
			return true, nil
		}
	}
	return r.pendingPrivately(name, dealID, slot)
}

// pendingPrivately is heldPrivately for the slots still waiting for the piece.
func (r *Runner) pendingPrivately(name string, dealID uint64, slot uint32) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.state.Pending {
		if p.Root != name || (p.DealID == dealID && p.Slot == slot) {
			continue
		}
		switch class := types.DealClass(p.Class); {
		case class == types.DealClass_DEAL_CLASS_UNSPECIFIED:
			return false, fmt.Errorf("the class of deal %d, which waits for the same piece, is not read yet", p.DealID)
		case !isPublicClass(class):
			return true, nil
		}
	}
	return false, nil
}
