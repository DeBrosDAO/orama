package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Assignments lists every deal slot this store has bound.
func (s *Store) Assignments() ([]Assignment, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, "assignments"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list assignments: %w", err)
	}
	var out []Assignment
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(s.dir, "assignments", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read assignment %s: %w", entry.Name(), err)
		}
		var rec Assignment
		if err := json.Unmarshal(body, &rec); err != nil {
			return nil, fmt.Errorf("assignment %s: %w", entry.Name(), err)
		}
		if err := validCID(rec.CID); err != nil {
			return nil, fmt.Errorf("assignment %s: %w", entry.Name(), err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// Release drops the dealID/slot binding. The piece bytes are removed only
// when no other binding names the same CID and keep does not claim it (the
// runner keeps a piece a waiting slot will accept). Releasing a slot that is
// not bound is a no-op.
func (s *Store) Release(dealID uint64, slot uint32, keep func(cid string) bool) error {
	cid, ok, err := s.Lookup(dealID, slot)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	all, err := s.Assignments()
	if err != nil {
		return err
	}
	last := true
	for _, a := range all {
		if a.CID == cid && (a.DealID != dealID || a.Slot != slot) {
			last = false
		}
	}
	remove := last && (keep == nil || !keep(cid))
	if remove {
		// The pin goes first: a failed unpin leaves the binding, so the next
		// sweep tries again instead of leaking a public pin.
		if err := s.unpinPiece(cid); err != nil {
			return err
		}
	}
	if err := os.Remove(s.assignPath(dealID, slot)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove deal %d slot %d binding: %w", dealID, slot, err)
	}
	if !remove {
		return nil
	}
	for _, path := range []string{s.piecePath(cid), s.metaPath(cid)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove released piece %s: %w", cid, err)
		}
	}
	return nil
}

// UsedBytes is the size of every stored piece.
func (s *Store) UsedBytes() (int64, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, "pieces"))
	if err != nil {
		return 0, fmt.Errorf("list pieces: %w", err)
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, fmt.Errorf("stat piece %s: %w", entry.Name(), err)
		}
		total += info.Size()
	}
	return total, nil
}
