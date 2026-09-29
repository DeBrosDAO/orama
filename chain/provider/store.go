// Package provider stores assigned pieces on disk and builds the proofs
// x/storage checks. Runner follows the chain for one node and submits its
// accepts, declines and proofs through a Chain.
package provider

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/chain/piece"
)

const (
	// ReasonRoot is a piece whose bytes do not hash to the claimed root.
	ReasonRoot = "piece root mismatch"
	// ReasonDenylist is a CID the operator refused.
	ReasonDenylist = "denylist"
	// ReasonDisk is not enough free bytes for the piece.
	ReasonDisk = "disk full"
	// ReasonExists is a name that already holds a different piece.
	ReasonExists = "piece exists"
)

// Decision is accept or a decline reason. A decline stores nothing.
type Decision struct {
	Accept bool
	Reason string
}

// Store is one node's piece directory.
type Store struct {
	dir       string
	deny      map[string]struct{}
	freeBytes func() (uint64, error)
}

type record struct {
	CID  string `json:"cid"`
	Root string `json:"root"`
	Size int    `json:"size"`
}

// Open reads an existing directory. freeBytes reports space for a new piece.
// A nil freeBytes means the check is skipped.
func Open(dir string, denylist []string, freeBytes func() (uint64, error)) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "pieces"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o700); err != nil {
		return nil, err
	}
	deny := make(map[string]struct{}, len(denylist))
	for _, cid := range denylist {
		cid = strings.TrimSpace(cid)
		if cid == "" || strings.HasPrefix(cid, "#") {
			continue
		}
		deny[cid] = struct{}{}
	}
	return &Store{dir: dir, deny: deny, freeBytes: freeBytes}, nil
}

// Ingest stores data when the piece root matches and the CID is allowed.
func (s *Store) Ingest(cid string, data []byte, claimedRoot []byte) (Decision, error) {
	if err := validCID(cid); err != nil {
		return Decision{}, err
	}
	if _, ok := s.deny[cid]; ok {
		return Decision{Reason: ReasonDenylist}, nil
	}
	c, err := piece.Commit(data)
	if err != nil {
		return Decision{}, err
	}
	if len(claimedRoot) != len(c.Root) || !bytes.Equal(claimedRoot, c.Root) {
		return Decision{Reason: ReasonRoot}, nil
	}
	if s.Has(cid) {
		// A stored piece is never replaced. The same bytes again are a no-op.
		existing, err := s.ReadRoot(cid)
		if err != nil {
			return Decision{}, err
		}
		if bytes.Equal(existing, c.Root) {
			return Decision{Accept: true}, nil
		}
		return Decision{Reason: ReasonExists}, nil
	}
	if s.freeBytes != nil {
		free, err := s.freeBytes()
		if err != nil {
			return Decision{}, err
		}
		if free < uint64(len(data)) {
			return Decision{Reason: ReasonDisk}, nil
		}
	}
	piecePath := s.piecePath(cid)
	tmpPiece := piecePath + ".tmp"
	if err := os.WriteFile(tmpPiece, data, 0o600); err != nil {
		return Decision{}, err
	}
	rec := record{CID: cid, Root: hex.EncodeToString(c.Root), Size: len(data)}
	body, err := json.Marshal(rec)
	if err != nil {
		_ = os.Remove(tmpPiece)
		return Decision{}, err
	}
	metaPath := s.metaPath(cid)
	tmpMeta := metaPath + ".tmp"
	if err := os.WriteFile(tmpMeta, body, 0o600); err != nil {
		_ = os.Remove(tmpPiece)
		return Decision{}, err
	}
	if err := os.Rename(tmpPiece, piecePath); err != nil {
		_ = os.Remove(tmpPiece)
		_ = os.Remove(tmpMeta)
		return Decision{}, err
	}
	if err := os.Rename(tmpMeta, metaPath); err != nil {
		_ = os.Remove(tmpMeta)
		return Decision{}, err
	}
	return Decision{Accept: true}, nil
}

// Has reports whether cid was ingested.
func (s *Store) Has(cid string) bool {
	if err := validCID(cid); err != nil {
		return false
	}
	_, err := os.Stat(s.piecePath(cid))
	return err == nil
}

// Absent returns assigned CIDs that are not stored, in the same order.
func (s *Store) Absent(assigned []string) []string {
	var missing []string
	for _, cid := range assigned {
		if !s.Has(cid) {
			missing = append(missing, cid)
		}
	}
	return missing
}

// Prove builds a challenge proof from the stored bytes.
func (s *Store) Prove(cid string, index uint64) (piece.Commitment, piece.Proof, error) {
	if err := validCID(cid); err != nil {
		return piece.Commitment{}, piece.Proof{}, err
	}
	data, err := os.ReadFile(s.piecePath(cid))
	if err != nil {
		return piece.Commitment{}, piece.Proof{}, err
	}
	c, err := piece.Commit(data)
	if err != nil {
		return piece.Commitment{}, piece.Proof{}, err
	}
	proof, err := piece.Prove(data, index)
	if err != nil {
		return piece.Commitment{}, piece.Proof{}, err
	}
	if err := piece.VerifyChallenge(c, proof); err != nil {
		return piece.Commitment{}, piece.Proof{}, err
	}
	return c, proof, nil
}

func (s *Store) piecePath(cid string) string {
	return filepath.Join(s.dir, "pieces", cid)
}

func (s *Store) metaPath(cid string) string {
	return filepath.Join(s.dir, "meta", cid+".json")
}

func validCID(cid string) error {
	if cid == "" || cid == "." || cid == ".." || strings.ContainsAny(cid, `/\`) || strings.ContainsRune(cid, 0) {
		return fmt.Errorf("cid %q is not a file name", cid)
	}
	return nil
}

// ReadRoot returns the root stored beside a piece.
func (s *Store) ReadRoot(cid string) ([]byte, error) {
	if err := validCID(cid); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(s.metaPath(cid))
	if err != nil {
		return nil, err
	}
	var rec record
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, err
	}
	root, err := hex.DecodeString(rec.Root)
	if err != nil {
		return nil, err
	}
	if rec.CID != cid {
		return nil, errors.New("piece metadata cid does not match")
	}
	return root, nil
}
