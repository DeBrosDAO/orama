package archiver

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

// Bundle file layout, version 1. It is not a CAR file.
//
//	"ORBH" | version u8 | start i64 | count u32 |
//	count × ( block hash [32] | length u32 | tendermint.types.Block protobuf )
//
// All integers are big-endian. The bundle CID is a CIDv1, raw codec, over
// the SHA-256 of the whole file: the same digest x/archive stores as
// bundle_hash.
const (
	bundleMagic   = "ORBH"
	bundleVersion = 1
	headerLen     = 4 + 1 + 8 + 4
	// MaxBlockBytes bounds one block inside a bundle.
	MaxBlockBytes = 64 << 20
)

// ErrBundle is a bundle whose bytes, headers or root do not check out.
var ErrBundle = errors.New("bundle does not verify")

// Block is one finalised block in a bundle.
type Block struct {
	Height int64
	Hash   []byte
	Proto  []byte
}

// Encode writes blocks, which must be consecutive heights, as a bundle.
func Encode(blocks []Block) ([]byte, error) {
	if len(blocks) == 0 {
		return nil, errors.New("bundle has no blocks")
	}
	var buf bytes.Buffer
	buf.WriteString(bundleMagic)
	buf.WriteByte(bundleVersion)
	_ = binary.Write(&buf, binary.BigEndian, blocks[0].Height)
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(blocks)))
	for i, b := range blocks {
		if b.Height != blocks[0].Height+int64(i) {
			return nil, fmt.Errorf("block %d is height %d, not consecutive", i, b.Height)
		}
		if len(b.Hash) != types.HashLen || len(b.Proto) == 0 || len(b.Proto) > MaxBlockBytes {
			return nil, fmt.Errorf("block %d has a bad hash or body", b.Height)
		}
		buf.Write(b.Hash)
		_ = binary.Write(&buf, binary.BigEndian, uint32(len(b.Proto)))
		buf.Write(b.Proto)
	}
	return buf.Bytes(), nil
}

// Decode parses a bundle and checks that every block's bytes hash to the
// block hash beside it and that heights run from start without a gap.
func Decode(body []byte) ([]Block, error) {
	if len(body) < headerLen || string(body[:4]) != bundleMagic || body[4] != bundleVersion {
		return nil, fmt.Errorf("%w: not an ORBH v%d bundle", ErrBundle, bundleVersion)
	}
	start := int64(binary.BigEndian.Uint64(body[5:13]))
	count := binary.BigEndian.Uint32(body[13:17])
	rest := body[headerLen:]
	blocks := make([]Block, 0, min(int(count), len(rest)/(types.HashLen+4)))
	for i := uint32(0); i < count; i++ {
		if len(rest) < types.HashLen+4 {
			return nil, fmt.Errorf("%w: truncated at block %d", ErrBundle, i)
		}
		hash := rest[:types.HashLen]
		n := binary.BigEndian.Uint32(rest[types.HashLen : types.HashLen+4])
		rest = rest[types.HashLen+4:]
		if n == 0 || n > MaxBlockBytes || int(n) > len(rest) {
			return nil, fmt.Errorf("%w: block %d length %d", ErrBundle, i, n)
		}
		b := Block{Height: start + int64(i), Hash: append([]byte(nil), hash...), Proto: append([]byte(nil), rest[:n]...)}
		rest = rest[n:]
		if err := checkBlock(b); err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: %d trailing bytes", ErrBundle, len(rest))
	}
	return blocks, nil
}

func checkBlock(b Block) error {
	var pb cmtproto.Block
	if err := pb.Unmarshal(b.Proto); err != nil {
		return fmt.Errorf("%w: block %d does not decode: %v", ErrBundle, b.Height, err)
	}
	blk, err := cmttypes.BlockFromProto(&pb)
	if err != nil {
		return fmt.Errorf("%w: block %d: %v", ErrBundle, b.Height, err)
	}
	if blk.Height != b.Height {
		return fmt.Errorf("%w: block at %d says height %d", ErrBundle, b.Height, blk.Height)
	}
	if !bytes.Equal(blk.Hash(), b.Hash) {
		return fmt.Errorf("%w: block %d bytes do not hash to its header hash", ErrBundle, b.Height)
	}
	return nil
}

// Verify checks a whole bundle against the range record on chain: the
// content hash, every block's header hash, and the block-hash Merkle root.
func Verify(body []byte, rec types.RangeRecord) ([]Block, error) {
	blocks, err := Decode(body)
	if err != nil {
		return nil, err
	}
	if blocks[0].Height != rec.StartHeight || blocks[len(blocks)-1].Height != rec.EndHeight {
		return nil, fmt.Errorf("%w: bundle is %d-%d, range is %d-%d", ErrBundle,
			blocks[0].Height, blocks[len(blocks)-1].Height, rec.StartHeight, rec.EndHeight)
	}
	hashes := make([][]byte, len(blocks))
	for i, b := range blocks {
		hashes[i] = b.Hash
	}
	sum := sha256.Sum256(body)
	if !bytes.Equal(sum[:], rec.BundleHash) {
		return nil, fmt.Errorf("%w: content hash differs from the chain", ErrBundle)
	}
	if err := types.VerifyBundle(hashes, rec.BundleHash, rec.MerkleRoot); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBundle, err)
	}
	return blocks, nil
}

// CID is the CIDv1 (raw codec, sha2-256) of body, base32 lower case.
func CID(body []byte) string {
	sum := sha256.Sum256(body)
	raw := append([]byte{0x01, 0x55, 0x12, 0x20}, sum[:]...)
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	return "b" + strings.ToLower(enc)
}
