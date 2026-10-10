package provider

import (
	"bytes"
	"testing"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func TestBindRestartsAndAnswersChallenges(t *testing.T) {
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
	if _, err := s.Ingest("piece-a", data, committed.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest("piece-b", data, committed.Root); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("missing", 7, 1); err == nil {
		t.Fatal("unbound cid was accepted")
	}
	if err := s.Bind("piece-a", 0, 1); err == nil {
		t.Fatal("deal 0 was accepted")
	}
	if err := s.Bind("piece-a", 7, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("piece-a", 7, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("piece-b", 7, 1); err == nil {
		t.Fatal("slot was rebound")
	}
	if err := s.Bind("piece-a", 7, 0); err != nil {
		t.Fatal(err)
	}

	again, err := Open(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cid, ok, err := again.Lookup(7, 1)
	if err != nil || !ok || cid != "piece-a" {
		t.Fatalf("lookup %q %v %v", cid, ok, err)
	}
	const (
		epoch  uint64 = 2
		nodeID        = "node-a"
	)
	want, err := piece.LeafIndex(types.LeafChallengeSeed(epoch, 7, 1, nodeID), committed.RealLeafCount)
	if err != nil {
		t.Fatal(err)
	}
	slot0, err := piece.LeafIndex(types.LeafChallengeSeed(epoch, 7, 0, nodeID), committed.RealLeafCount)
	if err != nil {
		t.Fatal(err)
	}
	challenges := []types.Challenge{
		{DealId: 7, Slot: 1, LeafIndex: want, Proved: true},
		{DealId: 9, Slot: 3, LeafIndex: want},
		{DealId: 7, Slot: 0, LeafIndex: slot0},
	}
	proofs, missing, err := again.AnswerChallenges(epoch, nodeID, challenges)
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 1 || proofs[0].DealId != 7 || proofs[0].Slot != 0 || proofs[0].LeafIndex != slot0 {
		t.Fatalf("proofs %+v", proofs)
	}
	if len(missing) != 1 || missing[0].DealId != 9 {
		t.Fatalf("missing %+v", missing)
	}
	open := types.Challenge{DealId: 7, Slot: 1, LeafIndex: want}
	proofs, missing, err = again.AnswerChallenges(epoch, nodeID, []types.Challenge{open})
	if err != nil || len(missing) != 0 || len(proofs) != 1 || proofs[0].LeafIndex != want {
		t.Fatalf("open %+v missing %+v %v", proofs, missing, err)
	}
	bad := open
	bad.LeafIndex = want + 1
	if _, _, err := again.AnswerChallenges(epoch, nodeID, []types.Challenge{bad}); err == nil {
		t.Fatal("wrong leaf was accepted")
	}
	if _, _, err := again.AnswerChallenges(epoch, "", []types.Challenge{open}); err == nil {
		t.Fatal("empty node was accepted")
	}
}
