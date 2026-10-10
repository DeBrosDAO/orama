package onchain

import (
	"bytes"
	"context"
	"errors"
	"math"
	"math/big"
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
	if got, err := scaleUp(1, GasSafetyNumerator, GasSafetyDenominator); err != nil || got != 2 {
		t.Errorf("scaleUp(1) = %d, %v, want 2", got, err)
	}
	if got, err := scaleUp(0, GasSafetyNumerator, GasSafetyDenominator); err != nil || got != 0 {
		t.Errorf("scaleUp(0) = %d, %v, want 0", got, err)
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
		"block refuses":     {func(c *fakeChain, _ *fakeSigner) { c.waitErr = boom }, "transaction ", 1, 1},
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
	for _, id := range []string{"", strings.Repeat("a", 65), "orama stagenet", "orama-1\x1b[2J", "orama/1", "orama-1\n", "chaîn"} {
		if _, err := New(newFakeChain(), newSigner(), id); err == nil {
			t.Errorf("New accepted chain id %q", id)
		}
	}
}

func TestValidChainID(t *testing.T) {
	for id, want := range map[string]bool{"orama-stagenet-5": true, "a": true, strings.Repeat("a", 64): true, "": false, strings.Repeat("a", 65): false, "a b": false, "a\x1b": false, "é": false} {
		if got := ValidChainID(id); got != want {
			t.Errorf("ValidChainID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestNew_acceptsTheChainIDsTheNetworksUse(t *testing.T) {
	for _, id := range []string{"orama-stagenet-5", "orama-1", "orama_localnet.2", strings.Repeat("a", 64)} {
		if _, err := New(newFakeChain(), newSigner(), id); err != nil {
			t.Errorf("New refused chain id %q: %v", id, err)
		}
	}
}

// The gas limit is the simulation's gas scaled by 1.5: a simulation near the top of uint64 must be an
// error, not a limit that wrapped around to a small number.
func TestSend_aGasLimitThatOverflowsIsRefused(t *testing.T) {
	if _, err := scaleUp(math.MaxUint64, GasSafetyNumerator, GasSafetyDenominator); err == nil {
		t.Fatal("scaleUp wrapped around")
	}
	chain, signer := newFakeChain(), newSigner()
	chain.gasUsed = math.MaxUint64 - 1
	_, err := newClient(t, chain, signer).RegisterOperator(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not fit") {
		t.Fatalf("err = %v", err)
	}
	if len(signer.signed) != 0 || len(chain.sent) != 0 {
		t.Fatal("a wrapped gas limit was signed")
	}
}

func TestParseBaseFee(t *testing.T) {
	for _, ok := range []string{"0", "1", "10", "007", strings.Repeat("9", maxBaseFeeDigits)} {
		if _, err := ParseBaseFee(ok); err != nil {
			t.Errorf("ParseBaseFee(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-1", "+5", "0x10", "1e3", "1.5", " 5", "5 ", "5\n", "1_000", strings.Repeat("9", maxBaseFeeDigits+1), "\x1b[2J"} {
		if _, err := ParseBaseFee(bad); err == nil {
			t.Errorf("ParseBaseFee(%q) succeeded", bad)
		}
	}
}

func TestSend_aBaseFeeThatIsNotAPlainIntegerSignsNothing(t *testing.T) {
	for _, fee := range []string{"-5", "1e30", strings.Repeat("9", 40), "0x10", ""} {
		chain, signer := newFakeChain(), newSigner()
		chain.baseFee = fee
		if _, err := newClient(t, chain, signer).RegisterOperator(context.Background()); err == nil {
			t.Errorf("base fee %q accepted", fee)
		}
		if len(signer.signed) != 0 || len(chain.sent) != 0 {
			t.Errorf("base fee %q: signed %d, sent %d", fee, len(signer.signed), len(chain.sent))
		}
	}
}

// A hostile base fee that is a valid number but absurd must not become a fee the wallet signs.
func TestSend_aFeeOverTheLimitIsRefusedUntilTheLimitIsRaised(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	chain.baseFee = "100000000000" // 150,000 gas x 1.5 x 1e11 = 2.25e16 norama
	c := newClient(t, chain, signer)
	_, err := c.RegisterOperator(context.Background())
	if err == nil || !strings.Contains(err.Error(), "over the limit") {
		t.Fatalf("err = %v", err)
	}
	if len(signer.signed) != 0 || len(chain.sent) != 0 {
		t.Fatal("an absurd fee was signed")
	}
	if err := c.SetMaxFee(new(big.Int).Mul(big.NewInt(DefaultMaxFeeNorama), big.NewInt(1_000_000_000))); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RegisterOperator(context.Background()); err != nil {
		t.Fatalf("an explicit limit above the fee still refused: %v", err)
	}
}

func TestSend_aNormalFeeIsFarUnderTheDefaultLimit(t *testing.T) {
	receipt, err := newClient(t, newFakeChain(), newSigner()).RegisterOperator(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fee, _ := new(big.Int).SetString(receipt.Fee, 10)
	if fee.Cmp(big.NewInt(DefaultMaxFeeNorama/100)) > 0 {
		t.Fatalf("a normal fee %s is not under a hundredth of the default limit", receipt.Fee)
	}
}

func TestSetMaxFee_refusesAnythingButAPositiveAmount(t *testing.T) {
	c := newClient(t, newFakeChain(), newSigner())
	for _, v := range []*big.Int{nil, big.NewInt(0), big.NewInt(-1)} {
		if err := c.SetMaxFee(v); err == nil {
			t.Errorf("SetMaxFee(%v) succeeded", v)
		}
	}
}

// The chain's answer to a broadcast must be the hash of the bytes that were sent. Another hash
// would have the wait, and the receipt, follow some other transaction.
func TestSend_aHashThatIsNotTheTransactionsIsRefused(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	chain.answerHash = strings.Repeat("AB", 32)
	_, err := newClient(t, chain, signer).RegisterOperator(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refusing to follow or report") {
		t.Fatalf("err = %v", err)
	}
	if len(chain.waited) != 0 {
		t.Fatalf("waited on %v", chain.waited)
	}
}

func TestSend_theReceiptCarriesTheLocallyComputedHash(t *testing.T) {
	chain := newFakeChain()
	c := newClient(t, chain, newSigner())
	receipt, err := c.RegisterOperator(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Hash != TxHash(chain.sent[0]) || len(chain.waited) != 1 || chain.waited[0] != receipt.Hash {
		t.Fatalf("receipt %s, waited %v", receipt.Hash, chain.waited)
	}
}

// A node may answer the right hash in lower case; it is the same hash, and the client follows and
// reports its own upper-case form.
func TestSend_aLowerCaseAnswerOfTheRightHashIsAccepted(t *testing.T) {
	chain := newFakeChain()
	c := newClient(t, chain, newSigner())
	first, err := c.RegisterOperator(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The fake chain does not move, so the second transaction is the same bytes with the same hash.
	chain.answerHash = strings.ToLower(first.Hash)
	second, err := c.RegisterOperator(context.Background())
	if err != nil {
		t.Fatalf("a lower-case answer of the right hash was refused: %v", err)
	}
	if second.Hash != first.Hash || chain.waited[len(chain.waited)-1] != first.Hash {
		t.Fatalf("second %s, waited %v", second.Hash, chain.waited)
	}
}

func TestTxHash_isTheSHA256OfTheBytesInUpperCaseHex(t *testing.T) {
	// SHA-256 of the empty input.
	if got := TxHash(nil); got != "E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855" {
		t.Fatalf("TxHash(nil) = %s", got)
	}
}

// Both chains report "no gas used" the same way, so the refusal is the client's and not a chain's.
func TestSend_aSimulationThatUsedNoGasIsRefused(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	chain.gasUsed = 0
	_, err := newClient(t, chain, signer).RegisterOperator(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no gas used") {
		t.Fatalf("err = %v", err)
	}
	if len(signer.signed) != 0 || len(chain.sent) != 0 {
		t.Fatal("a zero gas limit was signed")
	}
}
