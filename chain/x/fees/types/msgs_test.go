package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

func init() {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
}

func TestMsgWithdrawEarnings_ValidateBasic(t *testing.T) {
	signer := sdk.AccAddress("signer_______________").String()
	cases := map[string]struct {
		msg     types.MsgWithdrawEarnings
		wantErr string
	}{
		"valid":          {msg: types.MsgWithdrawEarnings{Signer: signer, Amount: math.NewInt(1)}},
		"large amount":   {msg: types.MsgWithdrawEarnings{Signer: signer, Amount: math.NewInt(1_000_000).MulRaw(params.NoramaPerOrama)}},
		"zero":           {msg: types.MsgWithdrawEarnings{Signer: signer, Amount: math.ZeroInt()}, wantErr: "positive"},
		"negative":       {msg: types.MsgWithdrawEarnings{Signer: signer, Amount: math.NewInt(-1)}, wantErr: "positive"},
		"nil amount":     {msg: types.MsgWithdrawEarnings{Signer: signer}, wantErr: "positive"},
		"empty signer":   {msg: types.MsgWithdrawEarnings{Amount: math.NewInt(1)}, wantErr: "signer"},
		"garbage signer": {msg: types.MsgWithdrawEarnings{Signer: "orama1xyz", Amount: math.NewInt(1)}, wantErr: "signer"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.msg.ValidateBasic()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}
