package nsbackup

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// A namespace backup is one frame, sealed with Seal:
//
//	magic(4) | version(1) | header length (uint32, big endian) | header JSON | SQLite file
//
// The SQLite file is the namespace RQLite's /db/backup output, carried raw so
// a large database is not inflated by base64. The header records its size and
// SHA-256, so a truncated or altered file is refused. The restore request the
// operator's machine sends a destination gateway is the same frame with a
// different magic and the secrets sealed to that gateway's restore key.

const (
	payloadMagic = "ORNP"
	requestMagic = "ORNR"
	frameVersion = 1
	frameFixed   = len(payloadMagic) + 1 + 4

	// maxHeaderBytes bounds the JSON header (pins and secrets), so a corrupt
	// length cannot make a reader allocate much before it finds out. At a few
	// hundred bytes a secret this is tens of thousands of secrets.
	maxHeaderBytes = 8 << 20

	// sqliteMagic is the first 16 bytes of every SQLite database file.
	sqliteMagic = "SQLite format 3\x00"
)

// Limits on what one backup may hold. A backup is one nacl box, so the whole
// frame is in memory at once, and the gateway holds about three copies of the
// snapshot while it seals one (the /db/backup read, the frame, the box).
const (
	// MaxRQLiteBytes is the largest namespace database a backup carries.
	MaxRQLiteBytes = 256 << 20
	// MaxPins is the most CIDs a backup lists.
	MaxPins = 50_000
	// MaxFrameBytes is the largest frame a reader accepts.
	MaxFrameBytes = frameFixed + maxHeaderBytes + MaxRQLiteBytes
)

var (
	// ErrCorrupt means a payload or restore request is truncated or malformed.
	ErrCorrupt = errors.New("backup payload is corrupt")
	// ErrTooLarge means a namespace is over one of the backup limits above.
	ErrTooLarge = errors.New("namespace is over the backup limit")
)

// Secret is one stored secret, in plaintext: the column it lives in, the
// values of that table's id columns (in secrets.NamespaceColumns order), and
// the decrypted value.
type Secret struct {
	Table  string   `json:"table"`
	Column string   `json:"column"`
	IDs    []string `json:"ids"`
	Value  string   `json:"value"`
}

// Payload is what a namespace backup holds once it is opened.
type Payload struct {
	Namespace string
	// Pins are the CIDs the namespace had pinned.
	Pins []string
	// StoredBytes is the namespace's logical storage use when the backup was
	// taken: the sum of ipfs_content_ownership.size_bytes, which is what the
	// storage quota counts.
	StoredBytes int64
	// Secrets are the namespace's stored secrets, decrypted by the source
	// cluster, because they were encrypted under its encryption root.
	Secrets []Secret
	// RQLite is the namespace database, as RQLite's /db/backup returns it.
	RQLite []byte
}

type frameHeader struct {
	Namespace    string   `json:"namespace"`
	Pins         []string `json:"pins"`
	StoredBytes  int64    `json:"stored_bytes"`
	RQLiteSize   int      `json:"rqlite_size"`
	RQLiteSHA256 string   `json:"rqlite_sha256"`
}

type payloadHeader struct {
	frameHeader
	Secrets []Secret `json:"secrets"`
}

// Marshal validates p and encodes it as the plaintext Seal encrypts.
func (p Payload) Marshal() ([]byte, error) {
	if err := validateFrame(p.Namespace, p.Pins, p.StoredBytes, p.RQLite); err != nil {
		return nil, err
	}
	for _, s := range p.Secrets {
		if err := validateSecretRef(s.Table, s.Column, s.IDs); err != nil {
			return nil, err
		}
	}
	h := payloadHeader{frameHeader: newFrameHeader(p.Namespace, p.Pins, p.StoredBytes, p.RQLite), Secrets: p.Secrets}
	return writeFrame(payloadMagic, h, p.RQLite)
}

// UnmarshalPayload decodes and validates what Open returned for a namespace
// backup.
func UnmarshalPayload(b []byte) (Payload, error) {
	var h payloadHeader
	db, err := readFrame(payloadMagic, b, &h)
	if err != nil {
		return Payload{}, err
	}
	if err := checkFrame(h.frameHeader, db); err != nil {
		return Payload{}, err
	}
	for _, s := range h.Secrets {
		if err := validateSecretRef(s.Table, s.Column, s.IDs); err != nil {
			return Payload{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
	}
	return Payload{Namespace: h.Namespace, Pins: h.Pins, StoredBytes: h.StoredBytes, Secrets: h.Secrets, RQLite: db}, nil
}

func newFrameHeader(ns string, pins []string, stored int64, db []byte) frameHeader {
	sum := sha256.Sum256(db)
	if pins == nil {
		pins = []string{}
	}
	return frameHeader{Namespace: ns, Pins: pins, StoredBytes: stored, RQLiteSize: len(db), RQLiteSHA256: hex.EncodeToString(sum[:])}
}

// checkFrame is what a reader verifies about any decoded frame.
func checkFrame(h frameHeader, db []byte) error {
	sum := sha256.Sum256(db)
	if h.RQLiteSize != len(db) || h.RQLiteSHA256 != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("%w: the RQLite snapshot does not match its recorded size and SHA-256", ErrCorrupt)
	}
	if err := validateFrame(h.Namespace, h.Pins, h.StoredBytes, db); err != nil {
		return fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return nil
}

func validateFrame(ns string, pins []string, stored int64, db []byte) error {
	if !httputil.ValidateNamespace(ns) {
		return fmt.Errorf("namespace %q is not a valid namespace name", ns)
	}
	if !bytes.HasPrefix(db, []byte(sqliteMagic)) {
		return fmt.Errorf("the RQLite snapshot is not a SQLite database")
	}
	if len(db) > MaxRQLiteBytes {
		return fmt.Errorf("%w: the RQLite snapshot is %d bytes, over the %d-byte backup limit", ErrTooLarge, len(db), MaxRQLiteBytes)
	}
	if len(pins) > MaxPins {
		return fmt.Errorf("%w: %d pins is over the %d-pin backup limit", ErrTooLarge, len(pins), MaxPins)
	}
	if stored < 0 {
		return fmt.Errorf("stored bytes %d is negative", stored)
	}
	seen := make(map[string]struct{}, len(pins))
	for _, c := range pins {
		if !httputil.ValidateCID(c) {
			return fmt.Errorf("pin %q is not a CID", c)
		}
		if _, dup := seen[c]; dup {
			return fmt.Errorf("pin %q is listed twice", c)
		}
		seen[c] = struct{}{}
	}
	return nil
}

// validateSecretRef accepts only the columns the cluster seals with its
// encryption root, so a restore can never be made to write anywhere else.
func validateSecretRef(table, column string, ids []string) error {
	for _, c := range secrets.NamespaceColumns() {
		if c.Table != table || c.Column != column {
			continue
		}
		if len(ids) != len(c.IDCols) {
			return fmt.Errorf("secret %s.%s has %d ids, want %d", table, column, len(ids), len(c.IDCols))
		}
		for _, id := range ids {
			if id == "" {
				return fmt.Errorf("secret %s.%s has an empty id", table, column)
			}
		}
		return nil
	}
	return fmt.Errorf("%s.%s is not a namespace secret column", table, column)
}

func writeFrame(magic string, header any, db []byte) ([]byte, error) {
	hdr, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("encode backup header: %w", err)
	}
	if len(hdr) > maxHeaderBytes {
		return nil, fmt.Errorf("backup header is %d bytes, over the %d-byte limit", len(hdr), maxHeaderBytes)
	}
	out := make([]byte, 0, frameFixed+len(hdr)+len(db))
	out = append(out, magic...)
	out = append(out, frameVersion)
	out = binary.BigEndian.AppendUint32(out, uint32(len(hdr)))
	out = append(out, hdr...)
	return append(out, db...), nil
}

// readFrame decodes the header into header and returns the SQLite bytes.
// Unknown header fields are refused, not ignored.
func readFrame(magic string, b []byte, header any) ([]byte, error) {
	if len(b) > MaxFrameBytes {
		return nil, fmt.Errorf("%w: %d bytes is over the %d-byte frame limit", ErrCorrupt, len(b), MaxFrameBytes)
	}
	if len(b) < frameFixed || string(b[:len(magic)]) != magic {
		return nil, fmt.Errorf("%w: not a namespace backup frame", ErrCorrupt)
	}
	if b[len(magic)] != frameVersion {
		return nil, fmt.Errorf("%w: frame version %d is not %d", ErrCorrupt, b[len(magic)], frameVersion)
	}
	n := binary.BigEndian.Uint32(b[len(magic)+1 : frameFixed])
	if n > maxHeaderBytes || int(n) > len(b)-frameFixed {
		return nil, fmt.Errorf("%w: header length %d does not fit", ErrCorrupt, n)
	}
	dec := json.NewDecoder(bytes.NewReader(b[frameFixed : frameFixed+int(n)]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(header); err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrCorrupt, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing data after the header", ErrCorrupt)
	}
	return b[frameFixed+int(n):], nil
}
