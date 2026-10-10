package clusterreg

import (
	"encoding/hex"
	"strings"
	"testing"
)

// These hex strings are the bytes chain/x/nodes/types MsgFundHotKey.Marshal writes for the same
// message (fund_hot_key_wire_test.go asserts the same constants). The source is field 4, a varint
// (tag 0x20), and 1 is FUND_SOURCE_BANK.
const (
	fundHotKeyEarningsHex = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d611a0432353030"
	fundHotKeyBankHex     = fundHotKeyEarningsHex + "2001"
)

func hotKeyFunding(fromBank bool) HotKeyFunding {
	return HotKeyFunding{
		Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeID:   "node-a",
		Amount:   "2500",
		FromBank: fromBank,
	}
}

func TestEncodeFundHotKey_matchesChainMarshal(t *testing.T) {
	if got := hex.EncodeToString(EncodeFundHotKey(hotKeyFunding(false))); got != fundHotKeyEarningsHex {
		t.Errorf("earnings wire %s", got)
	}
	if got := hex.EncodeToString(EncodeFundHotKey(hotKeyFunding(true))); got != fundHotKeyBankHex {
		t.Errorf("bank wire %s", got)
	}
}

func TestEncodeFundHotKey_sourceIsWrittenOnlyForTheBank(t *testing.T) {
	earnings := fields(t, EncodeFundHotKey(hotKeyFunding(false)))
	if _, ok := earnings[4]; ok {
		t.Errorf("an earnings funding wrote field 4: %v", earnings[4])
	}
	if len(earnings[1]) != 1 || len(earnings[2]) != 1 || len(earnings[3]) != 1 {
		t.Errorf("fields = %v", earnings)
	}
}

func TestValidateFundHotKey_refusals(t *testing.T) {
	good := hotKeyFunding(true)
	if err := ValidateFundHotKey(good); err != nil {
		t.Fatalf("a valid funding was refused: %v", err)
	}
	for name, mutate := range map[string]func(*HotKeyFunding){
		"empty operator":     func(f *HotKeyFunding) { f.Operator = "" },
		"foreign operator":   func(f *HotKeyFunding) { f.Operator = "cosmos1abc" },
		"bad node id":        func(f *HotKeyFunding) { f.NodeID = "bad id" },
		"empty node id":      func(f *HotKeyFunding) { f.NodeID = "" },
		"zero amount":        func(f *HotKeyFunding) { f.Amount = "0" },
		"empty amount":       func(f *HotKeyFunding) { f.Amount = "" },
		"negative amount":    func(f *HotKeyFunding) { f.Amount = "-5" },
		"leading zero":       func(f *HotKeyFunding) { f.Amount = "05" },
		"amount past bounds": func(f *HotKeyFunding) { f.Amount = strings.Repeat("9", maxAmountDigits+1) },
	} {
		t.Run(name, func(t *testing.T) {
			f := good
			mutate(&f)
			if err := ValidateFundHotKey(f); err == nil {
				t.Fatal("it was accepted")
			}
		})
	}
}
