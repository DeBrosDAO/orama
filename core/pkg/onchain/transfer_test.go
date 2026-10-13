package onchain

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

const testRecipient = "orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53"

func TestSend_publicSignsAndSendsABankPayment(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	receipt, err := newClient(t, chain, signer).Send(context.Background(), testRecipient, "1500000000", Public)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Height != 55 {
		t.Errorf("receipt = %+v", receipt)
	}
	doc := decodeSignDoc(t, signer.signed[0])
	if doc.typeURL != clusterreg.SendTypeURL {
		t.Errorf("type URL = %s", doc.typeURL)
	}
	msg := field(t, field(t, field(t, signer.signed[0], 1), 1), 2)
	if string(field(t, msg, 1)) != testOperator || string(field(t, msg, 2)) != testRecipient {
		t.Errorf("message parties = %q to %q", field(t, msg, 1), field(t, msg, 2))
	}
	if !bytes.Contains(msg, []byte("1500000000")) {
		t.Error("the message does not carry the amount")
	}
}

func TestSend_theZeroPrivacyIsPrivateAndNeverPays(t *testing.T) {
	var unchosen Privacy
	if unchosen != Private {
		t.Fatalf("the zero Privacy is %d, want Private", unchosen)
	}
	chain, signer := newFakeChain(), newSigner()
	_, err := newClient(t, chain, signer).Send(context.Background(), testRecipient, "1", unchosen)
	if !errors.Is(err, ErrPrivateUnavailable) {
		t.Fatalf("err = %v, want ErrPrivateUnavailable", err)
	}
	if len(signer.signed) != 0 || len(chain.sent) != 0 || len(chain.simulated) != 0 {
		t.Fatalf("a private request signed %d, sent %d, simulated %d; it must reach neither the wallet nor the chain",
			len(signer.signed), len(chain.sent), len(chain.simulated))
	}
}

func TestSend_anUnknownPrivacyIsRefused(t *testing.T) {
	chain := newFakeChain()
	if _, err := newClient(t, chain, newSigner()).Send(context.Background(), testRecipient, "1", Privacy(7)); err == nil {
		t.Fatal("an unknown privacy was accepted")
	}
	if len(chain.sent) != 0 {
		t.Fatal("an unknown privacy sent a transaction")
	}
}

func TestSend_publicRefusalsReachNeitherTheWalletNorTheChain(t *testing.T) {
	for name, c := range map[string]struct{ to, amount string }{
		"not an address": {"bob", "1"},
		"to yourself":    {testOperator, "1"},
		"zero":           {testRecipient, "0"},
		"fractional":     {testRecipient, "1.5"},
	} {
		chain, signer := newFakeChain(), newSigner()
		if _, err := newClient(t, chain, signer).Send(context.Background(), c.to, c.amount, Public); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if len(signer.signed) != 0 || len(chain.sent) != 0 {
			t.Errorf("%s: signed %d, sent %d", name, len(signer.signed), len(chain.sent))
		}
	}
}

func TestSend_aRefusedBroadcastNamesThePayment(t *testing.T) {
	chain := newFakeChain()
	chain.broadcast = errors.New("insufficient funds")
	_, err := newClient(t, chain, newSigner()).Send(context.Background(), testRecipient, "5", Public)
	if err == nil || !strings.Contains(err.Error(), "send 5 norama to "+testRecipient) || !strings.Contains(err.Error(), "insufficient funds") {
		t.Fatalf("err = %v", err)
	}
}

func TestWithdrawEarnings_signsTheMessageForTheSigner(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	if _, err := newClient(t, chain, signer).WithdrawEarnings(context.Background(), "250"); err != nil {
		t.Fatal(err)
	}
	doc := decodeSignDoc(t, signer.signed[0])
	if doc.typeURL != clusterreg.WithdrawEarningsTypeURL {
		t.Errorf("type URL = %s", doc.typeURL)
	}
	msg := field(t, field(t, field(t, signer.signed[0], 1), 1), 2)
	if string(field(t, msg, 1)) != testOperator || string(field(t, msg, 2)) != "250" {
		t.Errorf("message = signer %q amount %q", field(t, msg, 1), field(t, msg, 2))
	}
}

func TestWithdrawEarnings_refusesAnAmountThatIsNotPositive(t *testing.T) {
	chain := newFakeChain()
	for _, amount := range []string{"", "0", "-1", "all"} {
		if _, err := newClient(t, chain, newSigner()).WithdrawEarnings(context.Background(), amount); err == nil {
			t.Errorf("amount %q accepted", amount)
		}
	}
	if len(chain.sent) != 0 {
		t.Fatal("a refused amount was sent")
	}
}

func TestWithdrawEarnings_aChainRefusalIsReported(t *testing.T) {
	chain := newFakeChain()
	chain.waitErr = errors.New("the transaction failed in block 9 (code 5): insufficient earnings")
	_, err := newClient(t, chain, newSigner()).WithdrawEarnings(context.Background(), "9")
	if err == nil || !strings.Contains(err.Error(), "insufficient earnings") {
		t.Fatalf("err = %v", err)
	}
}

// The fee a caller shows for approval is the fee that is signed, and nothing is signed or sent
// before Submit.
func TestPreparePublicSend_showsTheFeeThatIsSigned(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	p, err := newClient(t, chain, signer).PreparePublicSend(context.Background(), testRecipient, "1500000000")
	if err != nil {
		t.Fatal(err)
	}
	if len(signer.signed) != 0 || len(chain.sent) != 0 {
		t.Fatalf("preparing signed %d and sent %d", len(signer.signed), len(chain.sent))
	}
	if p.Fee != "2250000" || p.Gas != 150_000 {
		t.Fatalf("prepared gas %d fee %s", p.Gas, p.Fee)
	}
	receipt, err := p.Submit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if doc := decodeSignDoc(t, signer.signed[0]); doc.fee != p.Fee || doc.gas != p.Gas || receipt.Fee != p.Fee {
		t.Fatalf("signed fee %s gas %d, receipt fee %s; prepared %s/%d", doc.fee, doc.gas, receipt.Fee, p.Fee, p.Gas)
	}
}

func TestPreparePublicSend_aFeeOverTheLimitIsNotPrepared(t *testing.T) {
	chain := newFakeChain()
	chain.baseFee = "100000000000"
	if _, err := newClient(t, chain, newSigner()).PreparePublicSend(context.Background(), testRecipient, "1"); err == nil {
		t.Fatal("an absurd fee was prepared")
	}
}
