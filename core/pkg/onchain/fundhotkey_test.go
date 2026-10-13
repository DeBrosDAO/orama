package onchain

import (
	"bytes"
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

func TestFundHotKey_fillsTheOperatorAndSendsTheBankSource(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	_, err := newClient(t, chain, signer).FundHotKey(context.Background(), clusterreg.HotKeyFunding{
		NodeID: "node-a", Amount: "1000000000", FromBank: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := typeURLOf(t, signer.signed[0]); got != clusterreg.FundHotKeyTypeURL {
		t.Errorf("type = %s", got)
	}
	if !bytes.Contains(chain.sent[0], []byte(testOperator)) || !bytes.Contains(chain.sent[0], []byte("1000000000")) {
		t.Error("the funding does not name the operator and the amount")
	}
	if !bytes.Contains(chain.sent[0], []byte{0x20, 0x01}) {
		t.Error("the funding does not carry the bank source (field 4, value 1)")
	}
}

func TestFundHotKey_refusesAnotherOperatorAndBadFacts(t *testing.T) {
	for name, f := range map[string]clusterreg.HotKeyFunding{
		"another operator": {Operator: "orama1other", NodeID: "node-a", Amount: "1"},
		"zero amount":      {NodeID: "node-a", Amount: "0"},
		"bad node id":      {NodeID: "bad id", Amount: "1"},
	} {
		t.Run(name, func(t *testing.T) {
			chain := newFakeChain()
			if _, err := newClient(t, chain, newSigner()).FundHotKey(context.Background(), f); err == nil {
				t.Fatal("FundHotKey accepted it")
			}
			if len(chain.simulated) != 0 {
				t.Error("an invalid funding reached the chain")
			}
		})
	}
}
