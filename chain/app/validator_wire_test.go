package app

import (
	"encoding/hex"
	"testing"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// core/pkg/clusterreg builds MsgUnjail and MsgEditValidator by hand (core does
// not import the SDK). These are the bytes its tests pin; the SDK types must
// produce the same ones.
const (
	wireOperator      = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	wireValidator     = "oramavaloper19rl4cm2hmr8afy4kldpxz3fka4jguq0al2xuls"
	unjailWireHex     = "0a336f72616d6176616c6f7065723139726c34636d32686d7238616679346b6c6470787a33666b61346a67757130616c3278756c73"
	editValidatorWire = "0a3f0a066e6f64652d61120f5b646f2d6e6f742d6d6f646966795d1a1368747470733a2f2f6578616d706c652e6f7267220f5b646f2d6e6f742d6d6f646966795d12336f72616d6176616c6f7065723139726c34636d32686d7238616679346b6c6470787a33666b61346a67757130616c3278756c731a113530303030303030303030303030303030"
)

func TestValidatorMsgs_wireMatchesCoreEncoder(t *testing.T) {
	hrp, acc, err := bech32.DecodeAndConvert(wireOperator)
	if err != nil || hrp != params.Bech32Prefix {
		t.Fatalf("decode %s: %q %v", wireOperator, hrp, err)
	}
	got, err := bech32.ConvertAndEncode(params.Bech32PrefixValAddr, acc)
	if err != nil {
		t.Fatal(err)
	}
	if got != wireValidator {
		t.Fatalf("valoper of %s is %s, core says %s", wireOperator, got, wireValidator)
	}

	unjail, err := (&slashingtypes.MsgUnjail{ValidatorAddr: wireValidator}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(unjail) != unjailWireHex {
		t.Fatalf("unjail wire %x", unjail)
	}

	rate := math.LegacyMustNewDecFromStr("0.05")
	edit := &stakingtypes.MsgEditValidator{
		Description: stakingtypes.Description{
			Moniker:         "node-a",
			Identity:        stakingtypes.DoNotModifyDesc,
			Website:         "https://example.org",
			SecurityContact: stakingtypes.DoNotModifyDesc,
		},
		ValidatorAddress: wireValidator,
		CommissionRate:   &rate,
	}
	wire, err := edit.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(wire) != editValidatorWire {
		t.Fatalf("edit validator wire %x", wire)
	}
}
