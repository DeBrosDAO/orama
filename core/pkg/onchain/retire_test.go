package onchain

import (
	"bytes"
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

func TestRetireNode_sendsMsgRetireNodeForTheSigningOperator(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()

	receipt, err := newClient(t, chain, signer).RetireNode(context.Background(), "node-a")

	if err != nil {
		t.Fatalf("RetireNode: %v", err)
	}
	if receipt == nil || receipt.Hash == "" {
		t.Errorf("receipt = %+v", receipt)
	}
	if got := typeURLOf(t, signer.signed[0]); got != clusterreg.RetireNodeTypeURL {
		t.Errorf("type = %s, want MsgRetireNode", got)
	}
	if !bytes.Contains(chain.sent[0], []byte(testOperator)) || !bytes.Contains(chain.sent[0], []byte("node-a")) {
		t.Error("the transaction does not name the operator and the node")
	}
}

func TestRetireNode_refusesABadNodeIDBeforeTheChainIsAsked(t *testing.T) {
	for _, id := range []string{"", "bad id", "a/b"} {
		chain := newFakeChain()

		if _, err := newClient(t, chain, newSigner()).RetireNode(context.Background(), id); err == nil {
			t.Errorf("id %q was accepted", id)
		}
		if len(chain.simulated) != 0 {
			t.Errorf("id %q reached the chain", id)
		}
	}
}

func TestRetireNode_aRefusedBlockIsAnError(t *testing.T) {
	chain := newFakeChain()
	chain.waitErr = context.DeadlineExceeded

	if _, err := newClient(t, chain, newSigner()).RetireNode(context.Background(), "node-a"); err == nil {
		t.Fatal("a transaction the block refused was reported as retired")
	}
}
