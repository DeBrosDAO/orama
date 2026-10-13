package indexer

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
)

// A list that grows with the chain (the transactions, an account's transactions, the blocks) is
// paged by the position of its last row, not by a page number: a page number makes the index skip
// every row before the page, so page 1000 at 100 a page costs 99,900 rows to read the last 100, and
// a row that arrives while a reader pages shifts every page after it. A cursor names the position of
// the last row a page returned and the next page starts strictly before it, so a page costs the page
// at any depth and no row is repeated or missed.

const (
	// CursorParam is the query parameter that carries a cursor.
	CursorParam = "cursor"
	// CursorLen is the length of a cursor, in characters: 12 bytes (the height, 8, and the index
	// of the transaction in its block, 4, both big-endian) in unpadded URL-safe base64.
	CursorLen = 16
	// MaxListPage is the deepest numbered page the long lists answer. A reader that wants to go
	// further follows the cursor each page returns (next_cursor).
	MaxListPage = 10

	cursorBytes = 12
)

// Cursor is a position in a list ordered by block height and index in the block: rows older than
// (Height, Index) are the page after it. A block list uses Index 0.
type Cursor struct {
	Height int64
	Index  uint32
}

// String is the cursor as the API carries it.
func (c Cursor) String() string {
	var raw [cursorBytes]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(c.Height))
	binary.BigEndian.PutUint32(raw[8:], c.Index)
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

// ParseCursor decodes a cursor and refuses anything but one String can write: exactly CursorLen
// characters of the URL-safe alphabet, which decode to a height from 1 to the largest a block can
// have. A cursor is a position, not a secret, so a forged one reads a page of the list from where
// it points and nothing else.
func ParseCursor(s string) (Cursor, error) {
	if len(s) != CursorLen {
		return Cursor{}, fmt.Errorf("a cursor is %d characters, not %d", CursorLen, len(s))
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return Cursor{}, fmt.Errorf("a cursor is URL-safe base64: %w", err)
	}
	height := binary.BigEndian.Uint64(raw[:8])
	if height < 1 || height > math.MaxInt64 {
		return Cursor{}, fmt.Errorf("a cursor names a height from 1 to %d, not %d", int64(math.MaxInt64), height)
	}
	return Cursor{Height: int64(height), Index: binary.BigEndian.Uint32(raw[8:])}, nil
}

// bound is the key suffix of the position, which every row key of a transaction list ends with.
func (c Cursor) bound() []byte { return join(be64(uint64(c.Height)), be32(c.Index)) }
