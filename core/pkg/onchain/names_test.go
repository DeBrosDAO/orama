package onchain

import (
	"bytes"
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

func TestClaimNodeName_sendsMsgClaimNodeNameForTheSigningOperator(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()

	receipt, err := newClient(t, chain, signer).ClaimNodeName(context.Background(), "node-a", "alpha-one")

	if err != nil {
		t.Fatalf("ClaimNodeName: %v", err)
	}
	if receipt == nil || receipt.Hash == "" {
		t.Errorf("receipt = %+v", receipt)
	}
	if got := typeURLOf(t, signer.signed[0]); got != clusterreg.ClaimNodeNameTypeURL {
		t.Errorf("type = %s, want MsgClaimNodeName", got)
	}
	for _, want := range []string{testOperator, "node-a", "alpha-one"} {
		if !bytes.Contains(chain.sent[0], []byte(want)) {
			t.Errorf("the transaction does not carry %q", want)
		}
	}
}

func TestClaimNodeName_refusesABadClaimBeforeTheChainIsAsked(t *testing.T) {
	for name, args := range map[string][2]string{
		"no node id": {"", "alpha-one"},
		"a bad id":   {"bad id", "alpha-one"},
		"no name":    {"node-a", ""},
	} {
		t.Run(name, func(t *testing.T) {
			chain := newFakeChain()
			if _, err := newClient(t, chain, newSigner()).ClaimNodeName(context.Background(), args[0], args[1]); err == nil {
				t.Fatal("the claim was accepted")
			}
			if len(chain.simulated) != 0 {
				t.Error("the claim reached the chain")
			}
		})
	}
}

func TestClaimNodeName_aRefusedBlockIsAnError(t *testing.T) {
	chain := newFakeChain()
	chain.waitErr = context.DeadlineExceeded
	if _, err := newClient(t, chain, newSigner()).ClaimNodeName(context.Background(), "node-a", "alpha-one"); err == nil {
		t.Fatal("a claim the block refused was reported as made")
	}
}

func TestReleaseNodeName_sendsMsgReleaseNodeNameForTheSigningOperator(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()

	if _, err := newClient(t, chain, signer).ReleaseNodeName(context.Background(), "node-a"); err != nil {
		t.Fatalf("ReleaseNodeName: %v", err)
	}
	if got := typeURLOf(t, signer.signed[0]); got != clusterreg.ReleaseNodeNameTypeURL {
		t.Errorf("type = %s, want MsgReleaseNodeName", got)
	}
	if !bytes.Contains(chain.sent[0], []byte(testOperator)) || !bytes.Contains(chain.sent[0], []byte("node-a")) {
		t.Error("the transaction does not name the operator and the node")
	}
}

func TestReleaseNodeName_refusesABadNodeIDBeforeTheChainIsAsked(t *testing.T) {
	for _, id := range []string{"", "bad id", "a/b"} {
		chain := newFakeChain()
		if _, err := newClient(t, chain, newSigner()).ReleaseNodeName(context.Background(), id); err == nil {
			t.Errorf("id %q was accepted", id)
		}
		if len(chain.simulated) != 0 {
			t.Errorf("id %q reached the chain", id)
		}
	}
}
