package indexer

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"
)

// writer is one block's writes. It reads its own writes, so a later
// transaction in the block sees what an earlier one did, and it commits with
// the cursor in one synced batch: a crash leaves the index at the previous
// block, never half of one.
type writer struct {
	b *pebble.Batch
}

func (s *Store) newWriter() *writer { return &writer{b: s.db.NewIndexedBatch()} }

func (w *writer) commit(height int64) error {
	if err := w.b.Set([]byte(keyCursor), be64(uint64(height)), nil); err != nil {
		return fmt.Errorf("failed to stage cursor %d: %w", height, err)
	}
	if err := w.b.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("failed to commit block %d to the index: %w", height, err)
	}
	return w.close()
}

func (w *writer) close() error {
	if err := w.b.Close(); err != nil {
		return fmt.Errorf("failed to release index batch: %w", err)
	}
	return nil
}

func (w *writer) set(key, value []byte) error {
	if err := w.b.Set(key, value, nil); err != nil {
		return fmt.Errorf("failed to stage index key %q: %w", key, err)
	}
	return nil
}

func (w *writer) del(key []byte) error {
	if err := w.b.Delete(key, nil); err != nil {
		return fmt.Errorf("failed to stage delete of index key %q: %w", key, err)
	}
	return nil
}

func (w *writer) setJSON(key []byte, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to encode index key %q: %w", key, err)
	}
	return w.set(key, raw)
}

func (w *writer) putBlock(b Block) error { return w.setJSON(blockKey(b.Height), b) }

// putTxOnce writes a transaction unless a successful one with the same hash
// is already indexed. A proposer can include the same bytes again; the copy
// fails on its sequence and must not replace the record that ran. This is
// what CometBFT's own kv indexer does.
func (w *writer) putTxOnce(hash []byte, t Tx) error {
	if t.Code != 0 {
		var prev Tx
		ok, err := getJSON(w.b, txKey(hash), &prev)
		if err != nil {
			return err
		}
		if ok && prev.Code == 0 {
			return nil
		}
	}
	return w.setJSON(txKey(hash), t)
}

func (w *writer) addAccountTx(addr string, height int64, index uint32, hash []byte) error {
	return w.set(accountKey(addr, height, index), hash)
}

func (w *writer) asset(loc []byte) (Asset, bool, error) {
	var a Asset
	ok, err := getJSON(w.b, assetKey(loc), &a)
	return a, ok, err
}

// putAsset writes the record at loc and moves its owner index entry. A
// burned asset has no owner entry.
func (w *writer) putAsset(loc []byte, a Asset) error {
	if err := w.dropOwner(loc); err != nil {
		return err
	}
	if err := w.setJSON(assetKey(loc), a); err != nil {
		return err
	}
	if a.State == AssetBurned {
		return nil
	}
	return w.set(ownerKey(a.Owner, loc), nil)
}

// deleteAsset removes the record at loc, for an asset that moved to a new leaf.
func (w *writer) deleteAsset(loc []byte) error {
	if err := w.dropOwner(loc); err != nil {
		return err
	}
	return w.del(assetKey(loc))
}

func (w *writer) dropOwner(loc []byte) error {
	prev, ok, err := w.asset(loc)
	if err != nil || !ok || prev.State == AssetBurned {
		return err
	}
	return w.del(ownerKey(prev.Owner, loc))
}

// decompressed finds the decompressed record of the asset id. x/cnft keeps
// at most one decompressed asset per id.
func (w *writer) decompressed(id []byte) ([]byte, Asset, bool, error) {
	prefix := assetPrefix(id)
	it, err := w.b.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return nil, Asset{}, false, fmt.Errorf("failed to open index iterator: %w", err)
	}
	for ok := it.First(); ok; ok = it.Next() {
		var a Asset
		if err := json.Unmarshal(it.Value(), &a); err != nil {
			return nil, Asset{}, false, errors.Join(fmt.Errorf("failed to decode asset %x: %w", id, err), closeIter(it))
		}
		if a.State == AssetDecompressed {
			loc := append([]byte(nil), it.Key()[len(pfxAsset):]...)
			return loc, a, true, closeIter(it)
		}
	}
	return nil, Asset{}, false, closeIter(it)
}

func (w *writer) treeCollection(tree uint64) (uint64, bool, error) {
	return getUint(w.b, treeKey(tree))
}

func (w *writer) putTree(tree, collection uint64) error {
	return w.set(treeKey(tree), be64(collection))
}

// listing is what the index keeps of an x/market listing: the tree its leaf
// sits in.
type listing struct {
	TreeID uint64 `json:"tree_id"`
}

func (w *writer) listing(id uint64) (listing, bool, error) {
	var l listing
	ok, err := getJSON(w.b, listingKey(id), &l)
	return l, ok, err
}

func (w *writer) putListing(id uint64, l listing) error { return w.setJSON(listingKey(id), l) }

// deleteListing drops a listing and every bid on it. x/market refunds the
// other bids when a listing settles or is cancelled.
func (w *writer) deleteListing(id uint64) error {
	if err := w.del(listingKey(id)); err != nil {
		return err
	}
	prefix := bidPrefix(id)
	if err := w.b.DeleteRange(prefix, prefixEnd(prefix), nil); err != nil {
		return fmt.Errorf("failed to stage delete of bids on listing %d: %w", id, err)
	}
	return nil
}

func (w *writer) bidder(listingID, bidID uint64) (string, bool, error) {
	v, ok, err := getRaw(w.b, bidKey(listingID, bidID))
	return string(v), ok, err
}

func (w *writer) putBid(listingID, bidID uint64, bidder string) error {
	return w.set(bidKey(listingID, bidID), []byte(bidder))
}

func (w *writer) deleteBid(listingID, bidID uint64) error {
	return w.del(bidKey(listingID, bidID))
}
