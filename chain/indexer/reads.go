package indexer

import (
	"encoding/json"
	"fmt"

	"github.com/cockroachdb/pebble"
)

// Block returns the indexed block at height.
func (s *Store) Block(height int64) (Block, bool, error) {
	var b Block
	ok, err := getJSON(s.db, blockKey(height), &b)
	return b, ok, err
}

// Tx returns the indexed transaction with this 32-byte hash.
func (s *Store) Tx(hash []byte) (Tx, bool, error) {
	var t Tx
	ok, err := getJSON(s.db, txKey(hash), &t)
	return t, ok, err
}

// AccountTxs returns one page of the transactions whose events name addr,
// newest first. page starts at 1.
func (s *Store) AccountTxs(addr string, page, limit int) ([]Tx, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	rows, err := scan(snap, accountPrefix(addr), true, page, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Tx, 0, len(rows))
	for _, row := range rows {
		h := row.value
		var t Tx
		ok, err := getJSON(snap, txKey(h), &t)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("index names transaction %x for %s but does not hold it", h, addr)
		}
		out = append(out, t)
	}
	return out, nil
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

func closeIter(it *pebble.Iterator) error {
	if err := it.Close(); err != nil {
		return fmt.Errorf("index iterator failed: %w", err)
	}
	return nil
}
