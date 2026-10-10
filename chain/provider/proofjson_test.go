package provider

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func TestWriteProofsMatchesTheProveCommandFile(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte{4}, piece.LeafSize*3-10)
	committed, err := piece.Commit(data)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Ingest("piece-a", data, committed.Root)
	if err != nil || !got.Accept {
		t.Fatalf("ingest %+v %v", got, err)
	}
	if err := s.Bind("piece-a", 7, 0); err != nil {
		t.Fatal(err)
	}
	const (
		epoch  uint64 = 2
		nodeID        = "node-a"
	)
	leaf, err := piece.LeafIndex(types.LeafChallengeSeed(epoch, 7, 0, nodeID), committed.RealLeafCount)
	if err != nil {
		t.Fatal(err)
	}
	proofs, missing, err := s.AnswerChallenges(epoch, nodeID, []types.Challenge{{
		DealId: 7, Slot: 0, LeafIndex: leaf,
	}})
	if err != nil || len(missing) != 0 || len(proofs) != 1 {
		t.Fatalf("answer %+v missing %+v %v", proofs, missing, err)
	}
	var buf bytes.Buffer
	if err := WriteProofs(&buf, proofs); err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		DealID    uint64   `json:"deal_id"`
		Slot      uint32   `json:"slot"`
		LeafIndex uint64   `json:"leaf_index"`
		Leaf      string   `json:"leaf"`
		Siblings  []string `json:"siblings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].DealID != 7 || rows[0].Slot != 0 || rows[0].LeafIndex != leaf {
		t.Fatalf("row %+v", rows)
	}
	rawLeaf, err := hex.DecodeString(rows[0].Leaf)
	if err != nil || !bytes.Equal(rawLeaf, proofs[0].Leaf) {
		t.Fatalf("leaf %v", err)
	}
	if len(rows[0].Siblings) != len(proofs[0].Siblings) || len(rows[0].Siblings) == 0 {
		t.Fatalf("siblings %d", len(rows[0].Siblings))
	}
	for i, sib := range rows[0].Siblings {
		raw, err := hex.DecodeString(sib)
		if err != nil || !bytes.Equal(raw, proofs[0].Siblings[i]) || len(raw) != 32 {
			t.Fatalf("sibling %d %v", i, err)
		}
		if sib != strings.ToLower(sib) {
			t.Fatalf("sibling hex %s", sib)
		}
	}
	if err := WriteProofs(&buf, nil); err == nil {
		t.Fatal("empty proof list was written")
	}
}
