package onchain

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

func TestRegisterOperator_derivesEverythingFromTheChainAndTheSigner(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	c := newClient(t, chain, signer)

	receipt, err := c.RegisterOperator(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Height != 55 || receipt.Hash == "" {
		t.Errorf("receipt = %+v", receipt)
	}
	if len(signer.signed) != 1 || len(chain.sent) != 1 || len(chain.simulated) != 1 {
		t.Fatalf("signed %d, sent %d, simulated %d; want one each", len(signer.signed), len(chain.sent), len(chain.simulated))
	}
	doc := decodeSignDoc(t, signer.signed[0])
	// 100,000 gas used x 1.5 = 150,000; fee = 10 x 150,000 x 1.5 = 2,250,000.
	want := signDocFields{
		chainID: testChainID, accountNumber: 42, sequence: 7, gas: 150_000, fee: "2250000",
		typeURL: clusterreg.RegisterOperatorTypeURL, pubKey: testKey(),
	}
	if doc.chainID != want.chainID || doc.accountNumber != want.accountNumber || doc.sequence != want.sequence ||
		doc.gas != want.gas || doc.fee != want.fee || doc.typeURL != want.typeURL || !bytes.Equal(doc.pubKey, want.pubKey) {
		t.Errorf("sign doc = %+v, want %+v", doc, want)
	}
	if receipt.Gas != 150_000 || receipt.Fee != "2250000" {
		t.Errorf("receipt gas/fee = %d/%s", receipt.Gas, receipt.Fee)
	}
	if !bytes.Contains(chain.sent[0], bytes.Repeat([]byte{9}, 64)) {
		t.Error("the broadcast transaction does not carry the RootWallet's signature")
	}
}

func TestSend_simulatesTheSameTransactionItSends(t *testing.T) {
	chain := newFakeChain()
	c := newClient(t, chain, newSigner())
	if _, err := c.RegisterOperator(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(chain.simulated[0], []byte(clusterreg.RegisterOperatorTypeURL)) {
		t.Error("the simulation did not carry the message")
	}
	if !bytes.Contains(chain.simulated[0], bytes.Repeat([]byte{0}, 64)) {
		t.Error("the simulation did not use a placeholder signature")
	}
}

func TestSend_asksTheAgentForItsAccountOnce(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	c := newClient(t, chain, signer)
	for i := 0; i < 3; i++ {
		if _, err := c.RegisterOperator(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if signer.accountCalls != 1 {
		t.Errorf("the agent was asked for its account %d times, want 1", signer.accountCalls)
	}
}

func TestSend_aZeroBaseFeeStillPaysOneNorama(t *testing.T) {
	chain := newFakeChain()
	chain.baseFee = "0"
	receipt, err := newClient(t, chain, newSigner()).RegisterOperator(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Fee != "1" {
		t.Errorf("fee = %s, want 1", receipt.Fee)
	}
}

func TestSend_aSmallGasRoundsUp(t *testing.T) {
	if got := scaleUp(1, GasSafetyNumerator, GasSafetyDenominator); got != 2 {
		t.Errorf("scaleUp(1) = %d, want 2", got)
	}
	if got := scaleUp(0, GasSafetyNumerator, GasSafetyDenominator); got != 0 {
		t.Errorf("scaleUp(0) = %d, want 0", got)
	}
}

func TestSend_aFailureAtAnyStepStopsBeforeTheNext(t *testing.T) {
	boom := errors.New("boom")
	for name, tc := range map[string]struct {
		edit       func(*fakeChain, *fakeSigner)
		wantIn     string
		wantSigned int
		wantSent   int
	}{
		"agent locked":      {func(_ *fakeChain, s *fakeSigner) { s.accountErr = boom }, "RootWallet", 0, 0},
		"account lookup":    {func(c *fakeChain, _ *fakeSigner) { c.accountErr = boom }, "chain account", 0, 0},
		"simulation refuse": {func(c *fakeChain, _ *fakeSigner) { c.simErr = boom }, "simulate", 0, 0},
		"owner declines":    {func(_ *fakeChain, s *fakeSigner) { s.signErr = boom }, "sign", 1, 0},
		"broadcast refused": {func(c *fakeChain, _ *fakeSigner) { c.broadcast = boom }, "broadcast", 1, 1},
		"block refuses":     {func(c *fakeChain, _ *fakeSigner) { c.waitErr = boom }, "transaction ABCDEF", 1, 1},
	} {
		t.Run(name, func(t *testing.T) {
			chain, signer := newFakeChain(), newSigner()
			tc.edit(chain, signer)
			_, err := newClient(t, chain, signer).RegisterOperator(context.Background())
			if !errors.Is(err, boom) || !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("error = %v, want it to wrap boom and mention %q", err, tc.wantIn)
			}
			if len(signer.signed) != tc.wantSigned || len(chain.sent) != tc.wantSent {
				t.Errorf("signed %d, sent %d; want %d, %d", len(signer.signed), len(chain.sent), tc.wantSigned, tc.wantSent)
			}
		})
	}
}

func TestSend_anAccountTheChainHasNotSeenSaysToFundIt(t *testing.T) {
	chain := newFakeChain()
	chain.accountErr = &clusterreg.StatusError{Code: http.StatusNotFound}
	_, err := newClient(t, chain, newSigner()).RegisterOperator(context.Background())
	if !errors.Is(err, ErrAccountNotFound) || !strings.Contains(err.Error(), testOperator) {
		t.Fatalf("error = %v, want ErrAccountNotFound naming the account", err)
	}
}

func TestSend_refusesASignatureFromAnotherKey(t *testing.T) {
	for name, edit := range map[string]func(*fakeSigner){
		"another address": func(s *fakeSigner) { s.asAddress = "orama1other" },
		"another key":     func(s *fakeSigner) { s.asKey = bytes.Repeat([]byte{2}, 33) },
	} {
		t.Run(name, func(t *testing.T) {
			chain, signer := newFakeChain(), newSigner()
			edit(signer)
			if _, err := newClient(t, chain, signer).RegisterOperator(context.Background()); err == nil {
				t.Fatal("a signature from another account was broadcast")
			}
			if len(chain.sent) != 0 {
				t.Error("a transaction was broadcast")
			}
		})
	}
}

func TestSend_refusesAChainKeyThatIsNotTheWallets(t *testing.T) {
	chain := newFakeChain()
	chain.account.PubKey = bytes.Repeat([]byte{3}, 33)
	if _, err := newClient(t, chain, newSigner()).RegisterOperator(context.Background()); err == nil {
		t.Fatal("a transaction was built for an account the wallet does not own")
	}
}

func TestNew_validatesItsInputs(t *testing.T) {
	if _, err := New(nil, newSigner(), testChainID); err == nil {
		t.Error("New accepted no chain")
	}
	if _, err := New(newFakeChain(), nil, testChainID); err == nil {
		t.Error("New accepted no signer")
	}
	for _, id := range []string{"", strings.Repeat("a", 65)} {
		if _, err := New(newFakeChain(), newSigner(), id); err == nil {
			t.Errorf("New accepted chain id %q", id)
		}
	}
}

func TestSend_aFeeBeyondTheCapIsNotSigned(t *testing.T) {
	for name, edit := range map[string]func(*fakeChain){
		"a huge base fee":         func(c *fakeChain) { c.baseFee = "9999999999999" },
		"a base fee of 41 digits": func(c *fakeChain) { c.baseFee = strings.Repeat("9", 41) },
		"a negative base fee":     func(c *fakeChain) { c.baseFee = "-1" },
		"a signed base fee":       func(c *fakeChain) { c.baseFee = "+5" },
		"gas that overflows":      func(c *fakeChain) { c.gasUsed = ^uint64(0) },
		"gas over the cap":        func(c *fakeChain) { c.gasUsed = MaxGas },
	} {
		t.Run(name, func(t *testing.T) {
			chain, signer := newFakeChain(), newSigner()
			edit(chain)
			if _, err := newClient(t, chain, signer).RegisterOperator(context.Background()); err == nil {
				t.Fatal("a transaction the chain node priced out of reason was signed")
			}
			if len(signer.signed) != 0 || len(chain.sent) != 0 {
				t.Errorf("signed %d, sent %d: nothing is signed when the figures are not believable", len(signer.signed), len(chain.sent))
			}
		})
	}
}

func TestSend_aFeeAtTheCapIsSigned(t *testing.T) {
	chain := newFakeChain()
	chain.gasUsed = 1_000_000
	chain.baseFee = "1000" // 1.5 * 1.5M gas * 1000 = 2.25e9 norama, under the 1e11 cap
	receipt, err := newClient(t, chain, newSigner()).RegisterOperator(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Fee != "2250000000" {
		t.Errorf("fee = %s", receipt.Fee)
	}
}
