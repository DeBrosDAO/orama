package archiver

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

// FuzzArchiverBundle: the bundle files the archiver pulls from the network are untrusted bytes.
// Decode never panics and refuses with ErrBundle; a bundle it accepts is the canonical encoding of
// its blocks (so one set of blocks has one content hash), every block hashes to the hash beside it,
// and Verify accepts it under the record built from it and refuses it under a record with any one
// of the content hash, the root, or the range changed.
func FuzzArchiverBundle(f *testing.F) {
	var blocks []Block
	for h := int64(5); h < 8; h++ {
		blocks = append(blocks, realBlock(f, h))
	}
	for n := 1; n <= len(blocks); n++ {
		body, err := Encode(blocks[:n])
		if err != nil {
			f.Fatal(err)
		}
		f.Add(body)
	}
	good, _ := Encode(blocks)
	f.Add(good[:len(good)-1])
	f.Add(append(bytes.Clone(good), 0))
	f.Add([]byte("ORBH\x01"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, body []byte) {
		got, err := Decode(body)
		if err != nil {
			if !errors.Is(err, ErrBundle) {
				t.Fatalf("a refused bundle is not ErrBundle: %v", err)
			}
			return
		}
		again, err := Encode(got)
		if err != nil || !bytes.Equal(again, body) {
			t.Fatalf("an accepted bundle is not the canonical encoding of its blocks (%v)", err)
		}
		hashes := make([][]byte, len(got))
		for i, b := range got {
			if got[0].Height+int64(i) != b.Height || len(b.Hash) != types.HashLen {
				t.Fatalf("block %d has height %d / hash length %d", i, b.Height, len(b.Hash))
			}
			hashes[i] = b.Hash
		}
		sum := sha256.Sum256(body)
		rec := types.RangeRecord{
			StartHeight: got[0].Height, EndHeight: got[len(got)-1].Height, BundleHash: sum[:], MerkleRoot: types.MerkleRoot(hashes),
		}
		if _, err := Verify(body, rec); err != nil {
			t.Fatalf("Verify refuses a bundle under the record built from it: %v", err)
		}
		for name, mutate := range map[string]func(*types.RangeRecord){
			"content hash": func(r *types.RangeRecord) { r.BundleHash = flip(r.BundleHash) },
			"root":         func(r *types.RangeRecord) { r.MerkleRoot = flip(r.MerkleRoot) },
			"start":        func(r *types.RangeRecord) { r.StartHeight++ },
			"end":          func(r *types.RangeRecord) { r.EndHeight++ },
		} {
			bad := rec
			bad.BundleHash, bad.MerkleRoot = bytes.Clone(rec.BundleHash), bytes.Clone(rec.MerkleRoot)
			mutate(&bad)
			if _, err := Verify(body, bad); err == nil {
				t.Fatalf("Verify accepted the bundle under a record with a changed %s", name)
			}
		}
	})
}

func flip(b []byte) []byte {
	out := bytes.Clone(b)
	out[0] ^= 1
	return out
}
