package install

import "testing"

// --operator-wallet used to be a free-form string written into node.yaml and
// echoed into a dns_nodes column, so a typo produced a node nobody owned and
// nothing said so. It seeds the cluster's operator list now, and an operator
// list built from typos is an operator list nobody is on.
func TestValidateOperatorWallet_acceptsAnAddress(t *testing.T) {
	f := &Flags{OperatorWallet: "0x1234567890AbCdEf1234567890aBcDeF12345678"}

	if err := f.validateOperatorWallet(); err != nil {
		t.Fatalf("a valid address was refused: %v", err)
	}
	if f.OperatorWallet != "0x1234567890abcdef1234567890abcdef12345678" {
		t.Errorf("the address was stored as %q; it has to be normalised or the "+
			"operator list is keyed by capitalisation", f.OperatorWallet)
	}
}

func TestValidateOperatorWallet_emptyIsAllowed(t *testing.T) {
	f := &Flags{}
	if err := f.validateOperatorWallet(); err != nil {
		t.Fatalf("omitting the flag was refused: %v", err)
	}
}

func TestValidateOperatorWallet_refusesAnythingElse(t *testing.T) {
	for _, wallet := range []string{
		"not-an-address",
		"0x123", // too short
		"0x1234567890abcdef1234567890abcdef123456789",  // 41 hex
		"1234567890abcdef1234567890abcdef12345678",     // no 0x
		"0xZZZZ567890abcdef1234567890abcdef12345678",   // not hex
		"0x1234567890abcdef1234567890abcdef12345678 x", // trailing junk
	} {
		f := &Flags{OperatorWallet: wallet}
		if err := f.validateOperatorWallet(); err == nil {
			t.Errorf("%q was accepted as an operator wallet", wallet)
		}
	}
}
