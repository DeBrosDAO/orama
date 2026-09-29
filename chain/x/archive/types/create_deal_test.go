package types_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

func validCreateDeal() *types.MsgCreateArchiveDeal {
	return &types.MsgCreateArchiveDeal{
		Archiver:        sdk.AccAddress(bytes.Repeat([]byte{9}, 20)).String(),
		NodeId:          "node-1",
		StartHeight:     1,
		EndHeight:       1000,
		PieceRoot:       bytes.Repeat([]byte{7}, types.HashLen),
		RealLeafCount:   3,
		PaddedLeafCount: 4,
		PieceBytes:      3000,
	}
}

func TestMsgCreateArchiveDeal_validateBasic(t *testing.T) {
	require.NoError(t, validCreateDeal().ValidateBasic())
	signer := sdk.AccAddress(bytes.Repeat([]byte{9}, 20))
	require.Equal(t, []sdk.AccAddress{signer}, validCreateDeal().GetSigners())

	for name, mutate := range map[string]func(*types.MsgCreateArchiveDeal){
		"bad archiver":         func(m *types.MsgCreateArchiveDeal) { m.Archiver = "nope" },
		"no node":              func(m *types.MsgCreateArchiveDeal) { m.NodeId = "" },
		"heights reversed":     func(m *types.MsgCreateArchiveDeal) { m.StartHeight, m.EndHeight = 10, 5 },
		"start below one":      func(m *types.MsgCreateArchiveDeal) { m.StartHeight = 0 },
		"short root":           func(m *types.MsgCreateArchiveDeal) { m.PieceRoot = m.PieceRoot[:31] },
		"no bytes":             func(m *types.MsgCreateArchiveDeal) { m.PieceBytes = 0 },
		"no leaves":            func(m *types.MsgCreateArchiveDeal) { m.RealLeafCount = 0 },
		"padding below leaves": func(m *types.MsgCreateArchiveDeal) { m.PaddedLeafCount = 2 },
	} {
		m := validCreateDeal()
		mutate(m)
		require.Errorf(t, m.ValidateBasic(), "%s must be refused", name)
	}
	var nilMsg *types.MsgCreateArchiveDeal
	require.Error(t, nilMsg.ValidateBasic())
}
