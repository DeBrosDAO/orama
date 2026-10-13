package indexer

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"
)

// Block returns the indexed block at height.
func (s *Store) Block(height int64) (Block, bool, error) {
	var b Block
	ok, err := getJSON(s.db, blockKey(height), &b)
	return b, ok, err
}

// Blocks returns one page of the indexed blocks, newest first, without their transaction hashes.
// page starts at 1.
func (s *Store) Blocks(page, limit int) ([]BlockSummary, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	return blockPage(snap, page, limit)
}

// BlocksAfter returns one page of the indexed blocks older than the height of after, newest first,
// without their transaction hashes.
func (s *Store) BlocksAfter(after Cursor, limit int) ([]BlockSummary, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	cursor, _, err := getUint(snap, []byte(keyCursor))
	if err != nil {
		return nil, err
	}
	return blocksFrom(snap, min(after.Height-1, int64(cursor)), cursor, limit)
}

// blockPage reads a page of blocks by height. The index holds every block from its start height to
// its cursor, so the page begins at a height worked out from the page number and is read by seeking
// there, not by skipping the blocks before it: the cost of the last page is that of the first.
func blockPage(db iterable, page, limit int) ([]BlockSummary, error) {
	cursor, _, err := getUint(db, []byte(keyCursor))
	if err != nil {
		return nil, err
	}
	return blocksFrom(db, int64(cursor)-int64(page-1)*int64(limit), cursor, limit)
}

// blocksFrom reads up to limit blocks, newest first, from height top down. cursor is the index's
// cursor, which the caller has read from the same snapshot.
func blocksFrom(db iterable, top int64, cursor uint64, limit int) ([]BlockSummary, error) {
	start, _, err := getUint(db, []byte(keyStart))
	if err != nil {
		return nil, err
	}
	out := make([]BlockSummary, 0, limit)
	if top < int64(start) || top < 1 {
		return out, nil
	}
	it, err := db.NewIter(&pebble.IterOptions{LowerBound: []byte(pfxBlock), UpperBound: prefixEnd([]byte(pfxBlock))})
	if err != nil {
		return nil, fmt.Errorf("failed to open index iterator: %w", err)
	}
	next := top
	for ok := it.SeekLT(blockKey(top + 1)); ok && len(out) < limit && next >= int64(start); ok = it.Prev() {
		var b Block
		if err := json.Unmarshal(it.Value(), &b); err != nil {
			return nil, errors.Join(fmt.Errorf("failed to decode block %d: %w", next, err), closeIter(it))
		}
		if b.Height != next {
			return nil, errors.Join(fmt.Errorf("index holds blocks %d to %d but not block %d", start, cursor, next), closeIter(it))
		}
		out = append(out, b.summary())
		next--
	}
	if err := closeIter(it); err != nil {
		return nil, err
	}
	if len(out) < limit && next >= int64(start) && next >= 1 {
		return nil, fmt.Errorf("index holds blocks %d to %d but not block %d", start, cursor, next)
	}
	return out, nil
}

// Tx returns the indexed transaction with this 32-byte hash.
func (s *Store) Tx(hash []byte) (Tx, bool, error) {
	var t Tx
	ok, err := getJSON(s.db, txKey(hash), &t)
	return t, ok, err
}

// maxAssetRecords bounds the records one asset id answers with. The chain
// lets a creator mint the same id again, so the count is not fixed at one.
const maxAssetRecords = 100

// Assets returns the records of the asset id, in tree and leaf order.
func (s *Store) Assets(id []byte) ([]Asset, error) {
	rows, err := scan(s.db, assetPrefix(id), false, 1, maxAssetRecords)
	if err != nil {
		return nil, err
	}
	out := make([]Asset, 0, len(rows))
	for _, row := range rows {
		var a Asset
		if err := json.Unmarshal(row.value, &a); err != nil {
			return nil, fmt.Errorf("failed to decode asset %x: %w", id, err)
		}
		out = append(out, a)
	}
	return out, nil
}

// OwnerAssets returns one page of the assets addr holds, compressed or
// decompressed, in asset id order. Burned assets are not held by anyone.
func (s *Store) OwnerAssets(addr string, page, limit int) ([]Asset, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	rows, err := scan(snap, ownerPrefix(addr), false, page, limit)
	if err != nil {
		return nil, err
	}
	return heldAssets(snap, addr, rows)
}

// OwnerAssetsAfter is OwnerAssets from the assets after the position of after.
func (s *Store) OwnerAssetsAfter(addr string, after AssetCursor, limit int) ([]Asset, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	rows, err := scanAfter(snap, ownerPrefix(addr), after.bound(), limit)
	if err != nil {
		return nil, err
	}
	return heldAssets(snap, addr, rows)
}

// heldAssets reads the records the owner index rows name, from the snapshot the rows came from.
func heldAssets(snap iterable, addr string, rows []entry) ([]Asset, error) {
	out := make([]Asset, 0, len(rows))
	for _, row := range rows {
		loc := row.suffix
		var a Asset
		ok, err := getJSON(snap, assetKey(loc), &a)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("owner index of %s names asset %x that the index does not hold", addr, loc)
		}
		out = append(out, a)
	}
	return out, nil
}

// entry is one index row under a prefix: the key after the prefix, and the value.
type entry struct {
	suffix []byte
	value  []byte
}

// iterable is a DB or a snapshot of it. Multi-key reads use a snapshot so a
// block committed between the scan and the point reads cannot tear them.
type iterable interface {
	getter
	NewIter(o *pebble.IterOptions) (*pebble.Iterator, error)
}

// scan returns one page of the rows under prefix, in key order or reversed.
// page starts at 1.
func scan(db iterable, prefix []byte, reverse bool, page, limit int) ([]entry, error) {
	it, err := db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return nil, fmt.Errorf("failed to open index iterator: %w", err)
	}
	skip := (page - 1) * limit
	var out []entry
	first, step := it.First, it.Next
	if reverse {
		first, step = it.Last, it.Prev
	}
	for ok := first(); ok && len(out) < limit; ok = step() {
		if skip > 0 {
			skip--
			continue
		}
		out = append(out, entry{
			suffix: append([]byte(nil), it.Key()[len(prefix):]...),
			value:  append([]byte(nil), it.Value()...),
		})
	}
	return out, closeIter(it)
}

// scanBefore returns up to limit rows under prefix, in reverse key order, whose key after the prefix
// is below bound: the page that follows a cursor, found by seeking, not by skipping the rows before.
func scanBefore(db iterable, prefix, bound []byte, limit int) ([]entry, error) {
	it, err := db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: join(prefix, bound)})
	if err != nil {
		return nil, fmt.Errorf("failed to open index iterator: %w", err)
	}
	var out []entry
	for ok := it.Last(); ok && len(out) < limit; ok = it.Prev() {
		out = append(out, entry{
			suffix: append([]byte(nil), it.Key()[len(prefix):]...),
			value:  append([]byte(nil), it.Value()...),
		})
	}
	return out, closeIter(it)
}

// scanAfter returns up to limit rows under prefix, in key order, whose key after the prefix is above
// bound: the page that follows a cursor, found by seeking, not by skipping the rows before.
func scanAfter(db iterable, prefix, bound []byte, limit int) ([]entry, error) {
	it, err := db.NewIter(&pebble.IterOptions{LowerBound: join(prefix, bound, []byte{0}), UpperBound: prefixEnd(prefix)})
	if err != nil {
		return nil, fmt.Errorf("failed to open index iterator: %w", err)
	}
	var out []entry
	for ok := it.First(); ok && len(out) < limit; ok = it.Next() {
		out = append(out, entry{
			suffix: append([]byte(nil), it.Key()[len(prefix):]...),
			value:  append([]byte(nil), it.Value()...),
		})
	}
	return out, closeIter(it)
}

func closeIter(it *pebble.Iterator) error {
	if err := it.Close(); err != nil {
		return fmt.Errorf("index iterator failed: %w", err)
	}
	return nil
}
