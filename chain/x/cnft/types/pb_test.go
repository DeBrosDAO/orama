package types

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func TestProtoMarshalRoundTrip(t *testing.T) {
	msg := &MsgMint{
		Creator: "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq3z5rxx",
		TreeId:  7,
		Root:    bytes32(1),
		Leaves: []MintLeaf{{
			AssetId:     bytes32(2),
			Owner:       "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq3z5rxx",
			Delegate:    "",
			MetadataCid: "bafybeigdyrzt",
		}},
	}
	bz, err := msg.Marshal()
	require.NoError(t, err)
	var out MsgMint
	require.NoError(t, out.Unmarshal(bz))
	require.Equal(t, msg.Creator, out.Creator)
	require.Equal(t, msg.TreeId, out.TreeId)
	require.Equal(t, msg.Root, out.Root)
	require.Equal(t, msg.Leaves, out.Leaves)
}

func TestRegisterInterfaces(t *testing.T) {
	reg := types.NewInterfaceRegistry()
	require.NotPanics(t, func() { RegisterInterfaces(reg) })
	var msg sdk.Msg
	require.NoError(t, reg.UnpackAny(mustAny(t, &MsgCreateTree{
		Creator:      "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq3z5rxx",
		CollectionId: 1,
		Depth:        3,
		Buffer:       2,
		Canopy:       0,
	}), &msg))
	require.IsType(t, &MsgCreateTree{}, msg)
}

func bytes32(b byte) []byte {
	out := make([]byte, HashSize)
	out[0] = b
	return out
}

func mustAny(t *testing.T, msg sdk.Msg) *types.Any {
	t.Helper()
	any, err := types.NewAnyWithValue(msg)
	require.NoError(t, err)
	return any
}
