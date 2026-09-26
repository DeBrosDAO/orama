package install

import "testing"

// --operator-wallet used to be a free-form string written into node.yaml and
// echoed into a dns_nodes column, so a typo produced a node nobody owned and
// nothing said so. It seeds the cluster's operator list now, and an operator
// list built from typos is an operator list nobody is on.
func TestValidateOperatorWallet_acceptsAnAddress(t *testing.T) {
	// An EIP-55 checksummed address, as a wallet displays it.
	f := &Flags{OperatorWallet: "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"}

	if err := f.validateOperatorWallet(); err != nil {
		t.Fatalf("a valid address was refused: %v", err)
	}
	if f.OperatorWallet != "0xfb6916095ca1df60bb79ce92ce3ea74c37c5d359" {
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

func TestRequireGenesisWallet(t *testing.T) {
	if err := (&Flags{}).requireGenesisWallet(); err == nil {
		t.Error("a genesis install without --operator-wallet was allowed")
	}
	for name, f := range map[string]*Flags{
		"genesis with wallet": {OperatorWallet: "0x1111111111111111111111111111111111111111"},
		"join":                {JoinAddress: "https://10.0.0.1", Token: "t"},
		"dry run":             {DryRun: true},
	} {
		if err := f.requireGenesisWallet(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestValidateOperatorWallet_checksTheEIP55Checksum(t *testing.T) {
	f := &Flags{OperatorWallet: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD"} // one letter's case flipped
	if err := f.validateOperatorWallet(); err == nil {
		t.Fatal("a mistyped checksummed wallet became the operator")
	}
	f = &Flags{OperatorWallet: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"}
	if err := f.validateOperatorWallet(); err != nil || f.OperatorWallet != "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed" {
		t.Fatalf("got %q, %v", f.OperatorWallet, err)
	}
}

func TestValidateExpectedSigners(t *testing.T) {
	join := func(v string) *Flags {
		return &Flags{JoinAddress: "https://10.0.0.1", Token: "t", ExpectArchiveSigners: v}
	}
	f := join(" 0x1111111111111111111111111111111111111111 ,0x2222222222222222222222222222222222222222")
	if err := f.validateExpectedSigners(); err != nil || len(f.expectedArchiveSigners) != 2 {
		t.Fatalf("got %v, %v", f.expectedArchiveSigners, err)
	}
	for _, bad := range []*Flags{join("0x12"), {ExpectArchiveSigners: "0x1111111111111111111111111111111111111111"}} {
		if err := bad.validateExpectedSigners(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
