package clusterreg

import (
	"encoding/hex"
	"strings"
	"testing"
)

// The chain module encodes the same messages with the SDK types and checks
// these bytes in chain/app/validator_wire_test.go.
const (
	vectorOperator  = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	vectorValidator = "oramavaloper19rl4cm2hmr8afy4kldpxz3fka4jguq0al2xuls"
	unjailWireHex   = "0a336f72616d6176616c6f7065723139726c34636d32686d7238616679346b6c6470787a33666b61346a67757130616c3278756c73"
	editWireHex     = "0a3f0a066e6f64652d61120f5b646f2d6e6f742d6d6f646966795d1a1368747470733a2f2f6578616d706c652e6f7267220f5b646f2d6e6f742d6d6f646966795d12336f72616d6176616c6f7065723139726c34636d32686d7238616679346b6c6470787a33666b61346a67757130616c3278756c731a113530303030303030303030303030303030"
)

func TestValidatorAddress_sameBytesUnderTheValoperPrefix(t *testing.T) {
	got, err := ValidatorAddress(vectorOperator)
	if err != nil {
		t.Fatal(err)
	}
	if got != vectorValidator {
		t.Fatalf("valoper = %s", got)
	}
	if _, err := ValidatorAddress(vectorValidator); err == nil {
		t.Fatal("a valoper address was accepted as an operator")
	}
	if _, err := ValidatorAddress(""); err == nil {
		t.Fatal("an empty operator was accepted")
	}
}

func TestEncodeUnjail_matchesTheChainWire(t *testing.T) {
	if got := hex.EncodeToString(EncodeUnjail(vectorValidator)); got != unjailWireHex {
		t.Fatalf("unjail wire %s", got)
	}
}

func TestEncodeEditValidator_matchesTheChainWire(t *testing.T) {
	e := NewValidatorEdit(vectorValidator)
	e.Moniker, e.Website, e.Details = "node-a", "https://example.org", ""
	e.CommissionRate = "0.05"
	got, err := EncodeEditValidator(e)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != editWireHex {
		t.Fatalf("edit wire %x", got)
	}
}

func TestEncodeEditValidator_unchangedFieldsAreMarked(t *testing.T) {
	got, err := EncodeEditValidator(NewValidatorEdit(vectorValidator))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(got), DoNotModify) != 5 {
		t.Fatalf("an unset description field is not %s", DoNotModify)
	}
}

func TestLegacyDecInteger_scalesBy1e18(t *testing.T) {
	cases := map[string]string{
		"0.05":                 "50000000000000000",
		"0":                    "0",
		"0.0":                  "0",
		"1":                    "1000000000000000000",
		"1.000":                "1000000000000000000",
		"0.000000000000000001": "1",
		"0.1":                  "100000000000000000",
	}
	for in, want := range cases {
		got, err := legacyDecInteger(in)
		if err != nil || got != want {
			t.Errorf("%s -> %q (%v), want %q", in, got, err, want)
		}
	}
}

func TestLegacyDecInteger_refusals(t *testing.T) {
	for _, bad := range []string{"", ".5", "1.5", "2", "-0.1", "0.0000000000000000001", "0.1e2", "0x1"} {
		if got, err := legacyDecInteger(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestEncodeEditValidator_allEmptyDescriptionRefused(t *testing.T) {
	e := ValidatorEdit{Validator: vectorValidator, CommissionRate: "0.1"}
	if _, err := EncodeEditValidator(e); err == nil {
		t.Fatal("an edit with an empty description was built")
	}
}
