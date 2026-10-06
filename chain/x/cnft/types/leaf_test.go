package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodeLeaf_canonicalOrder(t *testing.T) {
	asset := bytes.Repeat([]byte{0x11}, HashSize)
	owner := []byte{0x01, 0x02}
	delegate := []byte{}
	meta := []byte("bafy")
	creator := bytes.Repeat([]byte{0x22}, HashSize)
	const nonce = uint64(7)
	const hashID = uint32(1)

	got := EncodeLeaf(asset, owner, delegate, meta, creator, nonce, hashID)

	var want bytes.Buffer
	write := func(field []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(field)))
		want.Write(n[:])
		want.Write(field)
	}
	write(asset)
	write(owner)
	write(delegate)
	write(meta)
	write(creator)
	var nonceBuf [8]byte
	binary.BigEndian.PutUint64(nonceBuf[:], nonce)
	want.Write(nonceBuf[:])
	var idBuf [4]byte
	binary.BigEndian.PutUint32(idBuf[:], hashID)
	want.Write(idBuf[:])

	require.Equal(t, want.Bytes(), got)
	sum := sha256.Sum256(want.Bytes())
	require.Equal(t, sum[:], LeafHash(asset, owner, delegate, meta, creator, nonce, hashID))
}

func TestEncodeLeaf_everyFieldChangesTheEncoding(t *testing.T) {
	base := EncodeLeaf(
		bytes.Repeat([]byte{0x11}, 4),
		[]byte("owner"),
		[]byte("del"),
		[]byte("cid"),
		bytes.Repeat([]byte{0x22}, 4),
		1,
		1,
	)
	changed := [][]byte{
		EncodeLeaf(bytes.Repeat([]byte{0x12}, 4), []byte("owner"), []byte("del"), []byte("cid"), bytes.Repeat([]byte{0x22}, 4), 1, 1),
		EncodeLeaf(bytes.Repeat([]byte{0x11}, 4), []byte("ownes"), []byte("del"), []byte("cid"), bytes.Repeat([]byte{0x22}, 4), 1, 1),
		EncodeLeaf(bytes.Repeat([]byte{0x11}, 4), []byte("owner"), nil, []byte("cid"), bytes.Repeat([]byte{0x22}, 4), 1, 1),
		EncodeLeaf(bytes.Repeat([]byte{0x11}, 4), []byte("owner"), []byte("del"), []byte("cie"), bytes.Repeat([]byte{0x22}, 4), 1, 1),
		EncodeLeaf(bytes.Repeat([]byte{0x11}, 4), []byte("owner"), []byte("del"), []byte("cid"), bytes.Repeat([]byte{0x23}, 4), 1, 1),
		EncodeLeaf(bytes.Repeat([]byte{0x11}, 4), []byte("owner"), []byte("del"), []byte("cid"), bytes.Repeat([]byte{0x22}, 4), 2, 1),
		EncodeLeaf(bytes.Repeat([]byte{0x11}, 4), []byte("owner"), []byte("del"), []byte("cid"), bytes.Repeat([]byte{0x22}, 4), 1, 2),
	}
	for i, enc := range changed {
		require.NotEqual(t, base, enc, "field %d", i)
	}
}
