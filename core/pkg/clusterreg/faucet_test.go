package clusterreg

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

const testFaucetSigner = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"

// testFaucetRecipient is the recipient of the chain's wire vector, which the chain marshals without
// checking it; testRecipientAccount is a real account.
const testFaucetRecipient = "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqnrql8a"

func testRecipientAccount(t *testing.T) string {
	t.Helper()
	addr, err := bech32Encode(accountHRP, bytes.Repeat([]byte{7}, accountBytes))
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

// The wire hex is the one chain/x/emission/types/faucet_wire_test.go checks against the chain's own
// Marshal, so the two encoders cannot drift apart.
func TestEncodeFaucet_matchesChainMarshal(t *testing.T) {
	const want = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a6775713061357475703073122c6f72616d613171717171717171717171717171717171717171717171717171717171717171716e72716c38611a0c313030303030303030303030"
	got := EncodeFaucet(Faucet{Signer: testFaucetSigner, Recipient: testFaucetRecipient, Amount: "100000000000"})
	if hex.EncodeToString(got) != want {
		t.Fatalf("wire %x", got)
	}
}

func TestValidateFaucet(t *testing.T) {
	ok := Faucet{Signer: testFaucetSigner, Recipient: testRecipientAccount(t), Amount: "100000000000"}
	if err := ValidateFaucet(ok); err != nil {
		t.Fatalf("a good drip was refused: %v", err)
	}
	for name, edit := range map[string]func(*Faucet){
		"no signer":            func(f *Faucet) { f.Signer = "" },
		"a bad signer":         func(f *Faucet) { f.Signer = "orama1x" },
		"no recipient":         func(f *Faucet) { f.Recipient = "" },
		"an uppercase address": func(f *Faucet) { f.Recipient = strings.ToUpper(f.Recipient) },
		"another prefix":       func(f *Faucet) { f.Recipient = "cosmos1" + strings.TrimPrefix(f.Recipient, "orama1") },
		"no amount":            func(f *Faucet) { f.Amount = "" },
		"a zero amount":        func(f *Faucet) { f.Amount = "0" },
		"a negative amount":    func(f *Faucet) { f.Amount = "-5" },
		"a decimal amount":     func(f *Faucet) { f.Amount = "1.5" },
		"a leading zero":       func(f *Faucet) { f.Amount = "05" },
	} {
		t.Run(name, func(t *testing.T) {
			f := ok
			edit(&f)
			if err := ValidateFaucet(f); err == nil {
				t.Fatal("the drip was accepted")
			}
		})
	}
}
