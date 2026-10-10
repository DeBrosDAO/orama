package clusterreg

import (
	"bytes"
	"strings"
	"testing"
)

// otherAccount is a canonical orama account that is not vectorAddress.
func otherAccount(t *testing.T) string {
	t.Helper()
	addr, err := bech32Encode(accountHRP, bytes.Repeat([]byte{9}, accountBytes))
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

func TestEncodeSend_decodesToTheBankMessage(t *testing.T) {
	to := otherAccount(t)
	got, err := EncodeSend(vectorAddress, to, "1500000000")
	if err != nil {
		t.Fatal(err)
	}
	f := fields(t, got)
	if string(f[1][0]) != vectorAddress || string(f[2][0]) != to || len(f[3]) != 1 {
		t.Fatalf("fields = %v", f)
	}
	coin := fields(t, f[3][0])
	if string(coin[1][0]) != "norama" || string(coin[2][0]) != "1500000000" {
		t.Errorf("coin = %v", coin)
	}
}

func TestEncodeSend_matchesTheWireOfTheRootWalletVector(t *testing.T) {
	// The vector's body carries a MsgSend of 1500000000 norama from an account to itself. The same
	// message through the sign-document encoder must hold the bytes EncodeSend would write.
	to := otherAccount(t)
	msg, err := EncodeSend(vectorAddress, to, "1500000000")
	if err != nil {
		t.Fatal(err)
	}
	doc := EncodeMsgSendSignDoc(vectorAddress, to, "1500000000", "", nil, 0, "1", 1, "c", 0)
	if !bytes.Contains(doc, msg) {
		t.Fatalf("the sign document does not contain the message\n doc %x\n msg %x", doc, msg)
	}
}

func TestEncodeSend_refusals(t *testing.T) {
	to := otherAccount(t)
	cases := map[string]struct{ from, to, amount string }{
		"sender not an account":    {"orama1xyz", to, "1"},
		"recipient not an account": {vectorAddress, "cosmos1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqnrql8a", "1"},
		"a valoper recipient":      {vectorAddress, vectorValidator, "1"},
		"to yourself":              {vectorAddress, vectorAddress, "1"},
		"zero":                     {vectorAddress, to, "0"},
		"empty":                    {vectorAddress, to, ""},
		"leading zero":             {vectorAddress, to, "01"},
		"negative":                 {vectorAddress, to, "-5"},
		"decimal":                  {vectorAddress, to, "1.5"},
		"too many digits":          {vectorAddress, to, strings.Repeat("9", maxAmountDigits+1)},
	}
	for name, c := range cases {
		if _, err := EncodeSend(c.from, c.to, c.amount); err == nil {
			t.Errorf("%s: EncodeSend(%q, %q, %q) succeeded", name, c.from, c.to, c.amount)
		}
	}
}

func TestEncodeSend_largestAmountIsAccepted(t *testing.T) {
	if _, err := EncodeSend(vectorAddress, otherAccount(t), strings.Repeat("9", maxAmountDigits)); err != nil {
		t.Fatalf("%d digits refused: %v", maxAmountDigits, err)
	}
}

func TestEncodeWithdrawEarnings_isTheSignerAndTheAmount(t *testing.T) {
	got, err := EncodeWithdrawEarnings(vectorAddress, "42")
	if err != nil {
		t.Fatal(err)
	}
	f := fields(t, got)
	if len(f) != 2 || string(f[1][0]) != vectorAddress || string(f[2][0]) != "42" {
		t.Fatalf("fields = %v", f)
	}
}

func TestEncodeWithdrawEarnings_refusals(t *testing.T) {
	for name, c := range map[string]struct{ signer, amount string }{
		"not an account": {"orama1xyz", "1"},
		"zero":           {vectorAddress, "0"},
		"empty":          {vectorAddress, ""},
		"text":           {vectorAddress, "all"},
	} {
		if _, err := EncodeWithdrawEarnings(c.signer, c.amount); err == nil {
			t.Errorf("%s: EncodeWithdrawEarnings(%q, %q) succeeded", name, c.signer, c.amount)
		}
	}
}
