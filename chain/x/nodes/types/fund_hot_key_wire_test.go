package types

import (
	"encoding/hex"
	"testing"

	"cosmossdk.io/math"
)

// The wire of MsgFundHotKey. Field 4, source, is a varint (tag 0x20) written only for the bank
// source: proto3 omits the zero value, so an earnings funding is byte for byte what it was before
// the field existed. core/pkg/clusterreg encodes the same bytes by hand and asserts the same hex.
const (
	fundHotKeyEarningsHex = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d611a0432353030"
	fundHotKeyBankHex     = fundHotKeyEarningsHex + "2001"
)

func fundHotKeyMsg(source FundSource) *MsgFundHotKey {
	return &MsgFundHotKey{
		Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeId:   "node-a",
		Amount:   math.NewInt(2500),
		Source:   source,
	}
}

func TestMsgFundHotKey_wireMatchesCoreEncoder(t *testing.T) {
	for name, tc := range map[string]struct {
		source FundSource
		want   string
	}{
		"earnings omits the field": {FundSource_FUND_SOURCE_EARNINGS, fundHotKeyEarningsHex},
		"bank is field 4 value 1":  {FundSource_FUND_SOURCE_BANK, fundHotKeyBankHex},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := fundHotKeyMsg(tc.source).Marshal()
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(got) != tc.want {
				t.Fatalf("wire %x", got)
			}
		})
	}
}

func TestMsgFundHotKey_validateBasicRefusesAnUnknownSource(t *testing.T) {
	for _, source := range []FundSource{FundSource_FUND_SOURCE_EARNINGS, FundSource_FUND_SOURCE_BANK} {
		if err := fundHotKeyMsg(source).ValidateBasic(); err != nil {
			t.Errorf("source %s refused: %v", source, err)
		}
	}
	for _, source := range []FundSource{2, 99, -1} {
		if err := fundHotKeyMsg(source).ValidateBasic(); err == nil {
			t.Errorf("source %d accepted", source)
		}
	}
}
