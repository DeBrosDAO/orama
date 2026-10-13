package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// FuzzProviderProofJSON: the proof file `orama storage prove --file` reads is written without a
// panic for any proof, a proof of the wrong shape is refused with nothing written, and a file that
// is written is one JSON array whose hex decodes back to exactly the proofs given.
func FuzzProviderProofJSON(f *testing.F) {
	data := bytes.Repeat([]byte{4}, piece.LeafSize*3)
	p, _ := piece.Prove(data, 1)
	f.Add(uint64(7), uint32(0), uint64(1), p.Leaf, bytes.Join(p.Siblings, nil), uint8(2))
	f.Add(uint64(0), uint32(0), uint64(0), p.Leaf, []byte{}, uint8(1))
	f.Add(uint64(1), uint32(9), uint64(3), []byte("short"), []byte{}, uint8(0))
	f.Fuzz(func(t *testing.T, deal uint64, slot uint32, leafIndex uint64, leaf, siblings []byte, count uint8) {
		var sibs [][]byte
		for len(siblings) >= sha256.Size {
			sibs = append(sibs, siblings[:sha256.Size])
			siblings = siblings[sha256.Size:]
		}
		if len(siblings) > 0 {
			sibs = append(sibs, siblings)
		}
		var proofs []types.ReplicaProof
		for i := 0; i < int(count%4); i++ {
			proofs = append(proofs, types.ReplicaProof{DealId: deal, Slot: slot + uint32(i), LeafIndex: leafIndex, Leaf: leaf, Siblings: sibs})
		}
		var buf bytes.Buffer
		err := WriteProofs(&buf, proofs)
		wellFormed := len(proofs) > 0 && deal != 0 && len(leaf) == piece.LeafSize
		for _, s := range sibs {
			wellFormed = wellFormed && len(s) == sha256.Size
		}
		if (err == nil) != wellFormed {
			t.Fatalf("WriteProofs error %v for a proof that is well formed=%v", err, wellFormed)
		}
		if err != nil {
			if buf.Len() != 0 {
				t.Fatal("a refused proof set still wrote bytes")
			}
			return
		}
		var rows []proofFileRow
		if err := json.Unmarshal(buf.Bytes(), &rows); err != nil || len(rows) != len(proofs) {
			t.Fatalf("the file is not an array of %d rows: %v", len(proofs), err)
		}
		for i, row := range rows {
			got, err := hex.DecodeString(row.Leaf)
			if err != nil || !bytes.Equal(got, leaf) || row.DealID != deal || row.Slot != proofs[i].Slot || row.LeafIndex != leafIndex || len(row.Siblings) != len(sibs) {
				t.Fatalf("row %d does not carry the proof it was written from", i)
			}
			for j, s := range row.Siblings {
				if b, err := hex.DecodeString(s); err != nil || !bytes.Equal(b, sibs[j]) {
					t.Fatalf("row %d sibling %d is not the sibling given", i, j)
				}
			}
		}
	})
}
