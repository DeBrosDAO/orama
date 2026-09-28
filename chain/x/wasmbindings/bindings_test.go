package wasmbindings_test

import (
	"testing"

	"cosmossdk.io/errors"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/wasmbindings"
)

func TestDefaultBindingsReturnNotLinked(t *testing.T) {
	bindings := wasmbindings.Default{}
	calls := []struct {
		name string
		err  error
	}{
		{"token create", bindings.Token.Create("contract", "uusd")},
		{"token mint", bindings.Token.Mint("contract", "uusd", "to", "1")},
		{"token burn", bindings.Token.Burn("contract", "uusd", "from", "1")},
		{"cnft mint", bindings.CNFT.Mint("contract", "tree", "owner")},
		{"cnft verify", bindings.CNFT.VerifyProof("tree", []byte{1}, []byte{2})},
		{"market list", bindings.Market.List("contract", "listing")},
		{"market bid", bindings.Market.Bid("contract", "listing")},
		{"market settle", bindings.Market.Settle("contract", "listing")},
		{"storage deal", bindings.Storage.CreateDeal("contract", "bafy")},
		{"shielded shield", bindings.Shielded.Shield("contract", "1")},
		{"shielded unshield", bindings.Shielded.ReceiveUnshield("contract", "1")},
	}
	require.NotEmpty(t, calls)
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			require.ErrorIs(t, call.err, wasmbindings.ErrNotLinked)
			require.True(t, errors.IsOf(call.err, wasmbindings.ErrNotLinked))
			require.Contains(t, call.err.Error(), wasmbindings.NotLinkedCode)
		})
	}
}
