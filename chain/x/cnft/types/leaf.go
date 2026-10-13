package types

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// EncodeLeaf is the canonical encoding of a compressed leaf. The leaf hash is
// SHA-256 of these bytes. The field order is asset_id, owner, delegate,
// metadata_cid, creator_hash, nonce, hash_id. Each of the five byte fields is
// a big-endian uint32 length followed by the raw bytes, so an empty delegate
// is a zero length and not an omitted field. Nonce is a big-endian uint64 and
// hash_id is a big-endian uint32.
func EncodeLeaf(assetID, owner, delegate, metadataCID, creatorHash []byte, nonce uint64, hashID uint32) []byte {
	n := 4*5 + len(assetID) + len(owner) + len(delegate) + len(metadataCID) + len(creatorHash) + 8 + 4
	out := make([]byte, 0, n)
	out = appendLenBytes(out, assetID)
	out = appendLenBytes(out, owner)
	out = appendLenBytes(out, delegate)
	out = appendLenBytes(out, metadataCID)
	out = appendLenBytes(out, creatorHash)
	var nonceBuf [8]byte
	binary.BigEndian.PutUint64(nonceBuf[:], nonce)
	out = append(out, nonceBuf[:]...)
	var idBuf [4]byte
	binary.BigEndian.PutUint32(idBuf[:], hashID)
	out = append(out, idBuf[:]...)
	return out
}

func appendLenBytes(dst, field []byte) []byte {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(field)))
	dst = append(dst, n[:]...)
	return append(dst, field...)
}

// LeafHash is SHA-256 of EncodeLeaf. hashID is part of the encoding; this
// function always uses SHA-256, which is hash id HashIDSHA256.
func LeafHash(assetID, owner, delegate, metadataCID, creatorHash []byte, nonce uint64, hashID uint32) []byte {
	sum := sha256.Sum256(EncodeLeaf(assetID, owner, delegate, metadataCID, creatorHash, nonce, hashID))
	return sum[:]
}

// CreatorHash is the creator_hash stored in a leaf: SHA-256 of the collection
// creator's raw account bytes.
func CreatorHash(creator sdk.AccAddress) []byte {
	sum := sha256.Sum256(creator.Bytes())
	return sum[:]
}

// HashLeaf hashes a leaf, parsing owner and delegate as bech32 account addresses.
func HashLeaf(leaf Leaf) ([]byte, error) {
	if leaf.HashId != HashIDSHA256 {
		return nil, fmt.Errorf("unsupported hash_id %d", leaf.HashId)
	}
	if len(leaf.AssetId) != HashSize {
		return nil, fmt.Errorf("asset_id must be %d bytes", HashSize)
	}
	if len(leaf.CreatorHash) != HashSize {
		return nil, fmt.Errorf("creator_hash must be %d bytes", HashSize)
	}
	owner, err := sdk.AccAddressFromBech32(leaf.Owner)
	if err != nil {
		return nil, fmt.Errorf("leaf owner: %w", err)
	}
	var delegate []byte
	if leaf.Delegate != "" {
		del, err := sdk.AccAddressFromBech32(leaf.Delegate)
		if err != nil {
			return nil, fmt.Errorf("leaf delegate: %w", err)
		}
		delegate = del.Bytes()
	}
	return LeafHash(leaf.AssetId, owner.Bytes(), delegate, []byte(leaf.MetadataCid), leaf.CreatorHash, leaf.Nonce, leaf.HashId), nil
}
