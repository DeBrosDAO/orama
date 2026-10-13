package provider

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/piece"
)

func TestDecideBindsAMatchingRootAndDeclinesAMissingOne(t *testing.T) {
	sdk.GetConfig().SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
	dir := t.TempDir()
	data := bytes.Repeat([]byte{5}, 64)
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
	const signer = "orama1qyqszqgpqyqszqgpqyqszqgpqyqszqgp6cszae"
	accept, decline, err := s.Decide(signer, "node-a", 7, 1, committed.Root)
	if err != nil || decline != nil || accept == nil {
		t.Fatalf("accept %+v decline %+v %v", accept, decline, err)
	}
	if accept.DealId != 7 || accept.Slot != 1 || accept.NodeId != "node-a" {
		t.Fatalf("accept %+v", accept)
	}
	cid, ok, err := s.Lookup(7, 1)
	if err != nil || !ok || cid != "piece-a" {
		t.Fatalf("lookup %q %v %v", cid, ok, err)
	}
	missing := bytes.Repeat([]byte{9}, 32)
	accept, decline, err = s.Decide(signer, "node-a", 8, 0, missing)
	if err != nil || accept != nil || decline == nil || decline.Reason != "piece not stored" {
		t.Fatalf("decline %+v accept %+v %v", decline, accept, err)
	}
	if _, ok, err := s.Lookup(8, 0); err != nil || ok {
		t.Fatalf("missing was bound %v", err)
	}
}
