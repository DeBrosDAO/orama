package onchain

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

func TestFaucet_sendsMsgFaucetFromTheSigningAccount(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()

	receipt, err := newClient(t, chain, signer).Faucet(context.Background(), testRecipient, big.NewInt(100_000_000_000))

	if err != nil {
		t.Fatalf("Faucet: %v", err)
	}
	if receipt == nil || receipt.Hash == "" {
		t.Errorf("receipt = %+v", receipt)
	}
	if got := typeURLOf(t, signer.signed[0]); got != clusterreg.FaucetTypeURL {
		t.Errorf("type = %s, want MsgFaucet", got)
	}
	for _, want := range []string{testOperator, testRecipient, "100000000000"} {
		if !bytes.Contains(chain.sent[0], []byte(want)) {
			t.Errorf("the transaction does not carry %q", want)
		}
	}
}

func TestFaucet_refusesABadDripBeforeTheChainIsAsked(t *testing.T) {
	for name, tc := range map[string]struct {
		recipient string
		amount    *big.Int
	}{
		"no recipient":    {"", big.NewInt(1)},
		"a bad recipient": {"orama1nope", big.NewInt(1)},
		"no amount":       {testRecipient, nil},
		"a zero amount":   {testRecipient, big.NewInt(0)},
		"a negative one":  {testRecipient, big.NewInt(-1)},
	} {
		t.Run(name, func(t *testing.T) {
			chain := newFakeChain()
			if _, err := newClient(t, chain, newSigner()).Faucet(context.Background(), tc.recipient, tc.amount); err == nil {
				t.Fatal("the drip was accepted")
			}
			if len(chain.simulated) != 0 {
				t.Error("the drip reached the chain")
			}
		})
	}
}

// The simulation meets the chain's refusal first: nothing is signed, so the faucet pays no fee.
func TestFaucet_aRefusalInTheSimulationSignsNothing(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	chain.simErr = context.DeadlineExceeded

	if _, err := newClient(t, chain, signer).Faucet(context.Background(), testRecipient, big.NewInt(1)); err == nil {
		t.Fatal("a refused drip was reported as made")
	}
	if len(signer.signed) != 0 || len(chain.sent) != 0 {
		t.Errorf("signed %d, sent %d", len(signer.signed), len(chain.sent))
	}
}
