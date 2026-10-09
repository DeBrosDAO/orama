package indexer

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/cockroachdb/pebble"
)

// Key layout. Heights, ids and indices are big-endian so byte order is
// numeric order. Addresses are bech32 and never contain '/'.
const (
	keyStart   = "m/start"
	keyCursor  = "m/cursor"
	keyVersion = "m/version"
	pfxBlock   = "b/" // height
	pfxTx      = "t/" // 32-byte hash
	pfxAccount = "a/" // address "/" height index → 32-byte hash
	pfxAsset   = "c/a/"
	pfxOwner   = "c/o/" // owner "/" asset location → empty
	pfxTree    = "c/t/" // tree id → collection id
	pfxListing = "k/l/" // listing id → listing
	pfxBid     = "k/b/" // listing id, bid id → bidder
	pfxLatest  = "l/"   // height index → 32-byte hash, for the newest-first transaction list
	pfxHour    = "s/h/" // unix hour → hour statistics
	pfxSummary = "s/a/" // address → account summary
)

// schemaVersion is the layout of an index. An index written by another version
// is refused rather than served with fields it never recorded.
const schemaVersion uint64 = 2

// Store is the on-disk index. Reads are safe while the follower writes.
type Store struct {
	db *pebble.DB
}

// Open opens or creates the index in dir.
func Open(dir string) (*Store, error) {
	db, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to open index %s: %w", dir, err)
	}
	s := &Store{db: db}
	if err := s.checkVersion(); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return s, nil
}

// checkVersion stamps a new index with schemaVersion and refuses one that was
// written by another version.
func (s *Store) checkVersion() error {
	have, ok, err := getUint(s.db, []byte(keyVersion))
	if err != nil {
		return err
	}
	if ok {
		if have != schemaVersion {
			return fmt.Errorf("index has layout version %d, this indexer writes %d; index again into a new --home", have, schemaVersion)
		}
		return nil
	}
	if _, started, err := getUint(s.db, []byte(keyStart)); err != nil || started {
		if err != nil {
			return err
		}
		return fmt.Errorf("index was written before layout version %d; index again into a new --home", schemaVersion)
	}
	if err := s.db.Set([]byte(keyVersion), be64(schemaVersion), pebble.Sync); err != nil {
		return fmt.Errorf("failed to record index layout version: %w", err)
	}
	return nil
}

// Close flushes and closes the index.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("failed to close index: %w", err)
	}
	return nil
}

// Status reads the start height and the last indexed height. Both are zero
// on an index that has never been bound to a start height.
func (s *Store) Status() (Status, error) {
	start, _, err := getUint(s.db, []byte(keyStart))
	if err != nil {
		return Status{}, err
	}
	cursor, _, err := getUint(s.db, []byte(keyCursor))
	if err != nil {
		return Status{}, err
	}
	return Status{StartHeight: int64(start), Cursor: int64(cursor)}, nil
}

// bindStart records the start height of a new index. An index that already
// has one refuses a different value: its cursor and its account and cNFT
// state were built from that height, and silently moving it would leave a
// gap or a replay.
func (s *Store) bindStart(start int64) error {
	have, ok, err := getUint(s.db, []byte(keyStart))
	if err != nil {
		return err
	}
	if ok {
		if int64(have) != start {
			return fmt.Errorf("index was started from height %d, not %d; use a new --home to index from another height", have, start)
		}
		return nil
	}
	if err := s.db.Set([]byte(keyStart), be64(uint64(start)), pebble.Sync); err != nil {
		return fmt.Errorf("failed to record start height: %w", err)
	}
	return nil
}

type getter interface {
	Get(key []byte) ([]byte, io.Closer, error)
}

func getRaw(g getter, key []byte) ([]byte, bool, error) {
	v, closer, err := g.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to read index key %q: %w", key, err)
	}
	out := append([]byte(nil), v...)
	if err := closer.Close(); err != nil {
		return nil, false, fmt.Errorf("failed to release index key %q: %w", key, err)
	}
	return out, true, nil
}

func getUint(g getter, key []byte) (uint64, bool, error) {
	v, ok, err := getRaw(g, key)
	if err != nil || !ok {
		return 0, ok, err
	}
	if len(v) != 8 {
		return 0, false, fmt.Errorf("index key %q holds %d bytes, want 8", key, len(v))
	}
	return binary.BigEndian.Uint64(v), true, nil
}

func getJSON(g getter, key []byte, out any) (bool, error) {
	v, ok, err := getRaw(g, key)
	if err != nil || !ok {
		return ok, err
	}
	if err := json.Unmarshal(v, out); err != nil {
		return false, fmt.Errorf("failed to decode index key %q: %w", key, err)
	}
	return true, nil
}

func be64(v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return b[:]
}

func be32(v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return b[:]
}

func join(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func blockKey(height int64) []byte { return join([]byte(pfxBlock), be64(uint64(height))) }
func txKey(hash []byte) []byte     { return join([]byte(pfxTx), hash) }
func treeKey(id uint64) []byte     { return join([]byte(pfxTree), be64(id)) }
func listingKey(id uint64) []byte  { return join([]byte(pfxListing), be64(id)) }
func bidPrefix(listing uint64) []byte {
	return join([]byte(pfxBid), be64(listing))
}
func bidKey(listing, bid uint64) []byte { return join(bidPrefix(listing), be64(bid)) }

func latestKey(height int64, index uint32) []byte {
	return join([]byte(pfxLatest), be64(uint64(height)), be32(index))
}
func hourKey(hour int64) []byte        { return join([]byte(pfxHour), be64(uint64(hour))) }
func summaryKey(addr string) []byte    { return []byte(pfxSummary + addr) }
func accountPrefix(addr string) []byte { return []byte(pfxAccount + addr + "/") }
func accountKey(addr string, height int64, index uint32) []byte {
	return join(accountPrefix(addr), be64(uint64(height)), be32(index))
}

// location is an asset id plus the tree and leaf that hold it.
func location(id []byte, tree uint64, leaf uint32) []byte { return join(id, be64(tree), be32(leaf)) }
func assetPrefix(id []byte) []byte                        { return join([]byte(pfxAsset), id) }
func assetKey(loc []byte) []byte                          { return join([]byte(pfxAsset), loc) }
func ownerPrefix(owner string) []byte                     { return []byte(pfxOwner + owner + "/") }
func ownerKey(owner string, loc []byte) []byte            { return join(ownerPrefix(owner), loc) }

// prefixEnd is the smallest key greater than every key that starts with p.
func prefixEnd(p []byte) []byte {
	end := append([]byte(nil), p...)
	for i := len(end) - 1; i >= 0; i-- {
		end[i]++
		if end[i] != 0 {
			return end[:i+1]
		}
	}
	return nil
}
