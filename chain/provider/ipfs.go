package provider

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	gocid "github.com/ipfs/go-cid"
)

// MaxPieceCIDs bounds the CIDs recorded for one piece. Anyone can name a CID
// through POST /pins, so the list cannot grow without bound.
const MaxPieceCIDs = 4

// readRecord is the metadata beside a stored piece.
func (s *Store) readRecord(name string) (record, error) {
	var rec record
	if err := validCID(name); err != nil {
		return rec, err
	}
	body, err := os.ReadFile(s.metaPath(name))
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(body, &rec); err != nil {
		return rec, fmt.Errorf("piece metadata %s: %w", name, err)
	}
	if rec.CID != name {
		return rec, errors.New("piece metadata cid does not match")
	}
	return rec, nil
}

// writeRecord replaces a piece's metadata through a uniquely named temporary file.
func (s *Store) writeRecord(rec record) error {
	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.metaPath(rec.CID)), "."+rec.CID+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), s.metaPath(rec.CID)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// update reads a piece's record, applies fn and writes it back, holding recMu.
func (s *Store) update(name string, fn func(*record) error) error {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	rec, err := s.readRecord(name)
	if err != nil {
		return err
	}
	if err := fn(&rec); err != nil {
		return err
	}
	return s.writeRecord(rec)
}

// IPFSPins lists the public CIDs recorded for a stored piece.
func (s *Store) IPFSPins(name string) ([]ipfsPin, error) {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	rec, err := s.readRecord(name)
	if err != nil {
		return nil, err
	}
	return rec.IPFS, nil
}

// AddIPFS records a CID the piece is to be pinned under. The same CID again is
// a no-op. At most MaxPieceCIDs are kept; another is refused.
func (s *Store) AddIPFS(name, cid string) error {
	return s.update(name, func(rec *record) error {
		for _, p := range rec.IPFS {
			if p.CID == cid {
				return nil
			}
		}
		if len(rec.IPFS) >= MaxPieceCIDs {
			return fmt.Errorf("piece %s already has %d IPFS CIDs", name, MaxPieceCIDs)
		}
		rec.IPFS = append(rec.IPFS, ipfsPin{CID: cid})
		return nil
	})
}

// MarkPinned records that the public Kubo pins cid for the piece, adding it if
// the piece did not list it yet.
func (s *Store) MarkPinned(name, cid string) error {
	return s.update(name, func(rec *record) error {
		for i := range rec.IPFS {
			if rec.IPFS[i].CID == cid {
				rec.IPFS[i].Pinned = true
				return nil
			}
		}
		if len(rec.IPFS) >= MaxPieceCIDs {
			return fmt.Errorf("piece %s already has %d IPFS CIDs", name, MaxPieceCIDs)
		}
		rec.IPFS = append(rec.IPFS, ipfsPin{CID: cid, Pinned: true})
		return nil
	})
}

// Unpublish removes a piece from the public Kubo: it unpins every recorded CID
// and forgets them. It is what a PRIVATE deal's slot does to a piece an
// earlier public deal with the same root had pinned.
func (s *Store) Unpublish(name string) error {
	if err := s.unpinPiece(name); err != nil {
		return err
	}
	return s.update(name, func(rec *record) error {
		rec.IPFS = nil
		return nil
	})
}

// Denied reports whether cid is on the operator's denylist. A CIDv0 and the
// CIDv1 of the same multihash are the same entry; a name that is not a CID
// (a piece root) is compared as written.
func (s *Store) Denied(cid string) bool {
	_, ok := s.deny[denyKey(cid)]
	return ok
}

// denyKey is the multihash of a CID in hex, so the two spellings of one hash
// match, or the string itself when it is not a CID. Different chunking makes a
// different CID and is not matched.
func denyKey(cid string) string {
	c, err := gocid.Decode(cid)
	if err != nil {
		return cid
	}
	return hex.EncodeToString(c.Hash())
}

// ReadPiece returns a stored piece's bytes.
func (s *Store) ReadPiece(name string) ([]byte, error) {
	if err := validCID(name); err != nil {
		return nil, err
	}
	return os.ReadFile(s.piecePath(name))
}

// Discard removes a stored piece no slot is bound to, after unpinning its
// public CIDs. A piece some slot still binds is left alone.
func (s *Store) Discard(name string) error {
	bound, err := s.Assignments()
	if err != nil {
		return err
	}
	for _, a := range bound {
		if a.CID == name {
			return nil
		}
	}
	if err := s.unpinPiece(name); err != nil {
		return err
	}
	s.recMu.Lock()
	defer s.recMu.Unlock()
	for _, path := range []string{s.piecePath(name), s.metaPath(name)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("discard piece %s: %w", name, err)
		}
	}
	return nil
}

// unpinPiece drops the public pins of a stored piece. It does nothing when the
// store has no unpinner or the piece never had a public CID.
func (s *Store) unpinPiece(name string) error {
	if s.unpin == nil {
		return nil
	}
	pins, err := s.IPFSPins(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, p := range pins {
		if err := s.unpin.Unpin(context.Background(), p.CID); err != nil {
			return fmt.Errorf("unpin %s of piece %s: %w", p.CID, name, err)
		}
	}
	return nil
}
