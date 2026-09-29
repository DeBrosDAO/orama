package nullifier

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	dbm "github.com/cosmos/cosmos-db"
)

// nodeLen is the size of a nullifier.
const nodeLen = 32

// The two key spaces of the store:
//
//	'n' || nullifier                 -> height (u64 BE)      the index
//	'h' || height (u64 BE) || seq (u32 BE) -> nullifier      the append-only log, in insertion order
const (
	indexPrefix = 'n'
	logPrefix   = 'h'
)

// ErrCorrupt means the store disagrees with itself or with what the caller inserted.
var ErrCorrupt = errors.New("nullifier store is inconsistent")

// Store is the dedicated nullifier database that lives outside IAVL. The app hash commits to it
// through the running accumulator kept in module state (see x/shielded/keeper); this store holds
// the set itself. A record at height h is visible to a reader at height asOf when h < asOf, so
// after a crash, a replayed block or a rollback the store never shows a block its own records.
type Store struct {
	db dbm.DB
	// imported counts the records Import has written, so its log keys keep the order it was given.
	imported uint32
}

// NewStore wraps an open database.
func NewStore(db dbm.DB) *Store { return &Store{db: db} }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func indexKey(nf [nodeLen]byte) []byte { return append([]byte{indexPrefix}, nf[:]...) }

func logKey(height int64, seq uint32) []byte {
	key := make([]byte, 0, 1+8+4)
	key = append(key, logPrefix)
	key = binary.BigEndian.AppendUint64(key, uint64(height))
	return binary.BigEndian.AppendUint32(key, seq)
}

// Spent reports whether nf was recorded at a height below asOf.
func (s *Store) Spent(nf [nodeLen]byte, asOf int64) (bool, error) {
	raw, err := s.db.Get(indexKey(nf))
	if err != nil {
		return false, fmt.Errorf("read nullifier index: %w", err)
	}
	if raw == nil {
		return false, nil
	}
	if len(raw) != 8 {
		return false, fmt.Errorf("%w: index value for %x is %d bytes", ErrCorrupt, nf, len(raw))
	}
	return int64(binary.BigEndian.Uint64(raw)) < asOf, nil
}

// Commit writes the nullifiers of the block at height. It first removes every record at or above
// height: those can only be left by a block that was executed but not committed, or by a
// rollback, and the block about to be written replaces them. A nullifier already recorded below
// height is an error: the chain state must have refused it.
func (s *Store) Commit(height int64, nfs [][nodeLen]byte) error {
	batch := s.db.NewBatch()
	defer batch.Close()
	staged, err := s.purgeFrom(batch, height)
	if err != nil {
		return err
	}
	inBlock := make(map[[nodeLen]byte]bool, len(nfs))
	for i, nf := range nfs {
		if inBlock[nf] {
			return fmt.Errorf("%w: %x is listed twice in the block", ErrCorrupt, nf)
		}
		inBlock[nf] = true
		staged++
		if prior, err := s.Spent(nf, height); err != nil {
			return err
		} else if prior {
			return fmt.Errorf("%w: %x is already recorded below height %d", ErrCorrupt, nf, height)
		}
		if err := batch.Set(indexKey(nf), binary.BigEndian.AppendUint64(nil, uint64(height))); err != nil {
			return fmt.Errorf("stage nullifier index: %w", err)
		}
		if err := batch.Set(logKey(height, uint32(i)), nf[:]); err != nil {
			return fmt.Errorf("stage nullifier log: %w", err)
		}
	}
	if staged == 0 {
		return nil
	}
	if err := batch.WriteSync(); err != nil {
		return fmt.Errorf("write nullifier batch at height %d: %w", height, err)
	}
	return nil
}

// purgeFrom stages the deletion of every record at or above height and returns how many
// operations it staged.
func (s *Store) purgeFrom(batch dbm.Batch, height int64) (int, error) {
	it, err := s.db.Iterator(logKey(height, 0), []byte{logPrefix + 1})
	if err != nil {
		return 0, fmt.Errorf("scan nullifier log: %w", err)
	}
	defer it.Close()
	staged := 0
	for ; it.Valid(); it.Next() {
		if len(it.Value()) != nodeLen {
			return 0, fmt.Errorf("%w: log value is %d bytes", ErrCorrupt, len(it.Value()))
		}
		if err := batch.Delete(it.Key()); err != nil {
			return 0, fmt.Errorf("stage nullifier log delete: %w", err)
		}
		if err := batch.Delete(append([]byte{indexPrefix}, it.Value()...)); err != nil {
			return 0, fmt.Errorf("stage nullifier index delete: %w", err)
		}
		staged++
	}
	return staged, it.Error()
}

// Walk calls fn for every record in insertion order: by height, then by position in the block.
func (s *Store) Walk(fn func(nf [nodeLen]byte, height int64) error) error {
	it, err := s.db.Iterator([]byte{logPrefix}, []byte{logPrefix + 1})
	if err != nil {
		return fmt.Errorf("scan nullifier log: %w", err)
	}
	defer it.Close()
	for ; it.Valid(); it.Next() {
		if len(it.Value()) != nodeLen || len(it.Key()) != 1+8+4 {
			return fmt.Errorf("%w: malformed log record", ErrCorrupt)
		}
		var nf [nodeLen]byte
		copy(nf[:], it.Value())
		height := int64(binary.BigEndian.Uint64(it.Key()[1:9]))
		if err := fn(nf, height); err != nil {
			return err
		}
	}
	return it.Error()
}

// Empty reports whether the store holds no record.
func (s *Store) Empty() (bool, error) {
	it, err := s.db.Iterator(nil, nil)
	if err != nil {
		return false, fmt.Errorf("scan nullifier store: %w", err)
	}
	defer it.Close()
	return !it.Valid(), it.Error()
}

// Import appends records at height 0, in the order given, to a store that has none of its own yet.
// It is the genesis import: a chain restarted from an export has an initial height of its own, and
// height 0 is below any block, so every imported nullifier is visible to it whatever height the
// old chain had reached. Call it again for the next chunk; the order carries over.
func (s *Store) Import(nfs [][nodeLen]byte) error {
	batch := s.db.NewBatch()
	defer batch.Close()
	inChunk := make(map[[nodeLen]byte]bool, len(nfs))
	for _, nf := range nfs {
		spent, err := s.Spent(nf, 1)
		if err != nil {
			return err
		}
		if spent || inChunk[nf] {
			return fmt.Errorf("%w: %x is imported twice", ErrCorrupt, nf)
		}
		inChunk[nf] = true
		if s.imported == ^uint32(0) {
			return fmt.Errorf("%w: too many imported records for one height", ErrCorrupt)
		}
		if err := batch.Set(indexKey(nf), binary.BigEndian.AppendUint64(nil, 0)); err != nil {
			return fmt.Errorf("stage nullifier index: %w", err)
		}
		if err := batch.Set(logKey(0, s.imported), nf[:]); err != nil {
			return fmt.Errorf("stage nullifier log: %w", err)
		}
		s.imported++
	}
	if err := batch.WriteSync(); err != nil {
		return fmt.Errorf("write imported nullifiers: %w", err)
	}
	return nil
}

// Fold is SHA-256(prev || nullifier): one step of the running accumulator the app hash commits to.
func Fold(prev, nullifier [nodeLen]byte) [nodeLen]byte {
	h := sha256.New()
	_, _ = h.Write(prev[:])
	_, _ = h.Write(nullifier[:])
	var out [nodeLen]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Reset removes every record. A failed state-sync restore uses it so the next attempt starts empty.
func (s *Store) Reset() error {
	it, err := s.db.Iterator(nil, nil)
	if err != nil {
		return fmt.Errorf("scan nullifier store: %w", err)
	}
	var keys [][]byte
	for ; it.Valid(); it.Next() {
		keys = append(keys, append([]byte(nil), it.Key()...))
	}
	if err := it.Error(); err != nil {
		_ = it.Close()
		return err
	}
	if err := it.Close(); err != nil {
		return err
	}
	batch := s.db.NewBatch()
	defer batch.Close()
	for _, k := range keys {
		if err := batch.Delete(k); err != nil {
			return fmt.Errorf("stage nullifier delete: %w", err)
		}
	}
	s.imported = 0
	return batch.WriteSync()
}
