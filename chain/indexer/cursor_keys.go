package indexer

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// Two lists are not ordered by block position, so they cannot take a Cursor (cursor.go): an owner's
// assets, in asset-id order, and an account's rewards, newest epoch first. Each is paged the same
// way, by the key of the last row a page returned: the cursor is that key after the list's prefix,
// in unpadded URL-safe base64, and the next page seeks past it, so a page costs the page at any
// depth and an attacker who gives one owner a million assets cannot make a reader skip them.

const (
	// AssetCursorLen is the length of an owner-assets cursor, in characters: the 44 bytes of an
	// asset's location (the 32-byte asset id, the tree id, 8, and the leaf index, 4, both
	// big-endian), which is what follows the owner in the owner index's key.
	AssetCursorLen = 59
	// RewardCursorMinLen and RewardCursorMaxLen bound a rewards cursor, in characters: the epoch,
	// 8 bytes big-endian, and the validator operator address, which follows the account in the
	// rewards index's key. The shortest address is 21 characters (the oramavaloper1 prefix, a byte
	// of data, the checksum), the longest 427 (255 bytes), so 29 to 435 bytes.
	RewardCursorMinLen = 39
	RewardCursorMaxLen = 580

	hashSize = 32
)

// AssetCursor is a position in an owner's assets: the next page holds the assets after this one in
// the owner index's key order (asset id, then tree, then leaf).
type AssetCursor struct {
	ID        [hashSize]byte
	TreeID    uint64
	LeafIndex uint32
}

// assetCursorOf is the position of a.
func assetCursorOf(a Asset) (AssetCursor, error) {
	id, err := hex.DecodeString(a.ID)
	if err != nil || len(id) != hashSize {
		return AssetCursor{}, fmt.Errorf("asset id %q is not %d bytes of hex", a.ID, hashSize)
	}
	c := AssetCursor{TreeID: a.TreeID, LeafIndex: a.LeafIndex}
	copy(c.ID[:], id)
	return c, nil
}

// bound is the key suffix of the position in the owner index.
func (c AssetCursor) bound() []byte { return location(c.ID[:], c.TreeID, c.LeafIndex) }

// String is the cursor as the API carries it.
func (c AssetCursor) String() string { return base64.RawURLEncoding.EncodeToString(c.bound()) }

// ParseAssetCursor decodes a cursor and refuses anything but one String can write: exactly
// AssetCursorLen characters of the URL-safe alphabet. Any 44 bytes are a position, so a forged
// cursor reads a page of the owner's assets from where it points and nothing else.
func ParseAssetCursor(s string) (AssetCursor, error) {
	if len(s) != AssetCursorLen {
		return AssetCursor{}, fmt.Errorf("a cursor is %d characters, not %d", AssetCursorLen, len(s))
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return AssetCursor{}, fmt.Errorf("a cursor is URL-safe base64: %w", err)
	}
	c := AssetCursor{
		TreeID:    binary.BigEndian.Uint64(raw[hashSize:]),
		LeafIndex: binary.BigEndian.Uint32(raw[hashSize+8:]),
	}
	copy(c.ID[:], raw[:hashSize])
	return c, nil
}

// RewardCursor is a position in an account's rewards: the next page holds the rewards older than
// this one, by epoch, and within an epoch the validators that sort before this one.
type RewardCursor struct {
	Epoch     uint64
	Validator string
}

// bound is the key suffix of the position in the account rewards index.
func (c RewardCursor) bound() []byte { return join(be64(c.Epoch), []byte(c.Validator)) }

// String is the cursor as the API carries it.
func (c RewardCursor) String() string { return base64.RawURLEncoding.EncodeToString(c.bound()) }

// ParseRewardCursor decodes a cursor and refuses anything but one String can write:
// RewardCursorMinLen to RewardCursorMaxLen characters of the URL-safe alphabet that decode to an
// epoch from 1 and a canonical validator operator address.
func ParseRewardCursor(s string) (RewardCursor, error) {
	if len(s) < RewardCursorMinLen || len(s) > RewardCursorMaxLen {
		return RewardCursor{}, fmt.Errorf("a cursor is %d to %d characters, not %d", RewardCursorMinLen, RewardCursorMaxLen, len(s))
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return RewardCursor{}, fmt.Errorf("a cursor is URL-safe base64: %w", err)
	}
	c := RewardCursor{Epoch: binary.BigEndian.Uint64(raw[:8]), Validator: string(raw[8:])}
	if c.Epoch < 1 {
		return RewardCursor{}, fmt.Errorf("a cursor names an epoch from 1, not 0")
	}
	if !IsValidatorAddress(c.Validator) {
		return RewardCursor{}, fmt.Errorf("a cursor names a validator operator address, not %q", c.Validator)
	}
	return c, nil
}
