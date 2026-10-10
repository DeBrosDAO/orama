package onchain

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"strings"
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

// Once the chain has taken the transaction, every later error says so: it may still be in a block.
func TestFaucet_anErrorAfterTheBroadcastIsASentError(t *testing.T) {
	for name, edit := range map[string]func(*fakeChain){
		"the wait ends":                func(c *fakeChain) { c.waitErr = context.DeadlineExceeded },
		"the chain answers another tx": func(c *fakeChain) { c.answerHash = strings.Repeat("AB", 32) },
	} {
		t.Run(name, func(t *testing.T) {
			chain := newFakeChain()
			edit(chain)

			_, err := newClient(t, chain, newSigner()).Faucet(context.Background(), testRecipient, big.NewInt(1))

			var sent *SentError
			if !errors.As(err, &sent) || len(sent.Hash) != 64 {
				t.Fatalf("err = %v, want a SentError with the hash", err)
			}
		})
	}
}

func TestFaucet_anErrorBeforeTheBroadcastIsNotASentError(t *testing.T) {
	for name, edit := range map[string]func(*fakeChain){
		"the simulation refuses": func(c *fakeChain) { c.simErr = errors.New("refused") },
		"the broadcast fails":    func(c *fakeChain) { c.broadcast = errors.New("mempool full") },
	} {
		t.Run(name, func(t *testing.T) {
			chain := newFakeChain()
			edit(chain)

			_, err := newClient(t, chain, newSigner()).Faucet(context.Background(), testRecipient, big.NewInt(1))

			var sent *SentError
			if err == nil || errors.As(err, &sent) {
				t.Fatalf("err = %v, want an error from before the transaction was taken", err)
			}
		})
	}
}
