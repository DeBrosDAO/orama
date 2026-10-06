package types_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/cometbft/cometbft/crypto/merkle"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

func TestVerifyBundle_mutatedHeaderFails(t *testing.T) {
	t.Parallel()

	headers := [][]byte{
		bytes.Repeat([]byte{1}, types.HashLen),
		bytes.Repeat([]byte{2}, types.HashLen),
		bytes.Repeat([]byte{3}, types.HashLen),
	}
	bundleHash := bytes.Repeat([]byte{9}, types.HashLen)
	root := merkle.HashFromByteSlices(headers)
	if bytes.Equal(bundleHash, root) {
		t.Fatal("fixture bundle hash collided with the merkle root")
	}
	if err := types.VerifyBundle(headers, bundleHash, root); err != nil {
		t.Fatalf("VerifyBundle: %v", err)
	}

	mutated := append([][]byte(nil), headers...)
	flipped := append([]byte(nil), mutated[1]...)
	flipped[0] ^= 0xff
	mutated[1] = flipped
	err := types.VerifyBundle(mutated, bundleHash, root)
	if !errors.Is(err, types.ErrWrongRoot) {
		t.Fatalf("mutated header err = %v, want %v", err, types.ErrWrongRoot)
	}
}

func TestVerifyBundle_singleHeader(t *testing.T) {
	t.Parallel()

	headers := [][]byte{bytes.Repeat([]byte{4}, types.HashLen)}
	root := merkle.HashFromByteSlices(headers)
	bundleHash := bytes.Repeat([]byte{5}, types.HashLen)
	if err := types.VerifyBundle(headers, bundleHash, root); err != nil {
		t.Fatalf("VerifyBundle: %v", err)
	}
}

func TestVerifyBundle_rejectsEmptyBundleHash(t *testing.T) {
	t.Parallel()

	headers := [][]byte{bytes.Repeat([]byte{4}, types.HashLen)}
	root := merkle.HashFromByteSlices(headers)
	if err := types.VerifyBundle(headers, nil, root); err == nil {
		t.Fatal("empty bundle hash was accepted")
	}
}
