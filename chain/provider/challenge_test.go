package provider

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func TestReplicaProofMatchesTheChainLeaf(t *testing.T) {
	sdk.GetConfig().SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
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
	const (
		dealID uint64 = 7
		slot   uint32 = 1
		nodeID        = "node-a"
	)
	for _, epoch := range []uint64{1, 2, 40} {
		proof, err := s.ReplicaProof("piece-a", epoch, dealID, slot, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		want, err := piece.LeafIndex(types.LeafChallengeSeed(epoch, dealID, slot, nodeID), committed.RealLeafCount)
		if err != nil {
			t.Fatal(err)
		}
		if proof.LeafIndex != want || proof.LeafIndex >= committed.RealLeafCount {
			t.Fatalf("epoch %d leaf %d want %d real %d", epoch, proof.LeafIndex, want, committed.RealLeafCount)
		}
		if err := piece.VerifyChallenge(committed, piece.Proof{
			Index: proof.LeafIndex, Leaf: proof.Leaf, Siblings: proof.Siblings,
		}); err != nil {
			t.Fatal(err)
		}
		msg := &types.MsgSubmitProofs{
			Signer: "orama1qyqszqgpqyqszqgpqyqszqgpqyqszqgp6cszae",
			NodeId: nodeID,
			Proofs: []types.ReplicaProof{proof},
		}
		if err := msg.ValidateBasic(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ReplicaProof("missing", 1, dealID, slot, nodeID); err == nil {
		t.Fatal("missing piece produced a proof")
	}
	if _, err := s.ReplicaProof("piece-a", 1, 0, slot, nodeID); err == nil {
		t.Fatal("deal 0 produced a proof")
	}
	if _, err := s.ReplicaProof("piece-a", 1, dealID, slot, ""); err == nil {
		t.Fatal("empty node produced a proof")
	}
}
