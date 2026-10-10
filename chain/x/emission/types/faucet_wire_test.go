package types

import (
	"encoding/hex"
	"testing"

	"cosmossdk.io/math"
)

// faucetWireHex is the protobuf body of MsgFaucet for the fields below.
// core/pkg/clusterreg.EncodeFaucet must produce the same bytes.
const faucetWireHex = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a6775713061357475703073122c6f72616d613171717171717171717171717171717171717171717171717171717171717171716e72716c38611a0c313030303030303030303030"

func TestMsgFaucet_wireMatchesCoreEncoder(t *testing.T) {
	got, err := (&MsgFaucet{
		Signer:    "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		Recipient: "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqnrql8a",
		Amount:    math.NewInt(100_000_000_000),
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != faucetWireHex {
		t.Fatalf("wire %x", got)
	}
}
