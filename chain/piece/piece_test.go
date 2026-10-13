package piece

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDomainSeparation(t *testing.T) {
	data := bytesOf(LeafSize, 0x11)
	c, err := Commit(data)
	require.NoError(t, err)

	raw := sha256.Sum256(data)
	require.NotEqual(t, raw[:], c.Root, "leaf tag must change the hash")

	h := sha256.New()
	h.Write([]byte{TagLeaf})
	h.Write(data)
	require.Equal(t, h.Sum(nil), c.Root, "a single full leaf is only the tagged leaf hash")
	require.Equal(t, uint64(1), c.RealLeafCount)
	require.Equal(t, uint64(1), c.PaddedLeafCount)
}

func TestEmptyPiece(t *testing.T) {
	c, err := Commit(nil)
	require.NoError(t, err)
	require.Equal(t, EmptyRoot(), c.Root)
	require.Zero(t, c.RealLeafCount)
	require.Zero(t, c.PaddedLeafCount)
	_, err = Prove(nil, 0)
	require.Error(t, err)
	_, err = LeafIndex([]byte("seed"), 0)
	require.Error(t, err)
}

func TestPaddingNeverChallenged(t *testing.T) {
	// 3 real leaves pad to 4. Index 3 is the padding leaf.
	data := bytesOf(3*LeafSize, 0x5a)
	c, err := Commit(data)
	require.NoError(t, err)
	require.Equal(t, uint64(3), c.RealLeafCount)
	require.Equal(t, uint64(4), c.PaddedLeafCount)

	pad, err := Prove(data, 3)
	require.NoError(t, err)
	require.NoError(t, Verify(c, pad), "a padding leaf is in the tree")
	require.Error(t, VerifyChallenge(c, pad), "padding must not verify as a challenge")

	for i := 0; i < 2000; i++ {
		seed := []byte{byte(i), byte(i >> 8), byte(i >> 16), 0x7e}
		idx, err := LeafIndex(seed, c.RealLeafCount)
		require.NoError(t, err)
		require.Less(t, idx, c.RealLeafCount)
	}
}

func TestProofSizeGrowsWithPaddedTree(t *testing.T) {
	three := bytesOf(3*LeafSize, 0x01) // pads to 4, height 2
	five := bytesOf(5*LeafSize, 0x02)  // pads to 8, height 3
	p3, err := Prove(three, 2)         // last real leaf
	require.NoError(t, err)
	p5, err := Prove(five, 0) // first leaf
	require.NoError(t, err)
	require.Greater(t, len(p5.Siblings), len(p3.Siblings))

	size4, err := ProofSize(4)
	require.NoError(t, err)
	require.Equal(t, LeafSize+2*32, size4)
	require.Equal(t, size4, LeafSize+len(p3.Siblings)*32)

	size8, err := ProofSize(8)
	require.NoError(t, err)
	require.Equal(t, LeafSize+3*32, size8)
	require.Greater(t, size8, size4)

	// 64 GiB is 2^26 leaves of 1 KiB: 1024 + 26*32 = 1856.
	size64, err := ProofSize(1 << 26)
	require.NoError(t, err)
	require.Equal(t, 1856, size64)

	_, err = ProofSize(3)
	require.Error(t, err)
}

func TestDeterministicChallenges(t *testing.T) {
	seed := []byte("orama-challenge")
	a, err := LeafIndex(seed, 1000)
	require.NoError(t, err)
	b, err := LeafIndex(append([]byte(nil), seed...), 1000)
	require.NoError(t, err)
	require.Equal(t, a, b)
	c, err := LeafIndex(append(seed, 0x01), 1000)
	require.NoError(t, err)
	require.NotEqual(t, a, c)
}

func TestPartialTailLeaf(t *testing.T) {
	data := []byte{0xab}
	c, err := Commit(data)
	require.NoError(t, err)
	require.Equal(t, uint64(1), c.RealLeafCount)
	p, err := Prove(data, 0)
	require.NoError(t, err)
	require.Len(t, p.Leaf, LeafSize)
	require.Equal(t, byte(0xab), p.Leaf[0])
	require.Equal(t, byte(0), p.Leaf[1])
	require.NoError(t, VerifyChallenge(c, p))
	p.Leaf[0] ^= 0xff
	require.Error(t, VerifyChallenge(c, p))
}

func TestVectors(t *testing.T) {
	if os.Getenv("WRITE_VECTORS") == "1" {
		require.NoError(t, writeVectors(t))
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "vectors.json"))
	require.NoError(t, err)
	var doc vectorFile
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.Equal(t, LeafSize, doc.LeafSize)
	require.Equal(t, 1856, doc.ProofSize64GiB)
	require.NotEmpty(t, doc.Cases)

	for _, tc := range doc.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			data, err := hex.DecodeString(tc.DataHex)
			require.NoError(t, err)
			c, err := Commit(data)
			require.NoError(t, err)
			require.Equal(t, tc.RealLeafCount, c.RealLeafCount)
			require.Equal(t, tc.PaddedLeafCount, c.PaddedLeafCount)
			require.Equal(t, tc.RootHex, hex.EncodeToString(c.Root))

			for _, vp := range tc.Proofs {
				proof, err := Prove(data, vp.Index)
				require.NoError(t, err)
				require.Equal(t, vp.LeafHex, hex.EncodeToString(proof.Leaf))
				require.Len(t, proof.Siblings, len(vp.SiblingsHex))
				for i, sib := range proof.Siblings {
					require.Equal(t, vp.SiblingsHex[i], hex.EncodeToString(sib))
				}
				require.NoError(t, Verify(c, proof))
			}
			require.Len(t, tc.ChallengeIndexes, len(tc.ChallengeSeedsHex))
			for i, seedHex := range tc.ChallengeSeedsHex {
				seed, err := hex.DecodeString(seedHex)
				require.NoError(t, err)
				idx, err := LeafIndex(seed, c.RealLeafCount)
				require.NoError(t, err)
				require.Equal(t, tc.ChallengeIndexes[i], idx)
				require.Less(t, idx, c.RealLeafCount)
			}
		})
	}
}

type vectorFile struct {
	LeafSize       int          `json:"leaf_size"`
	ProofSize64GiB int          `json:"proof_size_64gib"`
	Cases          []vectorCase `json:"cases"`
}

type vectorCase struct {
	Name              string        `json:"name"`
	DataHex           string        `json:"data_hex"`
	RealLeafCount     uint64        `json:"real_leaf_count"`
	PaddedLeafCount   uint64        `json:"padded_leaf_count"`
	RootHex           string        `json:"root_hex"`
	Proofs            []vectorProof `json:"proofs"`
	ChallengeSeedsHex []string      `json:"challenge_seeds_hex"`
	ChallengeIndexes  []uint64      `json:"challenge_indexes"`
}

type vectorProof struct {
	Index       uint64   `json:"index"`
	LeafHex     string   `json:"leaf_hex"`
	SiblingsHex []string `json:"siblings_hex"`
}

func writeVectors(t *testing.T) error {
	t.Helper()
	cases := []struct {
		name    string
		data    []byte
		proofs  []uint64
		seeds   [][]byte
		realMod bool
	}{
		{name: "empty", data: nil},
		{name: "one_byte", data: []byte{0xab}, proofs: []uint64{0}, seeds: [][]byte{{0x01}, {0x02, 0x03}}},
		{name: "one_leaf", data: bytesOf(LeafSize, 0x11), proofs: []uint64{0}, seeds: [][]byte{{0x10}}},
		{name: "leaf_boundary_plus", data: append(bytesOf(LeafSize, 0x22), 0x33), proofs: []uint64{0, 1}, seeds: [][]byte{{0x20}, {0x21}}},
		{name: "three_leaves", data: bytesOf(3*LeafSize, 0x44), proofs: []uint64{0, 2, 3}, seeds: [][]byte{{0x01}, {0x02}, {0xff, 0x00}, {0x7e, 0x01}}},
	}
	doc := vectorFile{LeafSize: LeafSize, ProofSize64GiB: 1856}
	for _, tc := range cases {
		c, err := Commit(tc.data)
		if err != nil {
			return err
		}
		vc := vectorCase{
			Name:              tc.name,
			DataHex:           hex.EncodeToString(tc.data),
			RealLeafCount:     c.RealLeafCount,
			PaddedLeafCount:   c.PaddedLeafCount,
			RootHex:           hex.EncodeToString(c.Root),
			Proofs:            []vectorProof{},
			ChallengeSeedsHex: []string{},
			ChallengeIndexes:  []uint64{},
		}
		for _, idx := range tc.proofs {
			p, err := Prove(tc.data, idx)
			if err != nil {
				return err
			}
			vp := vectorProof{Index: idx, LeafHex: hex.EncodeToString(p.Leaf), SiblingsHex: []string{}}
			for _, sib := range p.Siblings {
				vp.SiblingsHex = append(vp.SiblingsHex, hex.EncodeToString(sib))
			}
			vc.Proofs = append(vc.Proofs, vp)
		}
		if c.RealLeafCount > 0 {
			for _, seed := range tc.seeds {
				idx, err := LeafIndex(seed, c.RealLeafCount)
				if err != nil {
					return err
				}
				vc.ChallengeSeedsHex = append(vc.ChallengeSeedsHex, hex.EncodeToString(seed))
				vc.ChallengeIndexes = append(vc.ChallengeIndexes, idx)
			}
		}
		doc.Cases = append(doc.Cases, vc)
	}
	buf, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join("testdata", "vectors.json"), buf, 0o644)
}

func bytesOf(n int, b byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
