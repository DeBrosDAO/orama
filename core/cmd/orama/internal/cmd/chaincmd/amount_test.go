package chaincmd

import (
	"strings"
	"testing"
)

func TestParseOramaAmount_values(t *testing.T) {
	for arg, want := range map[string]string{
		"1":            "1000000000",
		"12":           "12000000000",
		"0.5":          "500000000",
		"0.000000001":  "1",
		"007":          "7000000000",
		"1.123456789":  "1123456789",
		"999999999999": "999999999999000000000",
	} {
		got, err := parseOramaAmount(arg)
		if err != nil || got.String() != want {
			t.Errorf("parseOramaAmount(%q) = %v, %v; want %s", arg, got, err, want)
		}
	}
}

func TestParseOramaAmount_refusals(t *testing.T) {
	for _, arg := range []string{
		"", "0", "0.0", "0.000000000", "-1", "+1", "1e3", "1,5", "1.", ".5", "1.0000000001",
		"12 ORAMA", "0x10", "all", strings.Repeat("9", oramaMaxWholeDigits+1),
	} {
		if got, err := parseOramaAmount(arg); err == nil {
			t.Errorf("parseOramaAmount(%q) = %v, want an error", arg, got)
		}
	}
}
