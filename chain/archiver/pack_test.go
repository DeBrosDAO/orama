package archiver

import (
	"bytes"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

func TestPackBindsTheBodyAndRejectsAMutatedHash(t *testing.T) {
	hashes := [][]byte{
		bytes.Repeat([]byte{1}, types.HashLen),
		bytes.Repeat([]byte{2}, types.HashLen),
		bytes.Repeat([]byte{3}, types.HashLen),
	}
	body := []byte("block-bytes")
	got, err := Pack(10, hashes, body)
	if err != nil {
		t.Fatal(err)
	}
	if got.Start != 10 || got.End != 12 {
		t.Fatalf("range %d-%d", got.Start, got.End)
	}
	if err := types.VerifyBundle(got.BlockHashes, got.ContentHash, got.MerkleRoot); err != nil {
		t.Fatal(err)
	}
	mutated := append([]byte(nil), hashes[1]...)
	mutated[0] ^= 0xff
	bad := [][]byte{hashes[0], mutated, hashes[2]}
	if err := types.VerifyBundle(bad, got.ContentHash, got.MerkleRoot); err == nil {
		t.Fatal("a mutated block hash matched the root")
	}
	path := t.TempDir() + "/cursor"
	if h, err := LoadCursor(path); err != nil || h != 0 {
		t.Fatalf("missing cursor %d %v", h, err)
	}
	if err := SaveCursor(path, got.End); err != nil {
		t.Fatal(err)
	}
	h, err := LoadCursor(path)
	if err != nil || h != got.End {
		t.Fatalf("cursor %d %v", h, err)
	}
}
