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
// operator's denylist names a CID, or the piece is held for a PRIVATE deal. A
// deal of a private class, or a node with no public Kubo configured, does
// nothing to the piece except mark a private one private.
func (r *Runner) pinPublic(ctx context.Context, dealID uint64, root []byte) (string, error) {
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
		return "", r.store.MarkPrivate(name)
	}
	if private, err := r.store.IsPrivate(name); err != nil || private {
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
